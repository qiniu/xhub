package regression

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestClaudeKeepAllBudgetAndStop 验证真实后台有限额 Key 的 Claude Code 无操作字段及 Chat 流停止原因。
// 前置私有 PostgreSQL 和内存 Messages 上游；验证 JSON/SSE 结果、保留配置、精确费用与占用释放；
// 未知清理在外发前 400，harness 和 httptest 自动回收 schema、身份、Key 与 HTTP 服务。
func TestClaudeKeepAllBudgetAndStop(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, protocol := range []string{"messages", "chat"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				var calls atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if protocol == "messages" && body["context_management"] == nil {
						t.Error("原生 Messages 丢失 keep-all 配置")
					}
					if body["max_tokens"] != float64(64) {
						t.Error("真实出站输出上限丢失")
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, streamTerminalFrames("messages"))
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"id":"msg_compat","type":"message","role":"assistant","content":[{"type":"text","text":"TERMINAL_OK"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`)
					}
				}))
				t.Cleanup(up.Close)
				dep := hardBudgetDeployment("claude-client-compat")
				dep.LiteLLMParams["api_base"] = up.URL
				dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
				dep.ModelInfo["transport"] = "bypass_anthropic_messages"
				dep.ModelInfo["endpoint_types"] = []string{"messages", "chat", "responses"}
				h := newHarness(t, dep)
				admin := h.adminSession()
				owner := h.openScope(t, admin, dep.ModelName)
				h.setKeyBudget(t, admin, owner.key, 1)
				body := chatRequest(dep.ModelName, "Reply exactly TERMINAL_OK.")
				body["max_tokens"], body["stream"] = 64, stream
				path := "/v1/chat/completions"
				if protocol == "messages" {
					path = "/v1/messages"
					body["context_management"] = map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}}
				}
				got := h.ok(http.MethodPost, path, owner.key, body)
				if !strings.Contains(got.text(), "TERMINAL_OK") {
					t.Fatalf("兼容请求缺少真实文本: %s", got.describe())
				}
				if protocol == "chat" && !strings.Contains(got.text(), `"finish_reason":"stop"`) {
					t.Fatalf("Chat 泄漏供应商停止原因: %s", got.describe())
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
				if err != nil || len(holds) != 0 {
					t.Fatalf("正常结算未释放预占: %v %v", holds, err)
				}
				key, err := h.db.GetKey(t.Context(), owner.keyID)
				if err != nil || !nearlyEqual(key.Spend, 0.000032) {
					t.Fatalf("兼容请求计费错误: %+v %v", key, err)
				}
				if protocol == "messages" {
					body["context_management"] = map[string]any{"edits": []any{map[string]any{"type": "compact_20260112"}}}
					denied := h.do(http.MethodPost, path, owner.key, body)
					if denied.status != 200 || calls.Load() != 2 {
						t.Fatalf("原生清理不应被预算层拒绝: %s calls=%d", denied.describe(), calls.Load())
					}
				}
			})
		}
	}
}
