package dialogue

import (
	"fmt"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// nativeThinkingEvents 返回完整原生思考/工具 SSE 样本，参数无，返回事件列表；仅供纯内存传输测试，无外部副作用。
func nativeThinkingEvents() []string {
	return []string{`{"type":"message_start","message":{"id":"msg_native","type":"message","role":"assistant","model":"private","content":[],"usage":{"input_tokens":8}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"native-"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signature"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"native-cipher"}}`, `{"type":"content_block_stop","index":1}`, `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call","name":"lookup","caller":{"type":"direct"},"input":{}}}`, `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`, `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"a\"}"}}`, `{"type":"content_block_stop","index":2}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`, `{"type":"message_stop"}`}
}

// TestNativeMessagesStreamHistory 验证原生签名分片/密文/工具参数/usage保留以及坏流不能发布归属或成功终态。
// 参数 t 为上下文；完整、缺终态、缺结束原因、未闭合块、错误类型/索引/JSON均使用内存HTTP，无外部清理。
func TestNativeMessagesStreamHistory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]string) []string
		ok     bool
	}{{"valid", func(e []string) []string { return e }, true}, {"no_terminal", func(e []string) []string { return e[:len(e)-1] }, false}, {"no_reason", func(e []string) []string { return append(e[:len(e)-2], e[len(e)-1]) }, false}, {"open_block", func(e []string) []string { return append(e[:11], e[12:]...) }, false}, {"bad_signature", func(e []string) []string { e[3] = strings.Replace(e[3], `"native-"`, `42`, 1); return e }, false}, {"bad_index", func(e []string) []string { e[1] = strings.Replace(e[1], `"index":0`, `"index":1.5`, 1); return e }, false}, {"bad_args", func(e []string) []string { e[10] = strings.Replace(e[10], `\"a\"}`, `broken`, 1); return e }, false}, {"bad_caller", func(e []string) []string { e[8] = strings.Replace(e[8], `"direct"`, `"programmatic"`, 1); return e }, false}, {"delta_after_stop", func(e []string) []string { e[6] = e[4]; return e }, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var raw strings.Builder
			for _, e := range tc.change(nativeThinkingEvents()) {
				fmt.Fprintf(&raw, "data: %s\n\n", e)
			}
			w := httptest.NewRecorder()
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(raw.String()))}
			committed := false
			var result llmdialogue.DialogueResult
			wrote, usage, _, capture, err := PipeDialogue(w, resp, time.Now(), "public", "anthropic-messages", "anthropic-messages", func(r llmdialogue.DialogueResult) {
				if strings.Contains(w.Body.String(), "message_stop") {
					t.Error("归属在终态之后才提交")
				}
				committed = true
				result = r
			})
			if (err == nil) != tc.ok || committed != tc.ok {
				t.Fatalf("原生流生命周期不符: committed=%t err=%v body=%s", committed, err, capture)
			}
			if tc.ok {
				if !wrote || usage["total_tokens"] != float64(10) || result.Native == nil || !strings.Contains(string(capture), `"model":"public"`) {
					t.Fatalf("原生流事实丢失: %v %s", usage, capture)
				}
				content := result.Native["content"].([]any)
				if content[0].(map[string]any)["signature"] != "native-signature" || content[1].(map[string]any)["data"] != "native-cipher" || content[2].(map[string]any)["input"].(map[string]any)["path"] != "a" {
					t.Fatalf("快照拼接改变内容: %v", content)
				}
			} else if strings.Contains(string(capture), `"type":"message_stop"`) {
				t.Fatal("坏流仍发布成功终态")
			}
		})
	}
}
