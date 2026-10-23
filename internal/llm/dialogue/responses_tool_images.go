package dialogue

import (
	"fmt"
	"strings"
)

// projectResponsesToolImages 为共享解析器生成工具图片的文本投影，返回原生边界或格式错误。
// 参数 item 是独立投影，调用于工具类型标准化后；Native 保留原图文、顺序和关联 ID。
// 工具图片不能等价转换为纯文本工具输出，调用方必须设置 BridgeError，禁止丢图后跨协议发送。
func projectResponsesToolImages(item map[string]any) (string, error) {
	if item["type"] != "function_call_output" {
		return "", nil
	}
	parts, ok := item["output"].([]any)
	if !ok {
		return "", nil
	}
	hasImage := false
	for _, value := range parts {
		if dialogueMap(value)["type"] == "input_image" {
			hasImage = true
		}
	}
	if !hasImage {
		return "", nil
	}
	if strings.TrimSpace(dialogueString(item["call_id"])) == "" {
		return "", fmt.Errorf("tool result ID is required")
	}
	texts := make([]any, 0, len(parts))
	for _, value := range parts {
		part := dialogueMap(value)
		switch part["type"] {
		case "input_image":
			// 此处复用图片结构校验；user 仅用于校验器，不改写工具输出为用户消息。
			if _, err := parseImagePart(part, "openai-responses", "user"); err != nil {
				return "", err
			}
		case "input_text":
			if err := dialogueFields(part, "type", "text"); err != nil {
				return "", err
			}
			if _, ok := part["text"].(string); !ok {
				return "", fmt.Errorf("tool result content text must be string")
			}
			texts = append(texts, part)
		default:
			return "", fmt.Errorf("unsupported tool result content capability %v", part["type"])
		}
	}
	item["output"] = texts
	return "Responses tool result images require native Responses", nil
}
