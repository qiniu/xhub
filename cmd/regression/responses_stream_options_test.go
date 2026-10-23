package regression

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// TestResponsesStreamOptions 验证Codex流参数经真实网关完整转发，后续文本、日志及上游拒绝保留。
// 参数t为上下文；本地上游与隔离PG，非法类型、跨协议零外发；harness负责账号、部署和账单清理。
func TestResponsesStreamOptions(t *testing.T) {
	var calls atomic.Int32
	var reject atomic.Bool
	options := map[string]any{"reasoning_summary_delivery": "sequential_cutoff", "include_obfuscation": false}
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !reflect.DeepEqual(body["stream_options"], options) {
			t.Error("原生流参数丢失或被注入Chat参数")
		}
		if reject.Load() {
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"error": map[string]any{"message": "Unsupported delivery mode", "type": "invalid_request_error"}})
			return
		}
		writeAgentResponse(w, nativeAgentResponse("resp_stream_options", "Stream options preserved", false, nil), body["stream"] == true)
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
	body := map[string]any{"model": "history-model", "input": "Hello", "stream": true, "stream_options": options}
	for _, stream := range []bool{false, true} {
		body["stream"] = stream
		got := h.do("POST", "/v1/responses", owner.key, body)
		if got.status != 200 || !strings.Contains(got.text(), "Stream options preserved") {
			t.Fatalf("流选项请求失败: %s", got.describe())
		}
		if stream && !strings.Contains(got.text(), "response.completed") {
			t.Fatal("缺少完成事件")
		}
		h.flushSpend()
		detail := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
		if detail["status"] != "success" {
			t.Fatal("成功流被记为失败")
		}
	}
	body["input"] = "Continue."
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 200 {
		t.Fatal(got.describe())
	}
	before := calls.Load()
	body["stream_options"] = map[string]any{"reasoning_summary_delivery": true}
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || calls.Load() != before {
		t.Fatal("非法类型未零外发拒绝")
	}
	body["stream_options"] = options
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_chat", "endpoint_types": []string{"responses"}}})
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || calls.Load() != before {
		t.Fatal("跨协议流参数被静默丢弃")
	}
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
	reject.Store(true)
	body["stream"] = false
	got := h.do("POST", "/v1/responses", owner.key, body)
	if got.status != 400 || !strings.Contains(got.text(), "Unsupported delivery mode") {
		t.Fatalf("上游参数拒绝未保留: %s", got.describe())
	}
}
