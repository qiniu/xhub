package dialogue

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestStreamErrorDetailsMatrix 验证 Chat/Responses/Messages 原生与转换流在首帧/中途失败均保留诊断。
// 使用内存 SSE；参数 t 为上下文，核对 code/type/message/request_id、不产生成功终态，正文由管道关闭，无外部清理。
func TestStreamErrorDetailsMatrix(t *testing.T) {
	starts := map[string]string{
		"openai-chat":        `{"id":"resp-test","choices":[{"index":0,"delta":{"content":"Partial answer"}}]}`,
		"openai-responses":   `{"type":"response.output_text.delta","delta":"Partial answer"}`,
		"anthropic-messages": `{"type":"message_start","message":{"id":"resp-test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1}}}`,
	}
	for source, start := range starts {
		for _, target := range []string{"openai-chat", "openai-responses", "anthropic-messages"} {
			for _, partial := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/partial=%v", source, target, partial), func(t *testing.T) {
					detail := `{"code":"vendor_capacity","type":"overloaded_error","message":"Vendor pool busy: 保留原文","details":{"retryable":true},"request_id":"req-vendor"}`
					failure := `{"type":"error","error":` + detail + `,"request_id":"req-event"}`
					if source == "openai-responses" {
						failure = `{"type":"response.failed","response":{"id":"resp-test","error":` + detail + `},"request_id":"req-event"}`
					}
					input := ""
					if partial {
						input = "data: " + start + "\n\n"
					}
					input += "data: " + failure + "\n\n"
					resp := &http.Response{Body: io.NopCloser(strings.NewReader(input))}
					w := httptest.NewRecorder()
					wrote, _, _, capture, err := PipeDialogue(w, resp, time.Now(), "public", source, target)
					if err == nil {
						t.Fatal("上游失败被当作成功")
					}
					for _, marker := range []string{"vendor_capacity", "overloaded_error", "保留原文", "req-vendor", "req-event", "retryable"} {
						if !strings.Contains(err.Error(), marker) {
							t.Fatalf("返回错误丢失 %s: %v", marker, err)
						}
						if wrote && !strings.Contains(string(capture), marker) {
							t.Fatalf("客户端错误丢失 %s: %s", marker, capture)
						}
					}
					if strings.Contains(string(capture), `"type":"response.completed"`) || strings.Contains(string(capture), `"type":"message_stop"`) || strings.Contains(string(capture), "[DONE]") {
						t.Fatalf("失败产生成功终态: %s", capture)
					}
				})
			}
		}
	}
}
