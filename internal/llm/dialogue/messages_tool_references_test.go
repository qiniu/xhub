package dialogue

import (
	"encoding/json"
	"reflect"
	"testing"
)

// referenceRequest 构造合成工具发现历史，参数为工具结果和输出选项；返回独立请求供单元测试，无外部数据。
func referenceRequest(content any, output any) map[string]any {
	return map[string]any{"model": "public", "max_tokens": 128, "output_config": output,
		"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "search-1", "name": "ToolSearch", "input": map[string]any{"query": "lookup"}}}}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "search-1", "content": content}}}}}
}

// TestMessagesToolReferencesRoundTrip 验证引用、混合图片/文本和错误结果原生无损保留、跨协议拒绝；纯内存，每项独立无清理。
func TestMessagesToolReferencesRoundTrip(t *testing.T) {
	for _, kind := range []string{"references", "text", "image", "error"} {
		t.Run(kind, func(t *testing.T) {
			parts := []any{map[string]any{"type": "tool_reference", "tool_name": "lookup", "vendor_metadata": map[string]any{"version": 2}}}
			if kind == "text" {
				parts = append(parts, map[string]any{"type": "text", "text": "工具原文保持不变"})
			}
			if kind == "image" {
				parts = append(parts, toolResultImageFixture())
			}
			body := referenceRequest(parts, map[string]any{"effort": "high", "future_option": true})
			if kind == "error" {
				dialogueMap(dialogueList(dialogueMap(dialogueList(body["messages"])[1])["content"])[0])["is_error"] = true
			}
			before, _ := json.Marshal(body)
			d, err := ParseDialogue("anthropic-messages", body)
			if err != nil {
				t.Fatal(err)
			}
			out, err := EncodeDialogue(d, "anthropic-messages", "real")
			if err != nil {
				t.Fatal(err)
			}
			delete(out, "model")
			copy, _ := cloneObject(body)
			delete(copy, "model")
			if !reflect.DeepEqual(out, copy) {
				t.Fatalf("原生引用或配置被改写: %#v", out)
			}
			for _, target := range []string{"openai-chat", "openai-responses"} {
				if _, err := EncodeDialogue(d, target, "real"); err == nil {
					t.Fatalf("工具引用丢失后仍进入%s", target)
				}
			}
			after, _ := json.Marshal(body)
			if string(before) != string(after) {
				t.Fatal("修改了原始请求")
			}
		})
	}
}

// TestMessagesToolReferenceValidation 验证缺失名称、非法角色/ID/错误标记失败，空引用数组与普通文本仍可桥接；无外部依赖或清理。
func TestMessagesToolReferenceValidation(t *testing.T) {
	for _, kind := range []string{"missing_name", "empty_name", "number_name", "role", "id", "error"} {
		t.Run(kind, func(t *testing.T) {
			ref := map[string]any{"type": "tool_reference", "tool_name": "lookup"}
			body := referenceRequest([]any{ref}, map[string]any{"effort": "high"})
			message := dialogueMap(dialogueList(body["messages"])[1])
			block := dialogueMap(dialogueList(message["content"])[0])
			switch kind {
			case "missing_name":
				delete(ref, "tool_name")
			case "empty_name":
				ref["tool_name"] = " "
			case "number_name":
				ref["tool_name"] = 3
			case "role":
				message["role"] = "assistant"
			case "id":
				delete(block, "tool_use_id")
			case "error":
				block["is_error"] = "true"
			}
			if _, err := ParseDialogue("anthropic-messages", body); err == nil {
				t.Fatal("畸形工具引用被接受")
			}
		})
	}
	for _, parts := range []any{[]any{}, "No tools found"} {
		body := referenceRequest(parts, map[string]any{"effort": "high"})
		d, err := ParseDialogue("anthropic-messages", body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := EncodeDialogue(d, "openai-chat", "real"); err != nil {
			t.Fatalf("普通工具结果桥接回归: %v", err)
		}
	}
}

// TestMessagesOutputConfigNative 验证新会话首轮原生保留 effort/format/未来扩展；对象类型非法仍失败，纯内存无需清理。
func TestMessagesOutputConfigNative(t *testing.T) {
	for _, option := range []any{map[string]any{"effort": "high"}, map[string]any{"format": map[string]any{"type": "json_schema", "schema": map[string]any{"type": "object"}}}, map[string]any{"future_option": map[string]any{"enabled": true}}, "invalid"} {
		body := map[string]any{"model": "public", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}, "output_config": option}
		d, err := ParseDialogue("anthropic-messages", body)
		if option == "invalid" {
			if err == nil {
				t.Fatal("非法 output_config 类型被接受")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out, err := EncodeDialogue(d, "anthropic-messages", "public")
		if err != nil || !reflect.DeepEqual(out, body) {
			t.Fatalf("新会话配置丢失: %v %#v", err, out)
		}
	}
}
