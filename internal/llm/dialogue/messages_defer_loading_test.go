package dialogue

import "testing"

// TestMessagesDeferLoading 验证工具加载标记原生保留、false可桥接、true不可静默降级。
// 参数t为测试上下文；纯内存输入覆盖布尔值、错误类型及缺省，无外部资源或清理。
func TestMessagesDeferLoading(t *testing.T) {
	for _, value := range []any{true, false, nil, "true", 1} {
		tool := map[string]any{"name": "read", "input_schema": map[string]any{"type": "object"}, "defer_loading": value}
		body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Read the file."}}, "tools": []any{tool}}
		d, err := ParseDialogue("anthropic-messages", body)
		flag, valid := value.(bool)
		if !valid {
			if err == nil {
				t.Fatalf("非法标记被接受: %v", value)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		native, err := EncodeDialogue(d, "anthropic-messages", "claude")
		if err != nil || dialogueMap(dialogueList(native["tools"])[0])["defer_loading"] != flag {
			t.Fatalf("原生标记丢失: %v", err)
		}
		for _, protocol := range []string{"openai-chat", "openai-responses"} {
			_, err := EncodeDialogue(d, protocol, "model")
			if (err != nil) != flag {
				t.Fatalf("桥接标记%v错误: %v", flag, err)
			}
		}
		if tool["defer_loading"] != flag {
			t.Fatal("修改了原始工具定义")
		}
	}
}
