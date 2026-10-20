package dialogue

import "testing"

// TestMessagesReasoningContextRejectsUnmapped 验证 Messages 不会静默忽略 Responses 的历史投影指令。
// 前置合法文字与公开历史；已知/未知枚举和错误类型均在外发前拒绝，缺省仍可编码，内存对象无需清理。
func TestMessagesReasoningContextRejectsUnmapped(t *testing.T) {
	for _, value := range []any{"current_turn", "all_turns", "auto", "unknown", 42, map[string]any{}} {
		body := map[string]any{"input": "Hello", "reasoning": map[string]any{"effort": "none", "context": value}}
		d, err := ParseDialogue("openai-responses", body)
		if err == nil {
			_, err = EncodeDialogue(d, "anthropic-messages", "native")
		}
		if err == nil {
			t.Fatalf("未实现的 Messages 历史投影被忽略: %v", value)
		}
	}
}
