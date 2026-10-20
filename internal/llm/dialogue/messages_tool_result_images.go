package dialogue

import (
	"fmt"
	"strings"
)

// projectMessagesToolResultImages 校验 Messages 工具结果中的图片，并只为共享解析器生成文本投影。
// 参数 block 必须是 ParseDialogue 创建的独立投影，role 为外层消息角色；返回跨协议能力边界或格式错误。
// 原生正文不受修改：图片、图文顺序、缓存提示及错误标记均由 Native 重放；共享工具结果不能等价表达图片，
// 因此含图时必须设置 BridgeError 阻断跨协议编码，不能将其挪为用户消息或把删除图片后的投影外发。
func projectMessagesToolResultImages(block map[string]any, role string) (string, error) {
	if block["type"] != "tool_result" {
		return "", nil
	}
	parts, ok := block["content"].([]any)
	if !ok {
		return "", nil
	}
	hasImage := false
	for _, value := range parts {
		if dialogueMap(value)["type"] == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		return "", nil
	}
	if role != "user" {
		return "", fmt.Errorf("Messages tool_result images require user role")
	}
	if err := dialogueFields(block, "type", "tool_use_id", "content", "is_error"); err != nil {
		return "", err
	}
	id, ok := block["tool_use_id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("tool result ID is required")
	}
	if value, exists := block["is_error"]; exists {
		if _, ok := value.(bool); !ok {
			return "", fmt.Errorf("tool result is_error must be boolean")
		}
	}
	texts := make([]any, 0, len(parts))
	for _, value := range parts {
		part := dialogueMap(value)
		switch part["type"] {
		case "image":
			// 缓存提示独立于图片内容，仍由原生请求及预算准入检查；图片本体复用统一结构校验，
			// 不下载URL、不伪造图片角色或消息，传入user仅因外层Messages工具结果实际属于user。
			image := make(map[string]any, len(part))
			for key, value := range part {
				if key != "cache_control" {
					image[key] = value
				}
			}
			if _, err := parseImagePart(image, "anthropic-messages", role); err != nil {
				return "", err
			}
		case "text":
			if err := dialogueFields(part, "type", "text", "cache_control"); err != nil {
				return "", err
			}
			text, ok := part["text"].(string)
			if !ok {
				return "", fmt.Errorf("tool result content text must be string")
			}
			texts = append(texts, map[string]any{"type": "text", "text": text})
		default:
			return "", fmt.Errorf("unsupported tool result content capability %v", part["type"])
		}
	}
	block["content"] = texts
	return "Messages tool_result images require native Messages", nil
}
