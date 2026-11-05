package requestlog

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FailureSummary 从失败正文中保留有限长度的错误线索，只保留明确允许的诊断字段。
// raw 为上游或网关错误；返回 JSON 或短文本。纯生成流不存入错误摘要；错误信息仍可能包含上游回显。
// 供日志持久化使用，不更改发送给客户端的原始响应，也不改变上游诊断状态。
func FailureSummary(raw string) string {
	if raw == "" {
		return ""
	}
	// 失败响应也可能是异常巨大或无效正文；限制解析工作，避免诊断拖垮正常请求。
	if len(raw) > 1<<20 {
		return "upstream error details exceed the summary limit"
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		if object, ok := value.(map[string]any); ok {
			result := map[string]any{}
			for _, key := range []string{"error", "message", "code", "type", "param", "request_id", "event_id", "response_id", "diagnostics", "upstream_error", "status"} {
				if v, ok := object[key]; ok {
					result[key] = safeErrorValue(v, 0)
				}
			}
			if len(result) == 0 {
				return "upstream error details unavailable"
			}
			encoded, err := json.Marshal(result)
			if err == nil && len(encoded) <= 16<<10 {
				return string(encoded)
			}
			// 有界摘要仍保留常见错误原因，避免供应商扩展对象无限撑大数据库。
			fallback := map[string]any{"truncated": true}
			if details, ok := result["error"].(map[string]any); ok {
				brief := map[string]any{}
				for _, key := range []string{"code", "type", "message"} {
					if v, ok := details[key]; ok {
						s := []rune(shortErrorText(fmt.Sprint(v)))
						if len(s) > 512 {
							s = s[:512]
						}
						brief[key] = string(s)
					}
				}
				fallback["error"] = brief
			}
			encoded, _ = json.Marshal(fallback)
			return string(encoded)
		}
		return "upstream error details unavailable"
	}
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "data:") {
			return "upstream stream failed; details unavailable"
		}
	}
	return shortErrorText(raw)
}

// safeErrorValue 递归保留明确允许的诊断字段并限制深度和数组大小；value 是错误对象的字段值。
// 返回仅供持久化的副本，不修改上游对象；未知扩展字段不保存，消息仍可能包含上游回显。
func safeErrorValue(value any, depth int) any {
	if depth > 5 {
		return "[omitted]"
	}
	switch v := value.(type) {
	case string:
		return shortErrorText(v)
	case map[string]any:
		out := make(map[string]any, min(len(v), 32))
		// 不枚举未知供应商字段，避免 payload、data 等扩展旁路保存用户正文。
		for _, name := range []string{"error", "code", "type", "message", "param", "request_id", "event_id", "response_id", "upstream_request_id", "status", "status_code", "details", "diagnostics", "upstream_error", "phase", "kind", "retryable", "terminal_cause", "bytes_read", "body_limit", "request_body_omitted"} {
			if item, ok := v[name]; ok {
				out[name] = safeErrorValue(item, depth+1)
			}
		}
		return out
	case []any:
		if len(v) > 20 {
			v = v[:20]
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = safeErrorValue(item, depth+1)
		}
		return out
	default:
		return v
	}
}

// shortErrorText 限制错误文本大小并掩盖常见密钥格式；供错误摘要生成，不能替代访问控制。
// 参数 s 为原文本；过长时保留首尾共 4096 个 Unicode 字符并标识截断，方便保留末尾原因。
func shortErrorText(s string) string {
	runes := []rune(redactSecretText(s))
	if len(runes) > 4096 {
		return string(runes[:3072]) + "… [truncated] …" + string(runes[len(runes)-1024:])
	}
	return string(runes)
}
