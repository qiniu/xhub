package dialogue

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestChatReasoningItemsRoundTrip 验证有/无摘要和密文的 Responses 输出可投影为 Chat 并恢复工具续轮。
// 参数 t 为上下文；纯内存验证密文原厂 ID、公开摘要迁移、工具及用量保留和无重复摘要；无需外部清理。
func TestChatReasoningItemsRoundTrip(t *testing.T) {
	for _, summary := range []bool{false, true} {
		for _, cipher := range []bool{false, true} {
			t.Run(fmt.Sprintf("summary=%v/cipher=%v", summary, cipher), func(t *testing.T) {
				item := map[string]any{"id": "rs_original", "type": "reasoning", "summary": []any{}}
				if summary {
					item["summary"] = []any{map[string]any{"type": "summary_text", "text": "Plan"}}
				}
				if cipher {
					item["encrypted_content"] = "opaque-cipher"
				}
				raw, _ := json.Marshal(map[string]any{"id": "resp", "status": "completed", "output": []any{item,
					map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Reading"}}},
					map[string]any{"type": "function_call", "call_id": "call_1", "name": "read", "arguments": "{}"}},
					"usage": map[string]any{"input_tokens": 8, "output_tokens": 2}})
				result, err := ParseDialogueResult("openai-responses", raw)
				if err != nil {
					t.Fatal(err)
				}
				before, _ := json.Marshal(result.Native)
				encoded, err := EncodeDialogueResult(result, "openai-chat", "public")
				if err != nil {
					t.Fatal(err)
				}
				var response map[string]any
				_ = json.Unmarshal(encoded, &response)
				message := dialogueMap(dialogueMap(dialogueList(response["choices"])[0])["message"])
				if message["content"] != "Reading" || dialogueMap(dialogueList(message["reasoning_items"])[0])["id"] != "rs_original" || dialogueMap(response["usage"])["completion_tokens"] != float64(2) {
					t.Fatalf("Chat 投影丢失正文、原始推理 ID 或用量: %s", encoded)
				}
				if strings.Contains(dialogueString(message["content"])+dialogueString(message["reasoning_content"]), "opaque-cipher") {
					t.Fatal("密文被误当成可见推理")
				}
				body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Read the file."}, message, map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "文件原文"}}}
				d, err := ParseDialogue("openai-chat", body)
				if err != nil {
					t.Fatal(err)
				}
				out, err := EncodeDialogue(d, "openai-responses", "real")
				if err != nil {
					t.Fatal(err)
				}
				items := dialogueList(out["input"])
				if got := dialogueMap(items[1]); cipher && (got["id"] != "rs_original" || got["encrypted_content"] != "opaque-cipher") {
					t.Fatalf("Chat 续轮未恢复原始推理: %v", out)
				}
				encoded, _ = json.Marshal(out)
				wantReasoning := 0
				if summary || cipher {
					wantReasoning = 1
				}
				if strings.Count(string(encoded), `"type":"reasoning"`) != wantReasoning || !strings.Contains(string(encoded), "文件原文") || !strings.Contains(string(encoded), "call_1") {
					t.Fatalf("续轮重复推理或丢失工具: %s", encoded)
				}
				for _, target := range []string{"openai-chat", "anthropic-messages"} {
					converted, err := EncodeDialogue(d, target, "other")
					if cipher {
						if err == nil {
							t.Fatalf("不支持的推理扩展被发往 %s", target)
						}
					} else if err != nil {
						t.Fatalf("公开摘要未能迁移到 %s: %v", target, err)
					} else if target != "openai-chat" {
						raw, _ := json.Marshal(converted)
						wantSummary := 0
						if summary {
							wantSummary = 1
						}
						if strings.Count(string(raw), "Plan") != wantSummary {
							t.Fatalf("迁移公开摘要重复或丢失: %s", raw)
						}
					}
				}
				after, _ := json.Marshal(result.Native)
				if string(before) != string(after) {
					t.Fatal("Chat 编码修改了原始上游结果")
				}
				delete(message, "reasoning_items")
				d, err = ParseDialogue("openai-chat", body)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = EncodeDialogue(d, "openai-responses", "real"); err != nil {
					t.Fatalf("忽略扩展的普通 Chat 客户端无法续轮: %v", err)
				}
			})
		}
	}
}

// TestChatReasoningItemsBoundaries 验证可选扩展为空、非法形状、角色、字段类型及未知字段的明确边界。
// 参数 t 为上下文；内存输入正常返回或失败，不修改用户正文，无供应商、数据库和清理副作用。
func TestChatReasoningItemsBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		value      any
		valid      bool
	}{
		{"absent", "assistant", nil, true}, {"empty", "assistant", []any{}, true},
		{"wrong-list", "assistant", "opaque", false}, {"wrong-item", "assistant", []any{42}, false},
		{"user", "user", []any{map[string]any{"type": "reasoning", "id": "rs"}}, false},
		{"missing-id", "assistant", []any{map[string]any{"type": "reasoning"}}, false},
		{"wrong-kind", "assistant", []any{map[string]any{"type": "message", "id": "rs"}}, false},
		{"bad-cipher", "assistant", []any{map[string]any{"type": "reasoning", "id": "rs", "encrypted_content": 42}}, false},
		{"bad-summary", "assistant", []any{map[string]any{"type": "reasoning", "id": "rs", "summary": "Plan"}}, false},
		{"unknown-field", "assistant", []any{map[string]any{"type": "reasoning", "id": "rs", "unknown": true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"messages": []any{map[string]any{"role": tc.role, "content": "Hello", "reasoning_items": tc.value}}}
			before, _ := json.Marshal(body)
			_, err := ParseDialogue("openai-chat", body)
			if (err == nil) != tc.valid {
				t.Fatalf("Chat 推理边界错误: %v", err)
			}
			after, _ := json.Marshal(body)
			if string(before) != string(after) {
				t.Fatal("解析修改了原始输入")
			}
		})
	}
	if _, err := ChatReasoningItems(DialogueResult{Source: "openai-responses", Native: map[string]any{"output": []any{map[string]any{"type": "reasoning", "id": "rs", "summary": 42}}}}); err == nil {
		t.Fatal("非法上游摘要未拒绝")
	}
}
