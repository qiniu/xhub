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
	"time"

	"github.com/sunqirui1987/xhub/internal/gateway/billing"
)

// hardBudgetCacheUsage 构造缓存计量正常、同帧矛盾和累计起点；参数选择模式，返回独立对象，无外部副作用。
func hardBudgetCacheUsage(mode string) map[string]any {
	n := 2
	if mode == "growth" {
		n = 0
	}
	u := map[string]any{"cache_creation_input_tokens": n, "cache_write_tokens": n, "cached_tokens": n, "prompt_tokens_details": map[string]any{"cached_tokens": n}}
	if mode == "write_conflict" {
		u["cache_creation_input_tokens"], u["cache_write_tokens"] = 0, 50
	}
	if mode == "read_conflict" {
		u["cached_tokens"] = 0
		u["prompt_tokens_details"] = map[string]any{"cached_tokens": 8}
	}
	if mode == "separate" {
		u["cache_read_input_tokens"] = n
	}
	return u
}

// hardBudgetCacheReply 生成合法终态 JSON/SSE，并把缓存矛盾放在中间帧以验证后续正常值不能擦除证据。
// 参数为上游协议、场景与是否流式；返回离线正文，Messages 保持完整块生命周期，函数无外部副作用。
func hardBudgetCacheReply(protocol, mode string, stream bool) string {
	u := hardBudgetCacheUsage(mode)
	input, output := "input_tokens", "output_tokens"
	if protocol == "chat" {
		input, output = "prompt_tokens", "completion_tokens"
	}
	u[input], u[output] = 8, 2
	if !stream {
		var doc map[string]any
		switch protocol {
		case "chat":
			doc = map[string]any{"id": "chatcmpl_cache", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "TERMINAL_OK"}, "finish_reason": "stop"}}}
		case "responses":
			doc = map[string]any{"id": "resp_cache", "object": "response", "status": "completed", "output": []any{map[string]any{"id": "msg_cache", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "TERMINAL_OK"}}}}}
		default:
			doc = map[string]any{"id": "msg_cache", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "TERMINAL_OK"}}, "stop_reason": "end_turn"}
		}
		doc["usage"] = u
		raw, _ := json.Marshal(doc)
		return string(raw)
	}
	u[output] = 0
	initial, _ := json.Marshal(u)
	finalUsage := map[string]any{input: 8, output: 2, "cache_write_tokens": 2, "cached_tokens": 2}
	if mode == "separate" {
		finalUsage["cache_read_input_tokens"] = 2
	}
	final, _ := json.Marshal(finalUsage)
	raw := streamTerminalFrames(protocol)
	switch protocol {
	case "chat":
		raw = strings.Replace(raw, `{"prompt_tokens":8,"completion_tokens":2}`, string(final), 1)
		return `data: {"id":"chatcmpl_terminal","choices":[],"usage":` + string(initial) + "}\n\n" + raw
	case "responses":
		raw = strings.Replace(raw, `"delta":"TERMINAL_OK"`, `"delta":"TERMINAL_OK","usage":`+string(initial), 1)
		return strings.Replace(raw, `{"input_tokens":8,"output_tokens":2}`, string(final), 1)
	default:
		raw = strings.Replace(raw, `{"input_tokens":8,"output_tokens":0}`, string(initial), 1)
		return strings.Replace(raw, `{"output_tokens":2}`, string(final), 1)
	}
}

// TestHardBudgetCacheUsageConflict 验证真实后台三协议 JSON/SSE、原生旁路及 Chat 桥接都不会按矛盾缓存计量编造费用。
// 前置私有 PostgreSQL、无 Redis、本地明确缓存价格；正常和累计增长精确计费，矛盾仍返回完整输出但标记未定价，
// 下一调用仍可出站且没有遗留预占；harness 和 httptest 清理 schema、账号、Key、占用和服务，无付费依赖。
func TestHardBudgetCacheUsageConflict(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, tc := range []struct{ source, target, path string }{
		{"chat", "chat", "/v1/chat/completions"}, {"responses", "responses", "/v1/responses"}, {"messages", "messages", "/v1/messages"},
		{"chat", "chat", "/bypass/openai/v1/chat/completions"}, {"responses", "responses", "/bypass/openai/v1/responses"}, {"messages", "messages", "/bypass/anthropic/v1/messages"},
		{"chat", "responses", "/v1/responses"}, {"chat", "messages", "/v1/messages"},
	} {
		for _, stream := range []bool{false, true} {
			modes := []string{"equal", "write_conflict", "read_conflict"}
			if tc.source == "messages" {
				modes = append(modes, "separate")
			}
			if stream {
				modes = append(modes, "growth")
			}
			for _, mode := range modes {
				t.Run(fmt.Sprintf("%s%s/stream=%t/%s", tc.source, tc.path, stream, mode), func(t *testing.T) {
					var calls atomic.Int32
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						contentType := "application/json"
						if stream {
							contentType = "text/event-stream"
						}
						w.Header().Set("Content-Type", contentType)
						_, _ = io.WriteString(w, hardBudgetCacheReply(tc.source, mode, stream))
					}))
					t.Cleanup(up.Close)
					dep := hardBudgetDeployment("cache-usage")
					dep.LiteLLMParams["api_base"] = up.URL
					dep.LiteLLMParams["cache_read_input_token_cost"] = 0.5e-6
					dep.LiteLLMParams["cache_creation_input_token_cost"] = 3e-6
					protocol := map[string]string{"chat": "openai-chat", "responses": "openai-responses", "messages": "anthropic-messages"}[tc.source]
					dep.ModelInfo["transport"] = "bypass_" + strings.ReplaceAll(protocol, "-", "_")
					dep.ModelInfo["endpoint_types"] = []string{"chat", "responses", "messages", "bypass:" + protocol}
					h := newHarness(t, dep)
					admin := h.adminSession()
					owner := h.openScope(t, admin, "cache-usage")
					body := chatRequest(dep.ModelName, "Verify measured cache usage.")
					if tc.target == "responses" {
						delete(body, "messages")
						body["input"] = "Verify measured cache usage."
					}
					body["stream"] = stream
					planRaw := []byte(`{"messages":[{"role":"user","content":"Verify measured cache usage."}]}`)
					if tc.source == "responses" {
						planRaw = []byte(`{"input":"Verify measured cache usage."}`)
					}
					plan, _, err := billing.StrictBudgetPlan(dep, planRaw, protocol, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					h.setKeyBudget(t, admin, owner.key, plan.Amount)
					got := h.ok(http.MethodPost, tc.path, owner.key, body)
					if !strings.Contains(got.text(), "TERMINAL_OK") {
						t.Fatalf("缓存计量异常破坏正常输出: %s", got.describe())
					}
					holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
					if err != nil {
						t.Fatal(err)
					}
					key, err := h.db.GetKey(t.Context(), owner.keyID)
					if err != nil {
						t.Fatal(err)
					}
					if strings.HasSuffix(mode, "conflict") {
						if len(holds) != 0 || key.Spend != 0 {
							t.Fatalf("缓存矛盾被错误计价或预占: holds=%+v spend=%g", holds, key.Spend)
						}
						// 使用新正文避开响应缓存；验证被保留的余额不能支付下一次实际出站。
						nextBody := chatRequest(dep.ModelName, "Verify cache conflict retains funds for a new request.")
						nextBody["stream"] = stream
						if tc.target == "responses" {
							delete(nextBody, "messages")
							nextBody["input"] = "Verify cache conflict retains funds for a new request."
						}
						if next := h.do(http.MethodPost, tc.path, owner.key, nextBody); next.status != 200 {
							t.Fatalf("软预算不应因未知费用冻结下一调用: %s", next.describe())
						}
					} else {
						want := 6*2e-6 + 2*8e-6 + 2*0.5e-6 + 2*3e-6
						if mode == "separate" {
							want += 2 * 2e-6 // 原生Messages的8个输入不包含缓存2，必须保留其独立计量语义。
						}
						if len(holds) != 0 || !nearlyEqual(key.Spend, want) {
							t.Fatalf("合法缓存计量未精确结算: holds=%+v spend=%g want=%g", holds, key.Spend, want)
						}
					}
					wantCalls := int32(1)
					if strings.HasSuffix(mode, "conflict") {
						wantCalls = 2
					}
					if calls.Load() != wantCalls {
						t.Fatalf("缓存计量校验导致重复外发: calls=%d", calls.Load())
					}
				})
			}
		}
	}
}
