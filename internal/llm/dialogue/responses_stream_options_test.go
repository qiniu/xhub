package dialogue

import (
	"reflect"
	"testing"
)

// TestResponsesStreamOptions 验证原生选项保留、已知类型校验、扩展隔离与Chat边界；合成内存请求，无需清理。
// 参数t为上下文；正常请求不修改输入，非等价跨协议不能丢字段后成功。
func TestResponsesStreamOptions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		value         any
		bad, boundary bool
	}{
		{"delivery", map[string]any{"reasoning_summary_delivery": "sequential_cutoff"}, false, true},
		{"combined", map[string]any{"reasoning_summary_delivery": "sequential_cutoff", "include_usage": true}, false, true},
		{"extension", map[string]any{"include_obfuscation": false}, false, true},
		{"usage", map[string]any{"include_usage": false}, false, false},
		{"empty", map[string]any{}, false, false}, {"null", nil, false, false},
		{"scalar", 3, true, false}, {"array", []any{}, true, false},
		{"delivery_type", map[string]any{"reasoning_summary_delivery": true}, true, false},
		{"delivery_empty", map[string]any{"reasoning_summary_delivery": " "}, true, false},
		{"usage_type", map[string]any{"include_usage": "true"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model": "source", "input": "Hello", "stream": true, "stream_options": tc.value}
			before, _ := cloneObject(body)
			d, err := ParseDialogue("openai-responses", body)
			if tc.bad {
				if err == nil {
					t.Fatal("非法流选项未拒绝")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			out, err := EncodeDialogue(d, "openai-responses", "target")
			if err != nil || !reflect.DeepEqual(out["stream_options"], tc.value) || !reflect.DeepEqual(before, body) {
				t.Fatalf("原生选项被修改: %v", err)
			}
			for _, protocol := range []string{"openai-chat", "anthropic-messages"} {
				_, err := EncodeDialogue(d, protocol, "target")
				if (err != nil) != tc.boundary {
					t.Fatalf("跨协议边界错误 %s: %v", protocol, err)
				}
			}
		})
	}
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Hello"}}, "stream_options": map[string]any{"reasoning_summary_delivery": "sequential_cutoff"}}
	if _, err := ParseDialogue("openai-chat", body); err == nil {
		t.Fatal("Chat白名单退化")
	}
}
