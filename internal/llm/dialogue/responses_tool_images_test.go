package dialogue

import (
	"reflect"
	"testing"
)

// TestResponsesToolImages 验证原生图文重放、只含图片、自定义工具、文件引用及非法输入；合成内存数据无需清理。
// 参数 t 为上下文；正常请求不得修改原对象或丢图桥接，非法图片、文本及关联 ID 必须拒绝。
func TestResponsesToolImages(t *testing.T) {
	for _, name := range []string{"mixed", "image_only", "custom", "file", "plain", "bad_url", "bad_text", "missing_id", "unknown", "bad_detail"} {
		t.Run(name, func(t *testing.T) {
			image := map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AQID"}
			item := map[string]any{"type": "function_call_output", "call_id": "read_1", "output": []any{map[string]any{"type": "input_text", "text": "Image:"}, image}}
			bad := false
			switch name {
			case "image_only":
				item["output"] = []any{image}
			case "custom":
				item["type"] = "custom_tool_call_output"
			case "file":
				delete(image, "image_url")
				image["file_id"] = "file_fixture"
			case "plain":
				item["output"] = "Plain result"
			case "bad_url":
				image["image_url"] = "file:///tmp/private"
				bad = true
			case "bad_text":
				item["output"].([]any)[0].(map[string]any)["text"] = 3
				bad = true
			case "missing_id":
				item["call_id"] = " "
				bad = true
			case "unknown":
				image["extra"] = true
				bad = true
			case "bad_detail":
				image["detail"] = "invalid"
				bad = true
			}
			body := map[string]any{"model": "source", "input": []any{map[string]any{"type": "function_call", "call_id": "read_1", "name": "read", "arguments": "{}"}, item}}
			before, _ := cloneObject(body)
			d, err := ParseDialogue("openai-responses", body)
			if bad {
				if err == nil {
					t.Fatal("非法工具图片未拒绝")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			out, err := EncodeDialogue(d, "openai-responses", "target")
			if err != nil || !reflect.DeepEqual(out["input"], body["input"]) || !reflect.DeepEqual(before, body) {
				t.Fatalf("原生工具图文被修改: %v", err)
			}
			if name != "plain" {
				for _, protocol := range []string{"openai-chat", "anthropic-messages"} {
					if _, err := EncodeDialogue(d, protocol, "target"); err == nil {
						t.Fatal("跨协议静默丢图")
					}
				}
			}
		})
	}
}
