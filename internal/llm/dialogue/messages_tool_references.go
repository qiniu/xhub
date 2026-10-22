package dialogue

import (
	"fmt"
	"strings"
)

// projectMessagesToolReferences 从共享工具结果投影中分离原生工具引用。
// 参数 block 是独立的消息副本、role 是外层角色；返回跨协议边界或格式错误。
// ParseDialogue 在图片/错误文本投影前调用；Native 保存完整引用、顺序及扩展字段，
// BridgeError 禁止删掉引用的投影进入其他协议。仅修改副本，不解析或执行被引用工具。
func projectMessagesToolReferences(block map[string]any, role string) (string, error) {
	if block["type"] != "tool_result" {
		return "", nil
	}
	parts, ok := block["content"].([]any)
	if !ok {
		return "", nil
	}
	found := false
	projected := make([]any, 0, len(parts))
	for _, value := range parts {
		part := dialogueMap(value)
		if part["type"] != "tool_reference" {
			projected = append(projected, value)
			continue
		}
		name, valid := part["tool_name"].(string)
		if !valid || strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("tool_reference tool_name must be a nonempty string")
		}
		found = true
	}
	if !found {
		return "", nil
	}
	if role != "user" {
		return "", fmt.Errorf("Messages tool_result references require user role")
	}
	id, valid := block["tool_use_id"].(string)
	if !valid || strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("tool result ID is required")
	}
	if value, exists := block["is_error"]; exists {
		if _, valid := value.(bool); !valid {
			return "", fmt.Errorf("tool result is_error must be boolean")
		}
	}
	block["content"] = projected
	return "Messages tool references require native Messages", nil
}
