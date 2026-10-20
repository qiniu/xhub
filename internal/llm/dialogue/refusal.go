package dialogue

import (
	"fmt"
	"strings"
)

// MessagesRefusalExplanation 校验 Messages 拒绝的 stop_details 并返回上游公开说明。
// 参数为 stop_reason=refusal 时的原始详情；ParseDialogueResult 和流转换调用，返回说明或字段类型错误。
// 官方允许详情缺失、category/explanation 为 null；此时返回空串，原生可保留，跨协议编码必须明确拒绝。
// 不根据 category 编造说明，不修改原始对象，也不推断拒绝是否收费。
func MessagesRefusalExplanation(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	details, ok := value.(map[string]any)
	if !ok || details["type"] != "refusal" {
		return "", fmt.Errorf("invalid upstream refusal stop_details")
	}
	for _, field := range []string{"category", "explanation"} {
		if details[field] != nil {
			if _, ok := details[field].(string); !ok {
				return "", fmt.Errorf("invalid upstream refusal %s", field)
			}
		}
	}
	explanation, _ := details["explanation"].(string)
	return explanation, nil
}

// projectRefusalHistory 将单一公开拒绝投影为 assistant 文本，供用户在拒绝后继续提出新问题。
// 参数为入口协议和可修改消息副本；ParseDialogue 调用，返回跨协议边界原因或非法结构错误，同协议 Native 原文保持不变。
// 单一公开说明可以桥接，合法混合/空拒绝仅保留原生；其他角色、非法类型或未知块字段明确报错。
// 这里保留真实公开说明作为历史文本，不把拒绝当密文绑定连接，也不为缺失说明编造内容。
func projectRefusalHistory(protocol string, message map[string]any) (string, error) {
	bridgeError := ""
	if value, exists := message["refusal"]; exists && value != nil {
		text, ok := value.(string)
		if protocol != "openai-chat" || message["role"] != "assistant" || !ok {
			return "", fmt.Errorf("invalid refusal history: expected a public assistant refusal string")
		}
		if strings.TrimSpace(text) == "" || (message["content"] != nil && message["content"] != "") || len(dialogueList(message["tool_calls"])) > 0 {
			bridgeError = "mixed or empty refusal history requires native protocol"
		} else {
			message["content"] = text
		}
		delete(message, "refusal")
	}
	parts, _ := message["content"].([]any)
	projected := []any{}
	hasRefusal := false
	for _, value := range parts {
		block := dialogueMap(value)
		if block["type"] != "refusal" {
			projected = append(projected, value)
			continue
		}
		hasRefusal = true
		text, ok := block["refusal"].(string)
		if protocol != "openai-responses" || message["role"] != "assistant" || !ok {
			return "", fmt.Errorf("invalid refusal history: expected a public assistant refusal string")
		}
		if err := dialogueFields(block, "type", "refusal"); err != nil {
			return "", err
		}
		if len(parts) != 1 || strings.TrimSpace(text) == "" {
			// 合法但未实现的混合响应只能在 Native 中保留；共享投影只留普通块，不能让混合拒绝阻断原生重放。
			bridgeError = "mixed or empty refusal history requires native protocol"
		} else {
			projected = append(projected, map[string]any{"type": "output_text", "text": text, "annotations": []any{}})
		}
	}
	if hasRefusal {
		message["content"] = projected
	}
	return bridgeError, nil
}
