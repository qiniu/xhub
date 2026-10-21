package regression

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/gateway/billing"
)

// hardBudgetUsageFrames 生成完整协议流，参数 mode 选择正常、矛盾或合法累计更新；返回离线 SSE，无副作用。
// Chat 在普通帧报告矛盾，终态报告正常值，专门验证不能被后续帧清除；其余协议在原生 usage 位置报告矛盾。
func hardBudgetUsageFrames(protocol, mode string) string {
	raw := streamTerminalFrames(protocol)
	if mode == "growth" {
		switch protocol {
		case "chat":
			return "data: {\"id\":\"chatcmpl_terminal\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"input_tokens\":8,\"completion_tokens\":0,\"output_tokens\":0}}\n\n" + raw
		case "messages":
			return strings.Replace(raw, `"output_tokens":0`, `"output_tokens":0,"completion_tokens":0`, 1)
		case "responses":
			// 用量可随普通事件累计；单帧双别名相等，成功终态只报告原生名称。
			return strings.Replace(raw, `"delta":"TERMINAL_OK"`, `"delta":"TERMINAL_OK","usage":{"input_tokens":8,"prompt_tokens":8,"output_tokens":0,"completion_tokens":0}`, 1)
		}
	}
	if mode != "conflict" {
		return raw
	}
	if protocol == "chat" {
		return "data: {\"id\":\"chatcmpl_terminal\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"input_tokens\":9,\"completion_tokens\":0}}\n\n" + raw
	}
	return strings.Replace(raw, `"input_tokens":8`, `"input_tokens":8,"prompt_tokens":9`, 1)
}

// TestHardBudgetStreamUsageConflict 验证标准三协议、原生旁路及跨协议在真实 HTTP 结算中一致阻止矛盾用量自动退款。
// 前置私有 PostgreSQL、明确价格/上下文、无 Redis 和本地 SSE 上游；正常/合法增长流按实际用量落账，矛盾流不编造费用且不冻结后续调用。
// 客户端仍收到完整回复，不因计量不可信诱发重试；harness 关闭服务并清理所有账号、Key、预占和 schema。
func TestHardBudgetStreamUsageConflict(t *testing.T) {
	for _, tc := range []struct{ source, target, path string }{
		{"chat", "chat", "/v1/chat/completions"}, {"responses", "responses", "/v1/responses"}, {"messages", "messages", "/v1/messages"},
		{"chat", "chat", "/bypass/openai/v1/chat/completions"}, {"responses", "responses", "/bypass/openai/v1/responses"}, {"messages", "messages", "/bypass/anthropic/v1/messages"},
		{"chat", "responses", "/v1/responses"}, {"chat", "messages", "/v1/messages"},
	} {
		for _, mode := range []string{"normal", "conflict", "growth"} {
			t.Run(fmt.Sprintf("%s%s/%s", tc.source, tc.path, mode), func(t *testing.T) {
				t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, hardBudgetUsageFrames(tc.source, mode))
				}))
				t.Cleanup(up.Close)
				dep := chatDeployment("usage-conflict")
				dep.LiteLLMParams["api_base"] = up.URL
				dep.ModelInfo["max_input_tokens"] = 1024
				dep.ModelInfo["max_output_tokens"] = 8192
				dep.ModelInfo["supported_parameters"] = []string{"max_tokens"}
				protocol := map[string]string{"chat": "openai-chat", "responses": "openai-responses", "messages": "anthropic-messages"}[tc.source]
				dep.ModelInfo["transport"] = "bypass_" + strings.ReplaceAll(protocol, "-", "_")
				dep.ModelInfo["endpoint_types"] = []string{"chat", "responses", "messages", "bypass:" + protocol}
				h := newHarness(t, dep)
				admin := h.adminSession()
				owner := h.openScope(t, admin, "usage-conflict")
				planBody := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Verify complete measured usage."}}}
				if tc.source == "responses" {
					planBody = map[string]any{"input": "Verify complete measured usage."}
				}
				raw, _ := json.Marshal(planBody)
				plan, _, err := billing.StrictBudgetPlan(dep, raw, protocol, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				h.setKeyBudget(t, admin, owner.key, plan.Amount)
				body := chatRequest(dep.ModelName, "Verify complete measured usage.")
				body["stream"] = true
				if tc.target == "responses" {
					delete(body, "messages")
					body["input"] = "Verify complete measured usage."
				}
				got := h.ok(http.MethodPost, tc.path, owner.key, body)
				if !strings.Contains(got.text(), "TERMINAL_OK") {
					t.Fatalf("计量问题破坏了正常回复: %s", got.describe())
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
				if err != nil {
					t.Fatal(err)
				}
				key, err := h.db.GetKey(t.Context(), owner.keyID)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "conflict" {
					if len(holds) != 0 || key.Spend != 0 {
						t.Fatalf("矛盾用量被自动结算退款: holds=%+v spend=%g", holds, key.Spend)
					}
					if next := h.do(http.MethodPost, tc.path, owner.key, body); next.status != 200 {
						t.Fatalf("未知费用不应阻断下一调用: %s", next.describe())
					}
				} else if len(holds) != 0 || !nearlyEqual(key.Spend, 8*2e-6+2*8e-6) {
					t.Fatalf("正常实测未结算: holds=%+v spend=%g", holds, key.Spend)
				}
			})
		}
	}
}
