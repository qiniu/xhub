// 本文件验证 execution 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	dprouting "github.com/sunqirui1987/xhub/internal/dataplane/routing"
	providerregistry "github.com/sunqirui1987/xhub/internal/provider/registry"
	"github.com/sunqirui1987/xhub/internal/router"
)

// fallbackNativeHost 为原生回退测试提供指定请求级策略，其余行为复用 logicHost；不解析真实身份链模板。
type fallbackNativeHost struct {
	// 复用原生内存宿主，仅覆写请求策略。
	*logicHost
	// 本例生效的完整回退/重试配置。
	policy router.RouteSettings
}

// RouteSettingsFor 是 fallbackNativeHost 的测试接口实现。
// 忽略身份，返回 h.policy；供 ServeBypass 测试请求级回退快照，不访问模板库。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *fallbackNativeHost) RouteSettingsFor(*auth.Principal) router.RouteSettings { return h.policy }

// TestNativeDedicatedFallbackChain 验证仅配置专用列表时，第一目标5xx后继续剩余目标或目标自身的通用链。
// 参数 t 为测试上下文，无返回；本地HTTP模拟上下文和内容错误，验证普通4xx终止和异步禁用，Cleanup关闭服务。
func TestNativeDedicatedFallbackChain(t *testing.T) {
	for _, tc := range []struct {
		name, code         string
		child, background  bool
		middleStatus, want int
		models             string
	}{
		{"context-pending", "context_length_exceeded", false, false, 500, 200, "a,b,c"},
		{"content-child", "content_policy_violation", true, false, 500, 200, "a,b,c"},
		{"ordinary-terminal", "context_length_exceeded", false, false, 400, 400, "a,b"},
		{"background-disabled", "context_length_exceeded", false, true, 500, 400, "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var doc map[string]any
				json.NewDecoder(r.Body).Decode(&doc)
				model, _ := doc["model"].(string)
				calls = append(calls, model)
				w.Header().Set("Content-Type", "application/json")
				switch model {
				case "a":
					w.WriteHeader(400)
					fmt.Fprintf(w, "{\"error\":{\"code\":%q}}", tc.code)
				case "b":
					w.WriteHeader(tc.middleStatus)
					io.WriteString(w, "{\"error\":{\"code\":\"api_error\"}}")
				default:
					io.WriteString(w, "{\"id\":\"ok\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}")
				}
			}))
			t.Cleanup(up.Close)
			settings := router.BuiltinSettings()
			targets := []string{"b", "c"}
			if tc.child {
				targets = targets[:1]
			}
			p := router.FallbackPolicy{ContextWindow: targets}
			if tc.code == "content_policy_violation" {
				p = router.FallbackPolicy{ContentPolicy: targets}
			}
			settings.ModelFallbacks = map[string]router.FallbackPolicy{"a": p, "b": {Fallbacks: []string{"c"}}}
			h := &fallbackNativeHost{logicHost: officialHost(up, Deployment("a", "a", "key", up.URL, "bypass_openai_responses", nil), Deployment("b", "b", "key", up.URL, "bypass_openai_responses", nil), Deployment("c", "c", "key", up.URL, "bypass_openai_responses", nil)), policy: settings}
			req := httptest.NewRequest("POST", "/bypass/openai/v1/responses", strings.NewReader(fmt.Sprintf("{\"model\":\"a\",\"input\":\"hi\",\"background\":%t}", tc.background)))
			hit, ok := providerregistry.Match("POST", req.URL.Path)
			if !ok {
				t.Fatal("原生入口未匹配")
			}
			w := httptest.NewRecorder()
			ServeBypass(h, w, req, hit)
			if w.Code != tc.want || strings.Join(calls, ",") != tc.models {
				t.Fatalf("状态%d 调用%v 正文%s", w.Code, calls, w.Body.String())
			}
		})
	}
}

// fallbackServeHost 为统一执行回退测试覆写策略、模型授权、父响应约束及响应写入；不进行真实计费。
type fallbackServeHost struct {
	// 复用统一日志宿主，其余字段控制目标拒绝和固定归属。
	*logHost
	// 本例统一请求策略。
	settings router.RouteSettings
	// 被模拟权限拒绝的模型名，空值不拒绝正常模型。
	deny string
	// 是否启用首部署父响应约束，用于禁止普通回退。
	required bool
}

// RouteSettingsFor 是 fallbackServeHost 的测试接口实现。
// 忽略身份，返回 h.settings；供 Serve 测试回退配置，不查询数据库。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *fallbackServeHost) RouteSettingsFor(*auth.Principal) router.RouteSettings { return h.settings }

// WriteChatJSON 是 fallbackServeHost 的测试接口实现。
// 把参数 status/body 写入 w，忽略身份、缓存、计费参数及可变模型授权范围；无返回，供 HTTP 结果断言，不真实结算。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *fallbackServeHost) WriteChatJSON(w http.ResponseWriter, _ *auth.Principal, _, _, _, _, _ string, body []byte, status int, _ time.Time, _ string, _ ...string) {
	w.WriteHeader(status)
	w.Write(body)
}

// EnforceIdentityLimits 是 fallbackServeHost 的测试接口实现。
// 当参数 model 等于 h.deny 时向 w 写 403 并返回 false，否则返回 true；其他参数不参与判断，模拟目标权限拒绝。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *fallbackServeHost) EnforceIdentityLimits(w http.ResponseWriter, _ string, _ *auth.Principal, model string, _ int) bool {
	if model == h.deny {
		w.WriteHeader(403)
		return false
	}
	return true
}

// PlanRoute 是 fallbackServeHost 的测试接口实现。
// 忽略入站参数，未启用 required 返回空计划；启用时返回首部署稳定 ID 与 Required，验证父响应禁止回退。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *fallbackServeHost) PlanRoute(*http.Request, string, map[string]any, *auth.Principal) dpcontract.RoutePlan {
	if h.required {
		plan := dpcontract.RoutePlan{Pinned: router.DeploymentID(h.cfg.ModelList[0]), Target: dpcontract.RouteTarget{DeploymentID: router.DeploymentID(h.cfg.ModelList[0]), Model: "a", Pool: "a"}}
		dprouting.BindConnection(&plan, h.cfg.ModelList[0])
		plan.Required = true
		return plan
	}
	return dpcontract.RoutePlan{}
}

// TestServeModelFallback 验证实际HTTP执行顺序、禁用开关、继续请求和权限拒绝。
// 前置本地上游和内存路由，所有错误与成功均走真实HTTP，服务由Cleanup关闭。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestServeModelFallback(t *testing.T) {
	for _, tc := range []struct {
		name, denied      string
		disable, required bool
		want              int
		calls             int
	}{{"enabled", "", false, false, 200, 2}, {"disabled", "", true, false, 500, 1}, {"continuation", "", false, true, 500, 1}, {"denied", "b", false, false, 403, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				model, _ := b["model"].(string)
				calls = append(calls, model)
				if b["disable_fallbacks"] != nil {
					t.Error("开关发送给了上游")
				}
				if model == "a" {
					w.WriteHeader(500)
					io.WriteString(w, "{}")
					return
				}
				io.WriteString(w, "{\"id\":\"ok\",\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}")
			}))
			t.Cleanup(up.Close)
			cfg := chatConfig(config.ModelEntry{ModelName: "a", LiteLLMParams: map[string]any{"model": "a", "api_base": up.URL, "api_key": "sk-local", "deployment_id": "a"}}, config.ModelEntry{ModelName: "b", LiteLLMParams: map[string]any{"model": "b", "api_base": up.URL, "api_key": "sk-local", "deployment_id": "b"}})
			settings := router.BuiltinSettings()
			settings.ModelFallbacks = map[string]router.FallbackPolicy{"a": {Fallbacks: []string{"b"}}}
			h := &fallbackServeHost{logHost: newLogHost(cfg, up.Client()), settings: settings, deny: tc.denied, required: tc.required}
			req := chatRequest(t, "a", false)
			if tc.disable {
				req.Body = io.NopCloser(strings.NewReader("{\"model\":\"a\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"disable_fallbacks\":true}"))
			}
			w := httptest.NewRecorder()
			Serve(h, w, req, "chat")
			if w.Code != tc.want || len(calls) != tc.calls {
				t.Fatalf("%s: 状态%d 调用%v 正文%s", tc.name, w.Code, calls, w.Body.String())
			}
		})
	}
}

// TestFallbackStreamAlreadyWritten 验证配置回退后流式中断仍不切换；前置分段错误流，断言只发送一次。
// 使用内存RoundTripper，响应和缓存随测试结束释放，不依赖外部供应商。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestFallbackStreamAlreadyWritten(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(&FragmentedErrorReader{Parts: [][]byte{[]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")}, Err: errors.New("reset")})}, nil
	})}
	cfg := chatConfig(config.ModelEntry{ModelName: "a", LiteLLMParams: map[string]any{"model": "a", "api_base": "https://example.invalid", "api_key": "local"}}, config.ModelEntry{ModelName: "b", LiteLLMParams: map[string]any{"model": "b", "api_base": "https://example.invalid", "api_key": "local"}})
	settings := router.BuiltinSettings()
	settings.ModelFallbacks = map[string]router.FallbackPolicy{"a": {Fallbacks: []string{"b"}}}
	h := &fallbackServeHost{logHost: newLogHost(cfg, client), settings: settings}
	w := httptest.NewRecorder()
	Serve(h, w, chatRequest(t, "a", true), "chat")
	if attempts != 1 || !strings.Contains(w.Body.String(), "partial") {
		t.Fatalf("输出后重试: 次数%d 正文%s", attempts, w.Body.String())
	}
}

// TestNativeGroupMemberFallback 验证组名原生调用使用实际成员的回退链；参数t为测试上下文，无返回。
// 前置本地上游及单成员组；覆盖成功回退、显式禁用、异步禁用和目标错误，Cleanup关闭HTTP服务。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestNativeGroupMemberFallback(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		disabled, background bool
		targetStatus, want   int
		calls                string
	}{
		{"member-chain", false, false, 200, 200, "a,b"},
		{"disabled", true, false, 200, 500, "a"},
		{"background", false, true, 200, 500, "a"},
		{"target-error", false, false, 400, 400, "a,b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				model, _ := body["model"].(string)
				calls = append(calls, model)
				w.Header().Set("Content-Type", "application/json")
				status := tc.targetStatus
				if model == "a" {
					status = 500
				}
				w.WriteHeader(status)
				if status >= 400 {
					io.WriteString(w, "{\"error\":{\"code\":\"api_error\"}}")
				} else {
					io.WriteString(w, "{\"id\":\"ok\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}")
				}
			}))
			t.Cleanup(up.Close)
			settings := router.BuiltinSettings()
			settings.RoutingGroups = []router.Group{{Name: "group", Models: []string{"a"}, Strategy: "simple-shuffle"}}
			settings.ModelFallbacks = map[string]router.FallbackPolicy{"a": {Fallbacks: []string{"b"}}}
			h := &fallbackNativeHost{logicHost: officialHost(up,
				Deployment("a", "a", "key", up.URL, "bypass_openai_responses", nil),
				Deployment("b", "b", "key", up.URL, "bypass_openai_responses", nil)), policy: settings}
			req := httptest.NewRequest("POST", "/bypass/openai/v1/responses", strings.NewReader(fmt.Sprintf("{\"model\":\"group\",\"input\":\"hi\",\"background\":%t,\"disable_fallbacks\":%t}", tc.background, tc.disabled)))
			hit, ok := providerregistry.Match("POST", req.URL.Path)
			if !ok {
				t.Fatal("原生入口未匹配")
			}
			w := httptest.NewRecorder()
			ServeBypass(h, w, req, hit)
			if w.Code != tc.want || strings.Join(calls, ",") != tc.calls {
				t.Fatalf("组成员回退: 状态%d 调用%v 正文%s", w.Code, calls, w.Body.String())
			}
		})
	}
}

// TestNativeRetriesPreserveSupplier 验证同步原生对话不依赖跨模型回退配置即可在同一部署重试。
// 前置本地受控上游和 max_attempts=3；覆盖短暂失败恢复、持续失败耗尽、普通参数错误和异步不重放。
// 断言真实上游次数和 HTTP 结果；每个子测试由 Cleanup 关闭服务，无持久化数据。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestNativeRetriesPreserveSupplier(t *testing.T) {
	for _, tc := range []struct {
		name                          string // 场景名称，区分恢复、耗尽和不允许重放的边界。
		status                        int    // 首次或持续失败的上游状态码。
		recover, disabled, background bool   // 是否在重试后恢复、禁用跨模型回退或使用异步创建。
		wantStatus, wantCalls         int    // 网关最终状态与同一部署应收到的总调用次数。
	}{
		{"recover-without-fallback", 500, true, false, false, 200, 2},
		{"recover-with-fallback-disabled", 500, true, true, false, 200, 2},
		{"exhaust-same-deployment", 503, false, false, false, 503, 3},
		{"rate-limit-recovery", 429, true, false, false, 200, 2},
		{"ordinary-parameter-error", 400, false, false, false, 400, 1},
		{"background-not-replayed", 500, true, false, true, 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != "supplier-a" {
					t.Errorf("重试更换了上游型号: %v", body["model"])
				}
				w.Header().Set("Content-Type", "application/json")
				if !tc.recover || calls == 1 {
					w.WriteHeader(tc.status)
					io.WriteString(w, "{\"error\":{\"code\":\"api_error\"}}")
					return
				}
				io.WriteString(w, "{\"id\":\"ok\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}")
			}))
			t.Cleanup(up.Close)
			settings := router.BuiltinSettings()
			settings.Settings["retry_policy"].(map[string]any)["max_attempts"] = 3
			// 失败阈值为 1，当前请求仍应完成预算，不能因第一次失败触发冷却而提前放弃供应商。
			settings.Settings["retry_policy"].(map[string]any)["failure_threshold"] = 1
			h := &fallbackNativeHost{logicHost: officialHost(up, Deployment("public-a", "supplier-a", "key", up.URL, "bypass_openai_responses", nil)), policy: settings}
			req := httptest.NewRequest("POST", "/bypass/openai/v1/responses", strings.NewReader(fmt.Sprintf("{\"model\":\"public-a\",\"input\":\"Explain caching\",\"disable_fallbacks\":%t,\"background\":%t}", tc.disabled, tc.background)))
			hit, ok := providerregistry.Match("POST", req.URL.Path)
			if !ok {
				t.Fatal("原生入口未注册")
			}
			w := httptest.NewRecorder()
			ServeBypass(h, w, req, hit)
			if w.Code != tc.wantStatus || calls != tc.wantCalls {
				t.Fatalf("同供应商重试: 状态=%d 次数=%d，期望=%d/%d，正文=%s", w.Code, calls, tc.wantStatus, tc.wantCalls, w.Body.String())
			}
		})
	}
}
