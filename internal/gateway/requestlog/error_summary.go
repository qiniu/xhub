package requestlog

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FailureSummary 从失败正文中保留有限长度的错误线索，移除常见的请求、生成内容和工具字段。
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
					result[key] = safeErrorValue(key, v, 0)
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
	if strings.Contains(raw, "data:") {
		return "upstream stream failed; details unavailable"
	}
	return shortErrorText(raw)
}

// safeErrorValue 递归去除常见内容字段并限制深度和数组大小；key、value 是错误对象的字段和值。
// 返回仅供持久化的副本，不修改上游对象；保留错误码、追踪 ID 与有限长度的消息。
func safeErrorValue(key string, value any, depth int) any {
	switch strings.ToLower(key) {
	case "content", "messages", "input", "output", "prompt", "request", "response", "choices", "tools", "body", "request_body", "response_body":
		return "[omitted]"
	}
	if depth > 5 {
		return "[omitted]"
	}
	switch v := value.(type) {
	case string:
		return shortErrorText(v)
	case map[string]any:
		out := make(map[string]any, min(len(v), 32))
		for _, name := range []string{"code", "type", "message", "param", "request_id", "event_id", "response_id"} {
			if item, ok := v[name]; ok {
				out[name] = safeErrorValue(name, item, depth+1)
			}
		}
		for k, item := range v {
			if _, exists := out[k]; exists {
				continue
			}
			if len(out) >= 32 {
				out["_truncated"] = true
				break
			}
			out[k] = safeErrorValue(k, item, depth+1)
		}
		return out
	case []any:
		if len(v) > 20 {
			v = v[:20]
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = safeErrorValue("", item, depth+1)
		}
		return out
	default:
		return v
	}
}

// shortErrorText 限制错误文本大小并掩盖常见密钥格式；供错误摘要生成，不能替代访问控制。
// 参数 s 为原文本；返回最多 4096 个 Unicode 字符，截断后追加标识。
func shortErrorText(s string) string {
	runes := []rune(redactSecretText(s))
	if len(runes) > 4096 {
		return string(runes[:4096]) + "… [truncated]"
	}
	return string(runes)
}
