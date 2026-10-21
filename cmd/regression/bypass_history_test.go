package regression

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestBypassHistoryAfterRename 验证原生Claude、GPT Responses和Chat经过真实鉴权后，完整历史在改名后仍可透传。
// 参数t为上下文；隔离数据库、专用Key、本地HTTP上游验证密文原样保留，非法Key和未知父响应不上游；harness回收资源。
func TestBypassHistoryAfterRename(t *testing.T) {
	for _, protocol := range []string{"messages", "responses", "chat"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			h, owner, admin, id := historyHarness(t, "messages", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				calls.Add(1)
				if !strings.Contains(string(mustJSON(body)), "vendor-history") {
					t.Error("透传历史丢失")
				}
				switch protocol {
				case "messages":
					historyMessagesReply(w, []any{map[string]any{"type": "text", "text": "bypass-ok"}}, false)
				case "responses":
					writeJSON(w, map[string]any{"id": "resp_bypass", "object": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "bypass-ok"}}}}, "usage": map[string]any{"input_tokens": 8, "output_tokens": 2, "total_tokens": 10}})
				default:
					writeJSON(w, map[string]any{"id": "chat_bypass", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "bypass-ok"}, "finish_reason": "stop"}}, "usage": openAIUsage(nil)})
				}
			}))
			endpoint := map[string]string{"messages": "anthropic-messages", "responses": "openai-responses", "chat": "openai-chat"}[protocol]
			transport := map[string]string{"messages": "bypass_anthropic_messages", "responses": "bypass_openai_responses", "chat": "bypass_openai_chat"}[protocol]
			path := map[string]string{"messages": "/bypass/anthropic/v1/messages", "responses": "/bypass/openai/v1/responses", "chat": "/bypass/openai/v1/chat/completions"}[protocol]
			h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": transport, "endpoint_types": []string{"bypass:" + endpoint}}})
			body := map[string]any{"model": "history-model", "max_tokens": 64}
			if protocol == "responses" {
				delete(body, "max_tokens")
				body["max_output_tokens"] = 64
				body["input"] = []any{map[string]any{"type": "reasoning", "encrypted_content": "vendor-history"}, map[string]any{"role": "user", "content": "Continue."}}
			} else if protocol == "messages" {
				body["messages"] = []any{map[string]any{"role": "user", "content": "Start."}, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "Plan", "signature": "vendor-history"}, map[string]any{"type": "text", "text": "Ready"}}}, map[string]any{"role": "user", "content": "Continue."}}
			} else {
				body["messages"] = []any{map[string]any{"role": "assistant", "content": "Ready", "reasoning_items": []any{map[string]any{"type": "reasoning", "encrypted_content": "vendor-history"}}}, map[string]any{"role": "user", "content": "Continue."}}
			}
			headers := map[string]string{"X-Session-Id": "bypass-existing-session"}
			first := h.doHeaders("POST", path, owner.key, body, headers)
			if first.status != 200 {
				t.Fatal(first.describe())
			}
			h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_name": "history-renamed"})
			body["model"] = "history-renamed"
			second := h.doHeaders("POST", path, owner.key, body, headers)
			if second.status != 200 {
				t.Fatal(second.describe())
			}
			if got := h.doHeaders("POST", path, "invalid-key", body, headers); got.status != 401 {
				t.Fatalf("非法Key未拒绝: %s", got.describe())
			}
			body["previous_response_id"] = "unknown-parent"
			if got := h.doHeaders("POST", path, owner.key, body, headers); got.status != 400 {
				t.Fatalf("未知父响应未拒绝: %s", got.describe())
			}
			if calls.Load() != 2 {
				t.Fatalf("拒绝请求仍外发: %d", calls.Load())
			}
			h.flushSpend()
		})
	}
}
