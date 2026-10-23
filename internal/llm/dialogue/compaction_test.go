package dialogue

import "testing"

// TestCompactionNativeOnly 验证压缩历史原样保留且不允许跨协议降级，非法密文被明确拒绝。
// 参数 t 为测试上下文；全部为内存请求，无外部服务或数据清理。
func TestCompactionNativeOnly(t *testing.T) {
	for _, value := range []any{"vendor-cipher", "", nil, 42, "  "} {
		body := map[string]any{"input": []any{map[string]any{"type": "compaction", "encrypted_content": value}, map[string]any{"role": "user", "content": "Continue."}}}
		d, err := ParseDialogue("openai-responses", body)
		if value != "vendor-cipher" {
			if err == nil {
				t.Fatalf("非法压缩密文 %v 被接受", value)
			}
			continue
		}
		if err != nil || d.BridgeError == "" {
			t.Fatalf("压缩历史未限制为原生协议: %v", err)
		}
		if d.Native["input"].([]any)[0].(map[string]any)["encrypted_content"] != value {
			t.Fatal("压缩上下文被改写")
		}
		if _, err := EncodeDialogue(d, "openai-chat", "chat-model"); err == nil {
			t.Fatal("压缩历史被错误转换为 Chat")
		}
		native, err := EncodeDialogue(d, "openai-responses", "native-model")
		if err != nil || native["input"].([]any)[0].(map[string]any)["encrypted_content"] != value {
			t.Fatalf("原生编码丢失压缩密文: %v", err)
		}
	}
}
