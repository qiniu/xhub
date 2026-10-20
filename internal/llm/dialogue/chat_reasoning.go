package dialogue

import (
	"fmt"
	"strings"
)

// ChatReasoningItems 将已解析 Responses 的推理投影为 LiteLLM 风格的 Chat reasoning_items 扩展。
// 参数 result 为上游结果，返回独立的推理数组或字段错误；同步编码和流式终态共用，不修改原结果。
// 公开摘要仍由 reasoning_content 展示；密文只留在结构化扩展中，客户端可原样回传，也可按普通 Chat 忽略它。
func ChatReasoningItems(result DialogueResult) ([]any, error) {
	if result.Source != "openai-responses" {
		return nil, nil
	}
	var items []any
	for _, value := range dialogueList(result.Native["output"]) {
		item := dialogueMap(value)
		if item["type"] != "reasoning" {
			continue
		}
		copy, err := cloneObject(item)
		if err != nil {
			return nil, err
		}
		// 与 LiteLLM 的扩展形状保持一致，不把供应商新增的私有输出字段当成可重放输入。
		projected := map[string]any{"type": "reasoning", "id": copy["id"], "summary": []any{}}
		if summary, exists := copy["summary"]; exists {
			projected["summary"] = summary
		}
		if cipher, exists := copy["encrypted_content"]; exists {
			projected["encrypted_content"] = cipher
		}
		if _, err := chatReasoningReplay([]any{projected}, "assistant"); err != nil {
			return nil, err
		}
		items = append(items, projected)
	}
	return items, nil
}

// chatReasoningReplay 解析 Chat assistant 上回传的 reasoning_items，供共享解析器恢复 Responses 历史。
// 参数 value 为可选扩展、role 为消息角色，返回独立公开/原生 Turn 或格式错误；空扩展不影响普通 Chat。
// 经严格验证的公开摘要转为可迁移推理；含 encrypted_content 字段的项即使为空也保留原生约束。
// 不解密、不拼接正文；原生 Turn 阻止跨协议发送，连接归属另由路由层验证。
func chatReasoningReplay(value any, role string) ([]Turn, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("reasoning_items must be an array")
	}
	if len(items) > 0 && role != "assistant" {
		return nil, fmt.Errorf("reasoning_items requires assistant role")
	}
	var turns []Turn
	for _, value := range items {
		item := dialogueMap(value)
		if err := dialogueFields(item, "type", "id", "summary", "encrypted_content", "status"); err != nil {
			return nil, err
		}
		if item["type"] != "reasoning" || dialogueString(item["id"]) == "" {
			return nil, fmt.Errorf("reasoning_items requires reasoning type and ID")
		}
		text, opaque, err := chatReasoningSummary(item)
		if err != nil {
			return nil, err
		}
		if !opaque {
			// 公开摘要没有可跨供应商复用的原厂 ID；只保留文本语义，编码器按目标协议重建。
			// 即使摘要为空也返回占位推理轮，让调用方取消重复的 reasoning_content 投影。
			turns = append(turns, Turn{Role: "reasoning", Reasoning: text})
			continue
		}
		copy, err := cloneObject(item)
		if err != nil {
			return nil, err
		}
		// status 是输出生命周期字段；续轮使用原厂 ID、摘要和密文，不能伪造新的 reasoning ID。
		delete(copy, "status")
		turns = append(turns, Turn{Role: "reasoning", NativeProtocol: "openai-responses", Native: copy})
	}
	return turns, nil
}

// ChatReasoningRequiresNativeOwnership 供路由层判断 Chat 推理扩展能否跨连接迁移。
// 参数为客户端字段及消息角色，返回是否须验证原连接；只有完整通过解析且不含原生项才返回 false。
// 非法嵌套字段、未知签名和任何密文字段均不能通过普通选路绕过验证；纯内存解析，不修改输入。
func ChatReasoningRequiresNativeOwnership(value any, role string) bool {
	turns, err := chatReasoningReplay(value, role)
	if err != nil {
		return true
	}
	for _, turn := range turns {
		if turn.Native != nil {
			return true
		}
	}
	return false
}

// chatReasoningSummary 严格读取单个已校验顶层字段的 Chat reasoning 项，供重放及路由分类共用。
// 返回公开摘要文本、是否存在密文字段及格式错误；未知嵌套字段不允许伪装成可迁移摘要。
// 无副作用；生命周期状态只接受 Responses 定义的字符串，空或 null 密文字段仍保留原生限制。
func chatReasoningSummary(item map[string]any) (string, bool, error) {
	if status, exists := item["status"]; exists {
		switch status {
		case "in_progress", "completed", "incomplete":
		default:
			return "", false, fmt.Errorf("invalid reasoning status")
		}
	}
	_, opaque := item["encrypted_content"]
	if _, err := MessagesReasoningBlock(item); err != nil {
		return "", opaque, err
	}
	var parts []string
	for _, value := range dialogueList(item["summary"]) {
		part := dialogueMap(value)
		if err := dialogueFields(part, "type", "text"); err != nil {
			return "", opaque, err
		}
		parts = append(parts, part["text"].(string))
	}
	return strings.Join(parts, "\n\n"), opaque, nil
}
