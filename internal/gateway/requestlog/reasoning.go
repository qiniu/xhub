package requestlog

import (
	"encoding/json"
	"strings"
)

// ReasoningEffort 提取客户端明确指定的推理强度；path 为入站路径，body 为原始 JSON，返回短标识或空串。
// rememberExchange 在关闭提示词存储时也调用；仅识别三种文本协议的配置字段，不推断模型默认值、思考预算或响应内容。
// 非法 JSON、错误类型、超长值和非标识文本被忽略，不改变原请求校验、转发或计费，也不保存提示词。
func ReasoningEffort(path string, body []byte) string {
	var container, field string
	switch path = strings.TrimSuffix(path, "/"); {
	case strings.HasSuffix(path, "/chat/completions"):
		field = "reasoning_effort"
	case strings.HasSuffix(path, "/responses"):
		container, field = "reasoning", "effort"
	case strings.HasSuffix(path, "/messages"):
		container, field = "output_config", "effort"
	default:
		return ""
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(body, &values) != nil {
		return ""
	}
	if container != "" {
		var nested map[string]json.RawMessage
		if json.Unmarshal(values[container], &nested) != nil {
			return ""
		}
		values = nested
	}
	var effort string
	if json.Unmarshal(values[field], &effort) != nil || effort == "" || len(effort) > 32 {
		return ""
	}
	// 不硬编码厂商枚举，新增强度仍可记录；限定短 ASCII 标识，避免把任意请求文本带入独立日志字段。
	for _, ch := range effort {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return ""
		}
	}
	return effort
}
