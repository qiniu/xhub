package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

// TestMessagesDeferLoadingContract 验证真实用户Key携带工具加载标记经原生Messages原样外发并完成工具续轮。
// 参数t为测试上下文；本地上游检查JSON契约，覆盖布尔值与非法类型拒绝；harness回收独立schema和HTTP服务。
func TestMessagesDeferLoadingContract(t *testing.T) {
	for _, flag := range []bool{false, true} {
		t.Run(fmt.Sprint(flag), func(t *testing.T) {
			var calls atomic.Int32
			h, owner, _, _ := historyHarness(t, "messages", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				n := calls.Add(1)
				tool := body["tools"].([]any)[0].(map[string]any)
				if tool["defer_loading"] != flag {
					t.Errorf("工具加载标记丢失: %v", tool["defer_loading"])
				}
				content := []any{map[string]any{"type": "tool_use", "id": "call_read", "name": "read", "input": map[string]any{}}}
				if n > 1 {
					content = []any{map[string]any{"type": "text", "text": "defer-ok"}}
				}
				historyMessagesReply(w, content, false)
			}))
			tool := map[string]any{"name": "read", "input_schema": map[string]any{"type": "object"}, "defer_loading": flag}
			messages := []any{map[string]any{"role": "user", "content": "Read the file."}}
			body := map[string]any{"model": "history-model", "max_tokens": 64, "messages": messages, "tools": []any{tool}}
			first := h.ok("POST", "/v1/messages", owner.key, body)
			body["messages"] = append(messages, map[string]any{"role": "assistant", "content": first.json()["content"]}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_read", "content": "value"}}})
			h.ok("POST", "/v1/messages", owner.key, body)
			tool["defer_loading"] = "true"
			if got := h.do("POST", "/v1/messages", owner.key, body); got.status != 400 {
				t.Fatalf("非法标记未拒绝: %s", got.describe())
			}
			if calls.Load() != 2 {
				t.Fatalf("非法标记被外发: %d", calls.Load())
			}
			h.flushSpend()
		})
	}
}
