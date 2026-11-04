package regression

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestWorkBuddyChatToResponses 验证实测 WorkBuddy 消息经真实网关转换到 Responses。
// 前置隔离数据库和本地上游；覆盖流式、非流式、鉴权、未知字段和账单，harness 清理专属数据。
func TestWorkBuddyChatToResponses(t *testing.T) {
	var calls atomic.Int32
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		raw, _ := json.Marshal(body["input"])
		if r.URL.Path != "/v1/responses" || !strings.Contains(string(raw), "hello") || strings.Contains(string(raw), "conversationRequestId") || strings.Contains(string(raw), `"agent"`) {
			t.Errorf("上游路径或 WorkBuddy 跟踪字段处理错误: path=%s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		writeAgentResponse(w, nativeAgentResponse("resp_workbuddy", "Hello there", false, nil), body["stream"] == true)
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"chat"}}})
	for _, stream := range []bool{false, true} {
		body := map[string]any{"model": "history-model", "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hello", "agent": "cli", "startsNewUserRequest": true, "conversationRequestId": "request-1"}, map[string]any{"role": "assistant", "content": "Prior reply", "agent": "cli", "conversationRequestId": "request-1", "messageId": "message-1", "stepSeq": 1, "model": "history-model", "requestModelId": "history-model", "requestModelName": "history-model", "traceId": "trace-1", "rawUsage": map[string]any{"prompt_tokens": 1}, "usage": map[string]any{"completion_tokens": 1}}, map[string]any{"role": "user", "content": "hello again", "agent": "cli", "startsNewUserRequest": true, "conversationRequestId": "request-2"}}, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "read", "parameters": map[string]any{"type": "object"}, "strict": true}}}}
		got := h.do("POST", "/v1/chat/completions", owner.key, body)
		if got.status != 200 || !strings.Contains(got.text(), "Hello there") {
			t.Fatalf("WorkBuddy Chat 桥接失败: %s", got.describe())
		}
		if stream && !strings.Contains(got.text(), "[DONE]") {
			t.Fatal("流式完成标记缺失")
		}
		h.flushSpend()
		bill := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
		if bill["status"] != "success" || bill["api_key"] != owner.keyID {
			t.Fatalf("账单身份或状态错误: %v", bill)
		}
		before := calls.Load()
		body["messages"] = []any{map[string]any{"role": "user", "content": "hello", "agent": "cli", "unsupported_business_field": true}}
		if bad := h.do("POST", "/v1/chat/completions", owner.key, body); bad.status != 400 {
			t.Fatalf("未知字段应拒绝: %s", bad.describe())
		}
		if bad := h.do("POST", "/v1/chat/completions", "sk-invalid", body); bad.status != 401 {
			t.Fatalf("错误密钥应拒绝: %s", bad.describe())
		}
		if calls.Load() != before {
			t.Fatal("错误请求不应发送到上游")
		}
	}
}
