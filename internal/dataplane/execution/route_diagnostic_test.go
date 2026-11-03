// 本文件验证 execution 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package execution

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/router"
)

// diagnosticHost 为执行入口提供指定路由配置与运行快照，验证接口错误及进程日志能解释候选消失原因。
type diagnosticHost struct {
	// 复用执行日志捕获能力。
	*logHost
	// 本例指定的请求路由策略。
	settings router.RouteSettings
	// 本例指定的冷却/分配运行快照。
	state router.State
}

// RouteSettingsFor 返回测试的请求规则快照；参数身份由 Serve 提供，返回设置，不写数据。
func (h *diagnosticHost) RouteSettingsFor(*auth.Principal, []config.ModelEntry) router.RouteSettings {
	return h.settings
}

// RouteState 返回测试冷却状态；Serve 调用，无参数，返回只读快照，无外部副作用。
func (h *diagnosticHost) RouteState() router.State { return h.state }

// TestServeRouteDiagnosisLogs 验证用户复现的零权重/过期 ID 场景，接口返回503并关联安全日志。
// 前置独立配置和内存日志；同时覆盖不兼容请求/无效协议失败，恢复日志输出，无真实凭据及上游调用。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestServeRouteDiagnosisLogs(t *testing.T) {
	for _, mode := range []string{"zero", "invalid", "capability", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			dep := diagnosticDeployment("gpt-5.6-sol", "live", "bypass_openai_chat", false)
			s := router.BuiltinSettings()
			wantStatus, wantReason := 503, "deployment has no allocated traffic"
			if mode == "zero" {
				s.ModelDefaults = map[string]any{dep.ModelName: map[string]any{"allocations": []any{map[string]any{"deployment_id": "live", "weight": 0}, map[string]any{"deployment_id": "deleted", "weight": 100}}}}
			} else if mode == "invalid" {
				dep.ModelInfo["transport"] = "missing-transport"
				wantStatus, wantReason = 400, "model_info.transport must select a registered transport"
			} else if mode == "capability" {
				dep.ModelInfo["transport"] = "bypass_anthropic_messages"
				wantStatus, wantReason = 400, "strict"
			} else {
				s.ModelFallbacks = map[string]router.FallbackPolicy{dep.ModelName: {Fallbacks: []string{"fallback"}}}
				dep.ModelName = "fallback"
				dep.ModelInfo["transport"] = "missing-transport"
				wantStatus, wantReason = 400, "fallback_visited=1"
			}
			h := &diagnosticHost{logHost: newLogHost(&config.Config{ModelList: []config.ModelEntry{dep}}, nil), settings: s}
			buf := captureLog(t)
			req := chatRequest(t, "gpt-5.6-sol", false)
			if mode == "capability" {
				req = httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","strict":true,"parameters":{"type":"object"}}}]}`))
			}
			w := httptest.NewRecorder()
			Serve(h, w, req, "chat")
			if w.Code != wantStatus {
				t.Fatalf("%s 分类错误: %d %s", mode, w.Code, w.Body.String())
			}
			for _, fragment := range []string{"step=route_diagnosis", "call_id=\"" + w.Header().Get("x-litellm-call-id") + "\"", wantReason, "source=\"builtin\""} {
				if !strings.Contains(buf.String(), fragment) {
					t.Fatalf("缺失路由解释 %q: %s", fragment, buf.String())
				}
			}
			for _, secret := range []string{"arbitrary-private-secret", "private-password", "private-query", "https://"} {
				if strings.Contains(buf.String(), secret) {
					t.Fatalf("诊断泄漏敏感字段 %q", secret)
				}
			}
		})
	}
}

// diagnosticDeployment 构造执行链的诊断输入；参数为名称、部署、协议和暂停状态，返回内存配置，无外部数据。
// 供本包测试调用，不创建外部数据；资源由所属测试管理。
func diagnosticDeployment(name, id, transport string, disabled bool) config.ModelEntry {
	return config.ModelEntry{ModelName: name, LiteLLMParams: map[string]any{
		"model": "real", "api_key": "arbitrary-private-secret", "api_base": "https://user:private-password@upstream.example/v1?token=private-query",
		"custom_llm_provider": "custom",
	}, ModelInfo: map[string]any{"id": id, "transport": transport, "disabled": disabled}}
}
