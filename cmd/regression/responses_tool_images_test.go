package regression

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// TestResponsesToolImages 验证工具图片经真实网关原样到原生上游、同步流式及后续文本可用；失败和跨协议零外发。
// 参数t为上下文；隔离数据库及本地上游验证错误状态和日志，harness清理账号、部署及账单，无生产依赖。
func TestResponsesToolImages(t *testing.T) {
	var calls atomic.Int32
	var expected atomic.Value
	var status atomic.Int32
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !reflect.DeepEqual(body["input"], expected.Load()) {
			t.Error("工具图片、文本或顺序丢失")
		}
		if code := int(status.Load()); code != 0 {
			w.WriteHeader(code)
			writeJSON(w, map[string]any{"error": map[string]any{"type": "api_error", "message": "Service temporarily unavailable"}})
			return
		}
		writeAgentResponse(w, nativeAgentResponse("resp_image", "Image received", false, nil), body["stream"] == true)
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
	image := map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AQID"}
	input := []any{map[string]any{"type": "function_call", "call_id": "read_1", "name": "read", "arguments": "{}"}, map[string]any{"type": "function_call_output", "call_id": "read_1", "output": []any{map[string]any{"type": "input_text", "text": "Image:"}, image}}, map[string]any{"role": "user", "content": "Describe this image."}}
	body := map[string]any{"model": "history-model", "input": input}
	for _, stream := range []bool{false, true} {
		body["stream"] = stream
		expected.Store(input)
		got := h.do("POST", "/v1/responses", owner.key, body)
		if got.status != 200 || !strings.Contains(got.text(), "Image received") {
			t.Fatalf("工具图片续轮失败: %s", got.describe())
		}
	}
	input = append(input, map[string]any{"role": "user", "content": "Continue."})
	body["input"] = input
	expected.Store(input)
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 200 {
		t.Fatal(got.describe())
	}
	before := calls.Load()
	image["image_url"] = "file:///tmp/private"
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || calls.Load() != before {
		t.Fatal("非法图片没有零外发拒绝")
	}
	image["image_url"] = "data:image/png;base64,AQID"
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_chat", "endpoint_types": []string{"responses"}}})
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || calls.Load() != before {
		t.Fatal("跨协议未阻止丢图")
	}
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
	status.Store(503)
	body["stream"] = false
	got := h.do("POST", "/v1/responses", owner.key, body)
	if got.status != 503 || !strings.Contains(got.text(), "Service temporarily unavailable") {
		t.Fatalf("上游503被改写: %s", got.describe())
	}
	h.flushSpend()
	detail := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
	if detail["metadata"].(map[string]any)["http_status"] != float64(503) || detail["error"] != got.text() {
		t.Fatal("客户端与日志错误不一致")
	}
}
