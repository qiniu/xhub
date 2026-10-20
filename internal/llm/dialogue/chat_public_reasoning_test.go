package dialogue

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestChatPublicReasoningClassification 验证公开摘要分类与解析共用严格边界，非法扩展和任何密文字段均不允许普通迁移。
// 参数 t 为测试上下文；前置纯内存数据，覆盖嵌套签名、未知字段、错误类型及空摘要，无网络、持久化或清理需要。
func TestChatPublicReasoningClassification(t *testing.T) {
	for _, tc := range []struct {
		name, patch   string
		valid, opaque bool
	}{
		{"public", `"summary":[{"type":"summary_text","text":"Plan"}]`, true, false},
		{"empty", `"summary":[]`, true, false},
		{"missing-summary", `"status":"completed"`, true, false},
		{"multiple", `"summary":[{"type":"summary_text","text":"Plan"},{"type":"summary_text","text":"Next"}]`, true, false},
		{"cipher", `"encrypted_content":"cipher"`, true, true},
		{"null-cipher", `"encrypted_content":null`, true, true},
		{"empty-cipher", `"encrypted_content":""`, true, true},
		{"signature", `"signature":"signed"`, false, true},
		{"nested-signature", `"summary":[{"type":"summary_text","text":"Plan","signature":"signed"}]`, false, true},
		{"nested-cipher", `"summary":[{"type":"summary_text","text":"Plan","encrypted_content":"cipher"}]`, false, true},
		{"unknown", `"summary":[{"type":"summary_text","text":"Plan","future_state":{}}]`, false, true},
		{"bad-text", `"summary":[{"type":"summary_text","text":42}]`, false, true},
		{"bad-type", `"summary":[{"type":"thinking","text":"Plan"}]`, false, true},
		{"bad-summary", `"summary":null`, false, true},
		{"bad-status", `"status":[]`, false, true},
		{"unknown-status", `"status":"future"`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value []any
			if err := json.Unmarshal([]byte(`[{"type":"reasoning","id":"rs",`+tc.patch+`}]`), &value); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(value)
			if got := ChatReasoningRequiresNativeOwnership(value, "assistant"); got != tc.opaque {
				t.Fatalf("归属分类错误: got=%v want=%v", got, tc.opaque)
			}
			turns, err := chatReasoningReplay(value, "assistant")
			if (err == nil) != tc.valid {
				t.Fatalf("结构校验错误: %v", err)
			}
			if err == nil && tc.name == "multiple" && turns[0].Reasoning != "Plan\n\nNext" {
				t.Fatalf("多摘要顺序或间隔丢失: %+v", turns)
			}
			after, _ := json.Marshal(value)
			if string(before) != string(after) {
				t.Fatal("分类或解析修改了输入")
			}
		})
	}
	for _, value := range []any{"invalid", []any{nil}, []any{map[string]any{"type": "reasoning", "id": 42}}, []any{map[string]any{"type": "reasoning", "id": "rs"}, map[string]any{"type": "reasoning", "id": "rs2", "signature": "sig"}}} {
		if !ChatReasoningRequiresNativeOwnership(value, "assistant") {
			t.Fatalf("非法扩展未固定: %v", value)
		}
	}
	if !ChatReasoningRequiresNativeOwnership([]any{map[string]any{"type": "reasoning", "id": "rs"}}, "user") {
		t.Fatal("非 assistant 角色被允许迁移")
	}
}

// TestChatPublicReasoningDoesNotDuplicateProjection 验证公开结构化摘要优先于重复的 reasoning_content，工具结果原文与调用 ID 保留。
// 参数 t 为测试上下文；输入包含两段摘要和工具完整历史，转换三种协议后验证语义；仅内存数据，无需清理。
func TestChatPublicReasoningDoesNotDuplicateProjection(t *testing.T) {
	var body map[string]any
	raw := `{"messages":[{"role":"user","content":"Read the value."},{"role":"assistant","content":"","reasoning_content":"duplicate projection","reasoning_items":[{"type":"reasoning","id":"rs1","summary":[{"type":"summary_text","text":"Public plan"},{"type":"summary_text","text":"Next step"}]}],"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"unique-value"}]}`
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	d, err := ParseDialogue("openai-chat", body)
	if err != nil {
		t.Fatal(err)
	}
	// 直接检查共享表示的重新编码；同协议原生直通保留用户的两个原始投影字段，不执行拼接。
	d.Source, d.Native = "", nil
	for _, protocol := range []string{"openai-chat", "anthropic-messages", "openai-responses"} {
		out, err := EncodeDialogue(d, protocol, "target")
		if err != nil {
			t.Fatalf("公开摘要不能转换为 %s: %v", protocol, err)
		}
		encoded, _ := json.Marshal(out)
		s := string(encoded)
		if strings.Count(s, "Public plan") != 1 || strings.Count(s, "Next step") != 1 || strings.Contains(s, "duplicate projection") || !strings.Contains(s, "unique-value") || !strings.Contains(s, "call_1") {
			t.Fatalf("%s 摘要去重或工具保留失败: %s", protocol, s)
		}
	}
}
