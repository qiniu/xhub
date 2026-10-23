package regression

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestExternalCompactionContinuation 验证 Codex 旧压缩历史经真实网关原样到上游，支持同步/流式且保留工具关联。
// 参数 t 为上下文；隔离数据库、用户和本地 HTTP 上游，验证父游标授权、错误、跨协议拒绝和费用；harness 自动清理。
func TestExternalCompactionContinuation(t *testing.T) {
	var calls atomic.Int32
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls.Add(1)
		text := string(mustJSON(body))
		if !strings.Contains(text, "old-cipher") || !strings.Contains(text, "call_old") || !strings.Contains(text, "file-value") {
			t.Error("压缩历史或工具上下文丢失")
		}
		if strings.Contains(text, "reject-cipher") {
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "invalid encrypted content"}})
			return
		}
		writeAgentResponse(w, nativeAgentResponse("resp_compaction", "Continued", false, nil), body["stream"] == true)
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
	input := []any{map[string]any{"type": "compaction", "id": "cmp_old", "encrypted_content": "old-cipher"}, map[string]any{"type": "function_call", "call_id": "call_old", "name": "read", "arguments": "{}"}, map[string]any{"type": "function_call_output", "call_id": "call_old", "output": "file-value"}, map[string]any{"role": "user", "content": "Continue."}}
	body := map[string]any{"model": "history-model", "input": input}
	for _, stream := range []bool{false, true} {
		body["stream"] = stream
		got := h.do("POST", "/v1/responses", owner.key, body)
		if got.status != 200 || !strings.Contains(got.text(), "Continued") {
			t.Fatalf("旧会话续聊失败: %s", got.describe())
		}
		h.flushSpend()
		log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
		if log["status"] != "success" {
			t.Fatalf("成功续聊日志错误: %v", log["status"])
		}
	}
	before := calls.Load()
	stranger := h.openScope(t, admin, "compaction-other")
	body["previous_response_id"] = "resp_compaction"
	if got := h.do("POST", "/v1/responses", stranger.key, body); got.status != 400 {
		t.Fatal("压缩历史绕过其他用户父响应归属")
	}
	body["previous_response_id"] = "unknown-parent"
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 {
		t.Fatal("未知父游标未拒绝")
	}
	delete(body, "previous_response_id")
	if got := h.do("POST", "/v1/responses", "invalid-key", body); got.status != 401 {
		t.Fatal("非法凭据未拒绝")
	}
	if calls.Load() != before {
		t.Fatal("拒绝请求调用了上游")
	}
	body["stream"] = false
	input[0].(map[string]any)["encrypted_content"] = "old-cipher-reject-cipher"
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || !strings.Contains(got.text(), "invalid encrypted content") {
		t.Fatalf("上游密文错误未正确返回: %s", got.describe())
	}
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_chat", "endpoint_types": []string{"responses"}}})
	before = calls.Load()
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status == 200 {
		t.Fatal("压缩上下文被无损转换假象掩盖")
	}
	if calls.Load() != before {
		t.Fatal("压缩历史被转发到非 Responses 上游")
	}
}
