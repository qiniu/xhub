package dialogue

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// toolResultImageFixture 构造包含真实PNG字节的合成工具结果图片；返回独立对象供单测修改，无外部访问或清理。
func toolResultImageFixture() map[string]any {
	return map[string]any{"type": "image", "source": map[string]any{
		"type": "base64", "media_type": "image/png",
		"data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aRZkAAAAASUVORK5CYII=",
	}}
}

// toolResultImageRequest 构造Read调用及其用户侧结果；参数为结果块和角色，返回独立请求供解析、原生重放及桥接测试。
// 本地合成调用不访问文件、供应商或数据库，无数据清理需求。
func toolResultImageRequest(block map[string]any, role string) map[string]any {
	return map[string]any{"model": "source", "max_tokens": float64(128), "messages": []any{
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "read_1", "name": "Read", "input": map[string]any{"file_path": "/tmp/synthetic.png"}}}},
		map[string]any{"role": role, "content": []any{block}},
	}}
}

// TestNativeMessagesToolResultImages 验证工具图片仅原生保留，图文多图及错误结果顺序不变；跨协议不能删除图片后外发。
// 参数t为测试上下文；全部图片与调用均为内存合成数据，不调用网络或写入文件，无需清理。
func TestNativeMessagesToolResultImages(t *testing.T) {
	for _, name := range []string{"image_only", "mixed_images", "error_images", "url", "cache_hints"} {
		t.Run(name, func(t *testing.T) {
			image := toolResultImageFixture()
			block := map[string]any{"type": "tool_result", "tool_use_id": "read_1", "content": []any{image}}
			if name == "mixed_images" || name == "error_images" {
				block["content"] = []any{map[string]any{"type": "text", "text": "Before image"}, image, map[string]any{"type": "text", "text": "Between images"}, toolResultImageFixture()}
			}
			if name == "error_images" {
				block["is_error"] = true
			}
			if name == "url" {
				image["source"] = map[string]any{"type": "url", "url": "https://example.test/synthetic.png"}
			}
			if name == "cache_hints" {
				image["cache_control"] = map[string]any{"type": "ephemeral", "ttl": "1h"}
				block["cache_control"] = map[string]any{"type": "ephemeral"}
				block["content"] = []any{image, map[string]any{"type": "text", "text": "Original text", "cache_control": map[string]any{"type": "ephemeral"}}}
			}
			body := toolResultImageRequest(block, "user")
			before, _ := json.Marshal(body)
			d, err := ParseDialogue("anthropic-messages", body)
			if err != nil || !strings.Contains(d.BridgeError, "tool_result images") {
				t.Fatalf("有效工具图片未建立原生边界: %q %v", d.BridgeError, err)
			}
			same, err := EncodeDialogue(d, "anthropic-messages", "target")
			if err != nil || !reflect.DeepEqual(same["messages"], body["messages"]) || same["model"] != "target" {
				t.Fatalf("原生重放丢失图文、顺序或关联ID: %v %v", same, err)
			}
			for _, protocol := range []string{"openai-chat", "openai-responses"} {
				out, err := EncodeDialogue(d, protocol, "target")
				if err == nil || out != nil || !strings.Contains(err.Error(), "tool_result images") {
					t.Errorf("工具图片被删除或伪造用户消息后跨协议外发到%s: %v %v", protocol, out, err)
				}
			}
			after, _ := json.Marshal(body)
			if string(before) != string(after) {
				t.Fatal("解析或编码修改调用方原始工具图片")
			}
		})
	}
}

// TestNativeMessagesToolResultImageValidation 验证含图结果的角色、ID、错误标记、嵌套块及图片结构严格校验，格式错误不能借原生路径放行。
// 参数t为上下文；每项新建内存对象，覆盖正常图片附近的失败边界，不读取真实请求，无需清理。
func TestNativeMessagesToolResultImageValidation(t *testing.T) {
	for _, name := range []string{"assistant", "missing_id", "blank_id", "numeric_id", "bad_error", "null_error", "unknown_result", "unknown_image", "missing_source", "bad_source", "unknown_source", "bad_mime", "bad_base64", "empty_base64", "numeric_base64", "empty_url", "file_url", "credential_url", "invalid_text", "unknown_text", "unknown_block", "scalar_block"} {
		t.Run(name, func(t *testing.T) {
			image := toolResultImageFixture()
			source := image["source"].(map[string]any)
			block := map[string]any{"type": "tool_result", "tool_use_id": "read_1", "content": []any{image}}
			role := "user"
			switch name {
			case "assistant":
				role = "assistant"
			case "missing_id":
				delete(block, "tool_use_id")
			case "blank_id":
				block["tool_use_id"] = " "
			case "numeric_id":
				block["tool_use_id"] = 1
			case "bad_error":
				block["is_error"] = "true"
			case "null_error":
				block["is_error"] = nil
			case "unknown_result":
				block["unexpected"] = true
			case "unknown_image":
				image["unexpected"] = true
			case "missing_source":
				delete(image, "source")
			case "bad_source":
				image["source"] = "invalid"
			case "unknown_source":
				source["unexpected"] = true
			case "bad_mime":
				source["media_type"] = "application/pdf"
			case "bad_base64":
				source["data"] = "not base64"
			case "empty_base64":
				source["data"] = ""
			case "numeric_base64":
				source["data"] = 5
			case "empty_url", "file_url", "credential_url":
				url := map[string]string{"empty_url": "", "file_url": "file:///tmp/image.png", "credential_url": "https://user:pass@example.test/image.png"}[name]
				image["source"] = map[string]any{"type": "url", "url": url}
			case "invalid_text":
				block["content"] = []any{image, map[string]any{"type": "text", "text": 5}}
			case "unknown_text":
				block["content"] = []any{image, map[string]any{"type": "text", "text": "Hello", "unexpected": true}}
			case "unknown_block":
				block["content"] = []any{image, map[string]any{"type": "document"}}
			case "scalar_block":
				block["content"] = []any{image, "invalid"}
			}
			if _, err := ParseDialogue("anthropic-messages", toolResultImageRequest(block, role)); err == nil {
				t.Fatal("畸形工具图片结果被原生解析接受")
			}
		})
	}
}

// TestNativeMessagesTextToolResultStillBridges 验证纯文本、空数组和错误文本不被新的图片边界拦截，原有跨协议工具关联保持有效。
// 参数t为上下文；仅使用本地请求对象，正常及边界内容无需外部数据或清理。
func TestNativeMessagesTextToolResultStillBridges(t *testing.T) {
	for _, content := range []any{"Plain result", []any{}, []any{map[string]any{"type": "text", "text": "Read failed"}}} {
		block := map[string]any{"type": "tool_result", "tool_use_id": "read_1", "content": content, "is_error": true}
		d, err := ParseDialogue("anthropic-messages", toolResultImageRequest(block, "user"))
		if err != nil || d.BridgeError != "" {
			t.Fatalf("纯文本工具结果错误触发图片边界: %v %v", d.BridgeError, err)
		}
		for _, protocol := range []string{"openai-chat", "openai-responses"} {
			out, err := EncodeDialogue(d, protocol, "target")
			raw, _ := json.Marshal(out)
			if err != nil || !strings.Contains(string(raw), "[tool_error] ") || !strings.Contains(string(raw), "read_1") {
				t.Fatalf("纯文本工具错误桥接回归: %s %v", raw, err)
			}
		}
	}
}
