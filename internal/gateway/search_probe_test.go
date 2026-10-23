package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// searchProbeBrokenReader 为正文读取故障提供确定性输入；不访问外部资源，Read始终返回测试错误。
type searchProbeBrokenReader struct{}

// Read 模拟入站正文传输中断；参数缓冲区不修改，返回零字节和固定错误供解析器拒绝。
func (searchProbeBrokenReader) Read([]byte) (int, error) { return 0, errors.New("broken request body") }

// TestReadSearchProbeInput 验证单个JSON对象和互斥配置来源；正常ID/临时配置通过，损坏或模糊输入失败，无资源需清理。
// 参数t为测试上下文，验证错误后不继续构造HTTP请求。
func TestReadSearchProbeInput(t *testing.T) {
	for name, raw := range map[string]string{"id": `{"search_tool_id":"id"}`, "temporary": `{"litellm_params":{"search_provider":"tavily"},"query":"保持原文"}`} {
		t.Run(name, func(t *testing.T) {
			if _, err := readSearchProbeInput(strings.NewReader(raw)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, raw := range map[string]string{"empty": "", "null": "null", "array": "[]", "scalar": "42", "truncated": `{"search_tool_id":`, "trailing": `{"search_tool_id":"id"} {}`, "both": `{"search_tool_id":"id","litellm_params":{}}`, "none": "{}", "wrong-id": `{"search_tool_id":42}`, "wrong-params": `{"litellm_params":[]}`, "wrong-query": `{"search_tool_id":"id","query":2}`} {
		t.Run(name, func(t *testing.T) {
			if _, err := readSearchProbeInput(strings.NewReader(raw)); err == nil {
				t.Fatal("错误输入未被拒绝")
			}
		})
	}
	if _, err := readSearchProbeInput(searchProbeBrokenReader{}); err == nil {
		t.Fatal("正文读取错误被忽略")
	}
	if _, err := readSearchProbeInput(nil); err == nil {
		t.Fatal("缺少正文未被拒绝")
	}
}

// TestMakeSearchProbeConfig 验证供应商、完整端点与凭据边界；参数t为上下文，仅构造配置不访问供应商，无需清理。
// 正常查询逐字保留，缺少/未解析/掩码凭据、携密URL、未知供应商不能发出探测请求。
func TestMakeSearchProbeConfig(t *testing.T) {
	for provider, endpoint := range map[string]string{"tavily": "https://api.tavily.com/search", "perplexity": "https://api.perplexity.ai/search", "serper": "https://google.serper.dev/search"} {
		cfg, failure := makeSearchProbeConfig(map[string]any{"search_provider": provider, "api_key": "fixture"}, nil)
		if failure != nil || cfg.Endpoint != endpoint || cfg.Query != searchProbeQuery {
			t.Fatalf("默认协议配置错误: %+v %+v", cfg, failure)
		}
	}
	query := "  中文 query  "
	cfg, failure := makeSearchProbeConfig(map[string]any{"search_provider": "tavily", "api_key": "fixture", "api_base": "http://127.0.0.1:1234/custom/search"}, &query)
	if failure != nil || cfg.Query != query || cfg.Endpoint != "http://127.0.0.1:1234/custom/search" {
		t.Fatalf("完整endpoint或查询原文被改写: %+v %+v", cfg, failure)
	}
	for _, key := range []string{"", "********", "sk-***tail", "os.environ/TAVILY_API_KEY", "${TAVILY_API_KEY}", "env/KEY", "key\r\nInjected: 1"} {
		_, failure := makeSearchProbeConfig(map[string]any{"search_provider": "tavily", "api_key": key}, nil)
		if failure == nil || failure.Code != "missing_credential" {
			t.Errorf("不安全凭据未拒绝: %+v", failure)
		}
	}
	for _, base := range []any{"file:///tmp/secret", "https://user:pass@example.com/search", "https://example.com/search?api_key=secret", "https://example.com/search#secret", "/relative", 42} {
		_, failure := makeSearchProbeConfig(map[string]any{"search_provider": "tavily", "api_key": "fixture", "api_base": base}, nil)
		if failure == nil || failure.Code != "invalid_api_base" {
			t.Errorf("无效端点未拒绝: %+v", failure)
		}
	}
	if _, failure := makeSearchProbeConfig(map[string]any{"search_provider": "unknown", "api_key": "fixture"}, nil); failure == nil || failure.Code != "unsupported_provider" {
		t.Fatal("未知协议未拒绝")
	}
	blank := " \n"
	if _, failure := makeSearchProbeConfig(map[string]any{"search_provider": "tavily", "api_key": "fixture"}, &blank); failure == nil || failure.Code != "invalid_request" {
		t.Fatal("空查询未拒绝")
	}
}

// TestParseSearchProbeResults 验证官方搜索数组、空结果、错误形状和秘密回显脱敏；参数t为上下文，纯解析无需清理。
// 不把HTTP200错误对象或缺少结果字段当成成功，也不把合法空数组当成连接失败。
func TestParseSearchProbeResults(t *testing.T) {
	for provider, raw := range map[string]string{"tavily": `{"results":[{"title":"fixture-secret","url":"https://example.com","content":"text fixture-secret"}]}`, "perplexity": `{"results":[{"title":"Result","url":"https://example.com","snippet":"text"}]}`, "serper": `{"organic":[{"title":"Result","link":"https://example.com","snippet":"text"}]}`} {
		results, err := parseSearchProbeResults(provider, []byte(raw), "fixture-secret")
		if err != nil || len(results) != 1 || strings.Contains(results[0].Title+results[0].Snippet, "fixture-secret") {
			t.Fatalf("解析或脱敏错误: %+v %v", results, err)
		}
	}
	results, err := parseSearchProbeResults("tavily", []byte(`{"results":[]}`), "")
	if err != nil || results == nil || len(results) != 0 {
		t.Fatalf("合法空结果应成功: %+v %v", results, err)
	}
	for _, raw := range []string{`{`, `null`, `[]`, `{}`, `{"results":null}`, `{"results":{}}`, `{"results":[1]}`, `{"results":[{}]}`, `{"results":[],"error":"no credit"}`, `{"results":[{"title":"R","url":"javascript:alert(1)","content":"text"}]}`, `{"results":[{"title":"R","url":"https://example.com","content":2}]}`} {
		if _, err := parseSearchProbeResults("tavily", []byte(raw), ""); err == nil {
			t.Errorf("错误形状未拒绝: %s", raw)
		}
	}
}

// TestRunSearchProbeCancellationAndBounds 验证出站继承取消/超时并限制响应体；参数t为上下文，真实本地HTTP夹具在结束时关闭。
// 前置无付费凭据，取消和超时不能成为成功，超大响应返回可观察协议错误。
func TestRunSearchProbeCancellationAndBounds(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, failure := runSearchProbe(ctx, nil, searchProbeConfig{Provider: "tavily", Endpoint: "http://127.0.0.1:1", Key: "fixture", Query: "Query"})
		if failure == nil || failure.Status != 408 || failure.Code != "request_cancelled" {
			t.Fatalf("取消请求分类错误: %+v", failure)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
		}))
		defer upstream.Close()
		_, _, failure := runSearchProbe(context.Background(), &http.Client{Timeout: 20 * time.Millisecond}, searchProbeConfig{Provider: "tavily", Endpoint: upstream.URL, Key: "fixture", Query: "Query"})
		if failure == nil || failure.Status != 504 || failure.Code != "request_timeout" {
			t.Fatalf("超时请求分类错误: %+v", failure)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", searchProbeResponseLimit+1))
		}))
		defer upstream.Close()
		_, _, failure := runSearchProbe(context.Background(), nil, searchProbeConfig{Provider: "tavily", Endpoint: upstream.URL, Key: "fixture", Query: "Query"})
		if failure == nil || failure.Code != "invalid_search_response" {
			t.Fatalf("超大响应未拒绝: %+v", failure)
		}
	})
	t.Run("refused", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		endpoint := upstream.URL
		upstream.Close()
		_, _, failure := runSearchProbe(context.Background(), nil, searchProbeConfig{Provider: "tavily", Endpoint: endpoint, Key: "fixture", Query: "Query"})
		if failure == nil || failure.Code != "request_failed" {
			t.Fatalf("连接拒绝分类错误: %+v", failure)
		}
	})
}

// TestRunSearchProbeRejectsRedirect 验证供应商重定向不会携带Key触达第二个服务；参数t为上下文，两个本地HTTP服务器自动关闭。
// 预期只接收302并返回502+upstream302，不请求跳转目标，不修改共享HTTP客户端。
func TestRunSearchProbeRejectsRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer upstream.Close()
	client := &http.Client{Timeout: time.Second}
	_, status, failure := runSearchProbe(context.Background(), client, searchProbeConfig{Provider: "tavily", Endpoint: upstream.URL, Key: "fixture", Query: "Query"})
	if failure == nil || failure.Status != 502 || status != 302 || redirected.Load() != 0 || client.CheckRedirect != nil {
		t.Fatalf("重定向泄漏或共享客户端被修改: status=%d failure=%+v redirected=%d", status, failure, redirected.Load())
	}
}
