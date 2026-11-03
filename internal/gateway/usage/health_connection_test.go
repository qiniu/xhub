package usage

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	_ "github.com/sunqirui1987/xhub/internal/provider/all"
)

// healthRoundTripper 把连接探测限制在测试进程内；参数是待发请求，返回测试指定响应。
type healthRoundTripper func(*http.Request) (*http.Response, error)

// RoundTrip 执行测试回调；参数为探测请求，返回内存响应或失败，不访问外部服务。
func (f healthRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// healthHost 只实现连接测试所需宿主能力；嵌入接口让无关方法保持不可调用。
type healthHost struct {
	Host
	models    []config.ModelEntry
	attached  config.ModelEntry
	attachErr error
	client    *http.Client
	attachN   int
}

// RequireManage 模拟已登录平台管理员；参数为响应与请求，返回固定主体。
func (h *healthHost) RequireManage(http.ResponseWriter, *http.Request) *auth.Principal {
	return &auth.Principal{UserID: "admin"}
}

// ModelList 返回测试部署副本；无参数，结果只供服务端 id 匹配。
func (h *healthHost) ModelList() []config.ModelEntry {
	return append([]config.ModelEntry(nil), h.models...)
}

// AttachCredential 模拟凭据装载；参数为已匹配部署，返回测试配置或可能含秘密的错误。
func (h *healthHost) AttachCredential(config.ModelEntry) (config.ModelEntry, error) {
	h.attachN++
	return h.attached, h.attachErr
}

// HTTPClient 返回进程内客户端；无参数，不改变共享状态。
func (h *healthHost) HTTPClient() *http.Client { return h.client }

// healthDeployment 构造带服务端 id 的部署；参数 transport 决定对话或媒体能力。
func healthDeployment(transport string) config.ModelEntry {
	endpoint := "chat"
	if strings.Contains(transport, "image") {
		endpoint = "image_generation"
	}
	return config.ModelEntry{ModelName: "health-model", LiteLLMParams: map[string]any{
		"model": "upstream-health", "api_base": "https://secret.invalid/v1", "api_key": "secret-key-value",
	}, ModelInfo: map[string]any{"id": "health-deployment", "transport": transport, "endpoint_types": []string{endpoint}}}
}

// runHealthConnection 调用处理器并解析 JSON；参数为宿主、模式和部署 id，返回状态码与响应对象。
func runHealthConnection(t *testing.T, h *healthHost, mode, id string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"model_info": map[string]any{"id": id}, "mode": mode})
	req := httptest.NewRequest(http.MethodPost, "/health/test_connection", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	HealthTestConnection(h, rec, req)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("连接测试响应不是 JSON: %v: %s", err, rec.Body.String())
	}
	return rec.Code, got
}

// TestHealthTestConnectionSafeDiagnostics 验证凭据和网络失败可观察且不泄露 URL、密钥或底层错误。
func TestHealthTestConnectionSafeDiagnostics(t *testing.T) {
	dep := healthDeployment("bypass_openai_chat")
	tests := []struct {
		name      string
		attachErr error
		transport healthRoundTripper
		wantCode  string
	}{
		{name: "credential", attachErr: errors.New("vault https://secret.invalid secret-key-value"), wantCode: "credential_unavailable"},
		{name: "network", transport: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial https://secret.invalid?api_key=secret-key-value")
		}, wantCode: "request_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &healthHost{models: []config.ModelEntry{dep}, attached: dep, attachErr: tt.attachErr}
			if tt.transport != nil {
				h.client = &http.Client{Transport: tt.transport}
			}
			status, got := runHealthConnection(t, h, "bypass_openai_chat", "health-deployment")
			if status != http.StatusOK || got["status"] != "error" || got["healthy"] != false || got["error"] != tt.wantCode {
				t.Fatalf("连接失败契约错误: status=%d body=%#v", status, got)
			}
			result, _ := got["result"].(map[string]any)
			if got["message"] == "" || result["error"] != got["message"] {
				t.Fatalf("前端不可观察诊断: %#v", got)
			}
			raw, _ := json.Marshal(got)
			for _, secret := range []string{"secret.invalid", "secret-key-value", "api_key="} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("响应泄露秘密 %q: %s", secret, raw)
				}
			}
		})
	}
}

// TestHealthTestConnectionRejectsMediaWithoutProbe 验证媒体部署和错配模式不装载凭据、不访问上游。
func TestHealthTestConnectionRejectsMediaWithoutProbe(t *testing.T) {
	for _, tc := range []struct{ name, transport, mode string }{
		{name: "media deployment", transport: "openai_image_generation", mode: "openai_image_generation"},
		{name: "mismatched mode", transport: "bypass_openai_chat", mode: "image_generation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep := healthDeployment(tc.transport)
			h := &healthHost{models: []config.ModelEntry{dep}, attached: dep, client: &http.Client{Transport: healthRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Fatal("不支持的模式不应访问上游")
				return nil, nil
			})}}
			_, got := runHealthConnection(t, h, tc.mode, "health-deployment")
			if got["error"] != "unsupported_mode" || got["message"] == "" || h.attachN != 0 {
				t.Fatalf("媒体拒绝契约错误: attach=%d body=%#v", h.attachN, got)
			}
		})
	}
}

// TestHealthTestConnectionSuccess 验证匹配对话 transport 发出最小请求并返回健康。
func TestHealthTestConnectionSuccess(t *testing.T) {
	dep := healthDeployment("bypass_openai_chat")
	calls := 0
	h := &healthHost{models: []config.ModelEntry{dep}, attached: dep, client: &http.Client{Transport: healthRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "Reply with OK.") || r.Header.Get("Authorization") != "Bearer secret-key-value" {
			t.Fatalf("探测请求错误: path=%s body=%s", r.URL.Path, raw)
		}
		response := "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"OK\"}}]}"
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}}
	_, got := runHealthConnection(t, h, "bypass_openai_chat", "health-deployment")
	if got["status"] != "success" || got["healthy"] != true || calls != 1 {
		t.Fatalf("连接成功契约错误: calls=%d body=%#v", calls, got)
	}
}

// TestHealthServicesDoesNotInventHealth 验证服务健康未实现时明确501，退役告警服务仍400；内存处理器不访问网络或数据库。
func TestHealthServicesDoesNotInventHealth(t *testing.T) {
	for _, service := range []string{"", "redis", "database", "unknown", "email", "ms_teams", "msteams", "slack"} {
		w := httptest.NewRecorder()
		HealthServices(&healthHost{}, w, httptest.NewRequest("GET", "/health/services?service="+service, nil))
		want := 501
		if service == "email" || service == "ms_teams" || service == "msteams" || service == "slack" {
			want = 400
		}
		if w.Code != want {
			t.Fatal("无探测却报告健康或退役语义改变", service, w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if want == 501 {
			e, _ := body["error"].(map[string]any)
			if e["type"] != "unsupported_operation" || body["status"] != nil {
				t.Fatal("应明确不支持", body)
			}
		}
	}
}
