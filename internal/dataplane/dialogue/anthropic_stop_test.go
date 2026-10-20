package dialogue

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestAnthropicChatStopReasons 验证完整 Messages SSE 到 Chat 的终态枚举、空停止原因和原生协议保持。
// 前置仅含合成文本的内存上游；自然结束/停止序列规范化，截断保留 length，缺失终态拒绝，无外部资源。
func TestAnthropicChatStopReasons(t *testing.T) {
	for _, tc := range []struct{ stop, want string }{
		{"end_turn", "stop"}, {"stop_sequence", "stop"}, {"max_tokens", "length"}, {"", ""},
	} {
		t.Run(tc.stop, func(t *testing.T) {
			raw := `data: {"type":"message_start","message":{"id":"msg_stop","type":"message","role":"assistant","content":[],"usage":{"input_tokens":8,"output_tokens":0}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"` + tc.stop + `"},"usage":{"output_tokens":2}}

data: {"type":"message_stop"}

`
			for _, target := range []string{"openai-chat", "anthropic-messages"} {
				w := httptest.NewRecorder()
				_, _, _, capture, err := PipeDialogue(w, &http.Response{Body: io.NopCloser(strings.NewReader(raw))}, time.Now(), "test", "anthropic-messages", target)
				if tc.stop == "" {
					if err == nil {
						t.Fatal("缺停止原因却返回成功")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				want := `"finish_reason":"` + tc.want + `"`
				if target == "anthropic-messages" {
					want = `"stop_reason":"` + tc.stop + `"`
				}
				if !strings.Contains(string(capture), want) {
					t.Fatalf("%s 停止原因不符合协议: %s", target, capture)
				}
			}
		})
	}
}
