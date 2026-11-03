// 本文件验证 execution 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package execution

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/router"
	"github.com/sunqirui1987/xhub/internal/util"
)

// cacheScopeHost 在日志宿主上增加固定会话、可替换路由快照及缓存命中计数，验证缓存键隔离。
// 只在当前测试中使用，不保存真实会话或向外部缓存写数据。
type cacheScopeHost struct {
	// 复用日志宿主的身份、缓存及执行能力，下面覆写会话和策略。
	*logHost
	// 本例固定会话 ID，测试改变它以验证缓存隔离。
	session string
	// 实际进入 WriteCacheHit 的次数，不统计普通成功。
	hits int
	// 可选路由快照；nil 时沿用嵌入宿主。
	settings *router.RouteSettings
	// 本轮拒绝的实际模型，验证缓存命中也重新检查当前权限。
	denied string
	// 依序捕获本轮接受权限校验的组和模型，重复请求前由测试清空。
	checked []string
}

// RouteSettingsFor 是 cacheScopeHost 的测试接口实现。
// 参数 p 为身份；有 h.settings 时返回指定快照，否则委托 logHost；供缓存配置切换测试，无存储副作用。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *cacheScopeHost) RouteSettingsFor(p *auth.Principal, models []config.ModelEntry) router.RouteSettings {
	if h.settings != nil {
		return *h.settings
	}
	return h.logHost.RouteSettingsFor(p, models)
}

// PlanRoute 是 cacheScopeHost 的测试接口实现。
// 忽略请求与身份参数，返回仅包含 h.session 的计划；用于让测试直接控制缓存会话范围，不生成真实偏好。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *cacheScopeHost) PlanRoute(*http.Request, string, map[string]any, *auth.Principal) dpcontract.RoutePlan {
	return dpcontract.RoutePlan{SessionID: h.session}
}

// WriteCacheHit 是 cacheScopeHost 的测试接口实现。
// 忽略接口的输出及计费参数，将 hits 加一；无返回，仅断言确实走命中分支，不写响应或账单。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *cacheScopeHost) WriteCacheHit(http.ResponseWriter, *auth.Principal, string, string, string, string, []byte, time.Time) {
	h.hits++
}

// EnforceIdentityLimits 是 cacheScopeHost 的测试接口实现。
// 参数 w 用于输出拒绝状态，model 为待复核模型；忽略路径、身份和估算值。
// 依序记录 checked；model 等于 denied 时写 401 并返回 false，否则返回 true。
// 用于验证缓存命中也复核实际目标；不模拟真实预算或 RPM/TPM。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *cacheScopeHost) EnforceIdentityLimits(w http.ResponseWriter, _ string, _ *auth.Principal, model string, _ int) bool {
	h.checked = append(h.checked, model)
	if model == h.denied {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

// AdmitRequest 将本测试的请求准入委托给同一宿主的权限记录器；参数为响应、路径、身份、模型和估算量。
// 返回是否允许请求；缓存目标复核测试调用，避免嵌入宿主内部方法绕过本例覆写的模型检查，无外部副作用。
func (h *cacheScopeHost) AdmitRequest(w http.ResponseWriter, path string, p *auth.Principal, alias string, est int) bool {
	return h.EnforceIdentityLimits(w, path, p, alias, est)
}

// WriteChatJSON 是 cacheScopeHost 的测试接口实现。
// 参数 w 为测试响应，key 为缓存键，body/status 为执行结果，models 为缓存目标授权范围；其余记账参数忽略。
// 仅成功状态将正文及模型范围写入夹具私有缓存，然后向 w 输出原状态和正文；无返回，不写真实账单。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *cacheScopeHost) WriteChatJSON(w http.ResponseWriter, _ *auth.Principal, _, _, key, _, _ string, body []byte, status int, _ time.Time, _ string, models ...string) {
	if status >= 200 && status < 300 {
		h.cache.Set(key, body, models...)
	}
	w.WriteHeader(status)
	w.Write(body)
}

// TestCacheHitRechecksActualTarget 验证首次组成员执行写缓存、重复命中及权限撤销后拒绝。
// 前置私有内存上游和路由组，验证实际模型每次复核且缓存不外发；无外部资源需清理。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestCacheHitRechecksActualTarget(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))}, nil
	})}
	cfg := chatConfig(config.ModelEntry{ModelName: "member", LiteLLMParams: map[string]any{"model": "openai/member", "api_base": "https://example.invalid/v1", "api_key": "test-key"}})
	h := &cacheScopeHost{logHost: newLogHost(cfg, client), session: "group-session"}
	settings := h.RouteSettingsFor(nil, h.Models())
	settings.RoutingGroups = []router.Group{{Name: "group", Models: []string{"member"}, Strategy: "traffic-split"}}
	h.settings = &settings
	for round := 0; round < 3; round++ {
		h.checked = nil
		if round == 2 {
			h.denied = "member"
		}
		rec := httptest.NewRecorder()
		Serve(h, rec, chatRequest(t, "group", false), "chat")
		want := http.StatusOK
		if round == 2 {
			want = http.StatusUnauthorized
		}
		if rec.Code != want || attempts != 1 || h.hits != min(round, 1) || strings.Join(h.checked, ",") != "group,member" {
			t.Fatalf("第%d轮组缓存/目标复核失败: status=%d attempts=%d hits=%d checks=%v", round+1, rec.Code, attempts, h.hits, h.checked)
		}
	}
}

// TestCacheScopeChangesWithConfigurationSessionAndQuery 验证缓存配置隔离；前置内存传输和预存响应，
// 检查回退配置及无关路由组不禁用缓存，配置/会话/查询变化拒绝旧缓存，无外部资源需清理。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestCacheScopeChangesWithConfigurationSessionAndQuery(t *testing.T) {
	for _, change := range []string{"unchanged", "empty-fallback", "configured-fallback", "unrelated-group", "disabled", "price", "session", "query", "default-weights"} {
		t.Run(change, func(t *testing.T) {
			attempts := 0
			client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))}, nil
			})}
			cfg := chatConfig(config.ModelEntry{ModelName: "audit", LiteLLMParams: map[string]any{"model": "openai/audit", "api_base": "https://example.invalid/v1", "api_key": "test-key"}, ModelInfo: map[string]any{}})
			h := &cacheScopeHost{logHost: newLogHost(cfg, client), session: "session-a"}
			settings := h.RouteSettingsFor(nil, h.Models())
			switch change {
			case "empty-fallback":
				settings.ModelFallbacks = map[string]router.FallbackPolicy{"audit": {Fallbacks: []string{}, ContextWindow: []string{}, ContentPolicy: []string{}}}
			case "configured-fallback":
				settings.ModelFallbacks = map[string]router.FallbackPolicy{"audit": {Fallbacks: []string{"backup"}}}
			case "unrelated-group":
				settings.RoutingGroups = []router.Group{{Name: "other"}}
			}
			h.settings = &settings
			request := chatRequest(t, "audit", false)
			raw, _ := io.ReadAll(request.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			raw, _ = json.Marshal(body)
			scope, _ := json.Marshal(map[string]any{"models": cfg.ModelList, "router": h.RouteSettingsFor(nil, h.Models()).Settings, "model_defaults": h.RouteSettingsFor(nil, h.Models()).ModelDefaults, "session": h.session, "query": "", "routing_groups": h.RouteSettingsFor(nil, h.Models()).RoutingGroups, "fallbacks": h.RouteSettingsFor(nil, h.Models()).ModelFallbacks})
			h.cache.Set(util.CacheKey("user:log-user", "chat", "audit", string(raw), string(scope)), []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
			request.Body = io.NopCloser(strings.NewReader(string(raw)))
			switch change {
			case "disabled":
				cfg.ModelList[0].ModelInfo["disabled"] = true
			case "price":
				cfg.ModelList[0].ModelInfo["input_cost_per_token"] = 0.25
			case "default-weights":
				settings.ModelDefaults = map[string]any{"audit": map[string]any{"allocations": []any{}}}
			case "session":
				h.session = "session-b"
			case "query":
				request.URL.RawQuery = "version=2"
			}
			rec := httptest.NewRecorder()
			Serve(h, rec, request, "chat")
			if change == "unchanged" || change == "empty-fallback" || change == "configured-fallback" || change == "unrelated-group" {
				if h.hits != 1 || attempts != 0 {
					t.Fatalf("hits=%d attempts=%d", h.hits, attempts)
				}
			} else if h.hits != 0 {
				t.Fatalf("stale cache hit after %s", change)
			} else if change == "disabled" {
				if attempts != 0 || rec.Code != 400 {
					t.Fatalf("status=%d attempts=%d", rec.Code, attempts)
				}
			} else if attempts != 1 {
				t.Fatalf("attempts=%d", attempts)
			}
		})
	}
}
