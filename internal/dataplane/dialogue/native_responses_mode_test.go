package dialogue

import (
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNativeResponsesModeStream 验证灰度SSE保留新增内容并先提交终态，旧模式拒绝；无终态/错误均不提交。
// 使用内存HTTP正文，无网络或存储；读取器由流执行器关闭。
func TestNativeResponsesModeStream(t *testing.T) {
	terminal := `{"type":"response.completed","response":{"id":"resp_x","status":"completed","output":[{"type":"message","content":[{"type":"future_output","data":"keep"}]}],"usage":{"input_tokens":1,"output_tokens":1}}}`
	for _, tc := range []struct {
		name, data      string
		native, success bool
	}{
		{"native", terminal, true, true}, {"legacy", terminal, false, false},
		{"incomplete-envelope", `{"type":"response.completed","response":{"id":"x","status":"in_progress","output":[]}}`, true, false},
		{"error", `{"type":"response.failed","response":{"error":{"message":"Vendor failure"}}}`, true, false},
		{"missing-terminal", `{"type":"response.output_text.delta","delta":"Partial"}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			commits := 0
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + tc.data + "\n\n"))}
			_, _, _, _, err := PipePortableResponsesMode(rec, resp, time.Now(), "public", tc.native, func(r llmdialogue.DialogueResult) {
				commits++
				if strings.Contains(rec.Body.String(), "response.completed") {
					t.Error("终态先于历史提交")
				}
			})
			if (err == nil) != tc.success {
				t.Fatalf("终态处理错误: %v", err)
			}
			if tc.success {
				if commits != 1 || !strings.Contains(rec.Body.String(), "future_output") {
					t.Fatal("丢失扩展或未提交")
				}
			} else if commits != 0 {
				t.Fatal("失败流提交历史")
			}
		})
	}
}

// TestNativeResponsesHistoryEnvelope 验证独立原生校验仅在可信开关开启后用于历史恢复，保持原始对象及父响应消费规则。
// 纯内存输入，无需清理；非法父响应和store类型始终拒绝。
func TestNativeResponsesHistoryEnvelope(t *testing.T) {
	body := map[string]any{"input": []any{map[string]any{"type": "future_item", "payload": "keep"}}, "previous_response_id": "parent", "store": true}
	history := []llmdialogue.Turn{{NativeProtocol: "openai-responses", Native: map[string]any{"role": "user", "content": "Hello"}}}
	if _, err := ResponsesRequestBody(body, history); err == nil {
		t.Fatal("关闭开关仍接受新项")
	}
	out, err := ResponsesRequestBody(body, history, true)
	if err != nil || len(out["input"].([]any)) != 2 || out["previous_response_id"] != nil || out["store"] != nil {
		t.Fatalf("历史恢复错误: %v %v", out, err)
	}
	if body["previous_response_id"] != "parent" || len(body["input"].([]any)) != 1 {
		t.Fatal("修改了原始请求")
	}
	for _, bad := range []map[string]any{{"input": "Hello", "store": "yes"}, {"input": "Hello", "previous_response_id": 5}} {
		if _, err := ResponsesRequestBody(bad, nil, true); err == nil {
			t.Fatal("灰度绕过历史信封检查")
		}
	}
}
