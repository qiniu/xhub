package regression

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// visionToolResultRequest 模拟 Claude Code Read 返回图文的完整工具续轮；参数为模型、流标记和图片数据。
// 返回独立请求对象，供真实后台测试使用；所有内容为合成数据，不读取用户文件或产生外部副作用。
func visionToolResultRequest(model string, stream bool, data string) map[string]any {
	return map[string]any{"model": model, "stream": stream, "max_tokens": 64, "messages": []any{
		map[string]any{"role": "user", "content": "Read the image file."},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "read_image", "name": "Read", "input": map[string]any{"file_path": "/synthetic/image.png"}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "read_image", "content": []any{
			map[string]any{"type": "text", "text": "Image file contents:"},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": data}},
		}}}},
	}, "tools": []any{map[string]any{"name": "Read", "description": "Read a file.", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string"}}}}}}
}

// TestVisionToolResultHardBudget 验证原生 Messages Read 图片续轮经过有限额度准入、完整保留原文并准确结算。
// 前置私有 PostgreSQL schema 与本机上游；JSON/SSE 均验证上界头、终态、账单、零占用，非法图片及不可桥接历史零外发拒绝。
// httptest 与 harness 在结束时回收服务、账号、Key、账单和 schema，不使用真实上游凭据。
func TestVisionToolResultHardBudget(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	var calls atomic.Int32
	var expected atomic.Value
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(body["messages"], expected.Load()) {
			t.Error("Read 工具结果图片、顺序或调用ID被修改")
		}
		w.Header().Set("Content-Type", "application/json")
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, streamTerminalFrames("messages"))
			return
		}
		_, _ = io.WriteString(w, `{"id":"msg_vision_read","type":"message","role":"assistant","content":[{"type":"text","text":"TERMINAL_OK"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`)
	}))
	t.Cleanup(up.Close)
	dep := hardBudgetDeployment("claude-vision-read")
	dep.LiteLLMParams["api_base"] = up.URL
	dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
	dep.ModelInfo["transport"] = "bypass_anthropic_messages"
	dep.ModelInfo["endpoint_types"] = []string{"messages"}
	chat := hardBudgetDeployment("chat-vision-read")
	chat.LiteLLMParams["api_base"] = up.URL
	h := newHarness(t, dep, chat)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "vision-read-owner")
	h.setKeyBudget(t, admin, owner.key, 1)
	for _, stream := range []bool{false, true} {
		body := visionToolResultRequest(dep.ModelName, stream, "AQID")
		expected.Store(body["messages"])
		got := h.do("POST", "/v1/messages", owner.key, body)
		if got.status != 200 || !strings.Contains(got.text(), "TERMINAL_OK") || got.header("X-XHub-Budget-Guarantee") != "soft" {
			t.Fatalf("Read 图片硬额度续轮失败: %s", got.describe())
		}
		if stream && !strings.Contains(got.text(), "message_stop") {
			t.Fatal("Read 图片流没有完整终态")
		}
	}
	for _, tc := range []struct{ model, data, reason string }{{dep.ModelName, "???", "非法图片"}, {chat.ModelName, "AQID", "跨协议图片工具结果"}} {
		before := calls.Load()
		got := h.do("POST", "/v1/messages", owner.key, visionToolResultRequest(tc.model, false, tc.data))
		if got.status != 400 || calls.Load() != before {
			t.Fatalf("%s 未零外发拒绝: %s", tc.reason, got.describe())
		}
	}
	holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
	if err != nil || len(holds) != 0 {
		t.Fatalf("Read 图片结算残留预占: %v %v", holds, err)
	}
	key, err := h.db.GetKey(t.Context(), owner.keyID)
	if err != nil || !nearlyEqual(key.Spend, 2*0.000032) {
		t.Fatalf("Read 图片token账单错误: %+v %v", key, err)
	}
}
