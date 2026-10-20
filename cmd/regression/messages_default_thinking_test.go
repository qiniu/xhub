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

// TestMessagesBridgeDefaultThinkingToolRoundTrip 验证真实网关把无推理的 Chat/Responses 工具续轮送至 Messages 时明确关闭 thinking。
// 前置私有 PostgreSQL、有限额 Key 与本地 Messages 上游；检查完整历史、JSON/SSE 终态、两轮结算及错误的零外发，原生 Messages 缺省保持不变。
// harness 和 httptest 在结束时删除测试 schema、身份、Key 与 HTTP 服务，不调用付费供应商。
func TestMessagesBridgeDefaultThinkingToolRoundTrip(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, protocol := range []string{"responses", "chat", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				var calls atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					raw, _ := io.ReadAll(r.Body)
					var body map[string]any
					if err := json.Unmarshal(raw, &body); err != nil {
						t.Error(err)
					}
					thinking, _ := body["thinking"].(map[string]any)
					if protocol == "messages" {
						if _, exists := body["thinking"]; exists {
							t.Error("原生 Messages 被注入默认 thinking")
						}
					} else if thinking["type"] != "disabled" {
						t.Error("无推理的跨协议请求未关闭上游自动 thinking")
					}
					if body["max_tokens"] != float64(64) {
						t.Error("跨协议输出上限丢失")
					}
					if n == 1 {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"id":"msg_default_tool","type":"message","role":"assistant","content":[{"type":"tool_use","id":"call_default","name":"lookup","input":{"id":"fixture"}}],"stop_reason":"tool_use","usage":{"input_tokens":8,"output_tokens":2}}`)
						return
					}
					if n != 2 || !strings.Contains(string(raw), `"tool_use_id":"call_default"`) || !strings.Contains(string(raw), "SYNTHETIC_LOOKUP_RESULT") {
						t.Errorf("工具历史或关联 ID 丢失: calls=%d body=%s", n, raw)
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, streamTerminalFrames("messages"))
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"id":"msg_default_final","type":"message","role":"assistant","content":[{"type":"text","text":"TERMINAL_OK"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`)
					}
				}))
				t.Cleanup(up.Close)
				dep := hardBudgetDeployment("claude-default-thinking")
				dep.LiteLLMParams["api_base"] = up.URL
				dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
				dep.ModelInfo["transport"] = "bypass_anthropic_messages"
				dep.ModelInfo["endpoint_types"] = []string{"messages", "chat", "responses"}
				h := newHarness(t, dep)
				admin := h.adminSession()
				owner := h.openScope(t, admin, dep.ModelName)
				h.setKeyBudget(t, admin, owner.key, 1)
				body := map[string]any{"model": dep.ModelName, "stream": false}
				user := map[string]any{"role": "user", "content": "Call lookup with id fixture and then read the result."}
				schema := map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}
				path := "/v1/" + protocol
				switch protocol {
				case "responses":
					body["input"], body["max_output_tokens"] = []any{user}, 64
					body["tools"] = []any{map[string]any{"type": "function", "name": "lookup", "parameters": schema}}
				case "chat":
					path = "/v1/chat/completions"
					body["messages"], body["max_tokens"] = []any{user}, 64
					body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": schema}}}
				case "messages":
					body["messages"], body["max_tokens"] = []any{user}, 64
					body["tools"] = []any{map[string]any{"name": "lookup", "input_schema": schema}}
				}
				first := h.ok(http.MethodPost, path, owner.key, body)
				if !strings.Contains(first.text(), "call_default") {
					t.Fatalf("首轮工具调用丢失: %s", first.describe())
				}
				payload := first.json()
				switch protocol {
				case "responses":
					body["input"] = append(append([]any{user}, payload["output"].([]any)...), map[string]any{"type": "function_call_output", "call_id": "call_default", "output": "SYNTHETIC_LOOKUP_RESULT"})
				case "chat":
					message := payload["choices"].([]any)[0].(map[string]any)["message"]
					body["messages"] = []any{user, message, map[string]any{"role": "tool", "tool_call_id": "call_default", "content": "SYNTHETIC_LOOKUP_RESULT"}}
				case "messages":
					body["messages"] = []any{user, map[string]any{"role": "assistant", "content": payload["content"]}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_default", "content": "SYNTHETIC_LOOKUP_RESULT"}}}}
				}
				body["stream"] = stream
				final := h.ok(http.MethodPost, path, owner.key, body)
				if !strings.Contains(final.text(), "TERMINAL_OK") {
					t.Fatalf("工具续轮未完成: %s", final.describe())
				}
				if protocol != "messages" {
					for _, value := range []any{map[string]any{"effort": "low"}, map[string]any{"summary": false}, map[string]any{"context": "all_turns"}} {
						body["reasoning"] = value
						denied := h.do(http.MethodPost, path, owner.key, body)
						if denied.status != 400 || calls.Load() != 2 {
							t.Fatalf("不可表达推理未在外发前拒绝: %s calls=%d", denied.describe(), calls.Load())
						}
					}
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
				if err != nil || len(holds) != 0 {
					t.Fatalf("工具续轮或拒绝留下预占: %v %v", holds, err)
				}
				key, err := h.db.GetKey(t.Context(), owner.keyID)
				if err != nil || !nearlyEqual(key.Spend, 0.000064) {
					t.Fatalf("两轮实际用量未准确结算: %+v %v", key, err)
				}
			})
		}
	}
}
