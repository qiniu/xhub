package diagnostics

import (
	"encoding/json"
	"errors"
)

// UpstreamError 保存供应商明确返回的错误对象及关联 ID，不把 HTTP 200 的流内错误误当作成功。
// 由 ExtractUpstreamError 创建，协议适配器和执行器消费；Envelope 只包含错误及关联信息，不包含生成正文。
type UpstreamError struct{ Envelope map[string]any }

// Error 返回完整 JSON 错误描述，供执行器记录和非结构化错误调用方使用；不修改对象。
// 接收者由 ExtractUpstreamError 构造，序列化失败时返回固定诊断，不能伪装成成功。
func (e *UpstreamError) Error() string {
	raw, err := json.Marshal(e.Envelope)
	if err != nil {
		return "invalid upstream error payload"
	}
	return string(raw)
}

// ExtractUpstreamError 从已解码供应商 JSON/SSE 中提取顶层或 Responses 内嵌错误。
// 参数 doc 为原始事件，正常事件返回 nil；流校验器和诊断观察器调用，不修改输入。
// 保留错误原值、扩展字段、request_id 和 response ID；失败事件缺少错误时明确标记诊断缺失。
func ExtractUpstreamError(doc map[string]any) *UpstreamError {
	value := doc["error"]
	nested, _ := doc["response"].(map[string]any)
	if value == nil {
		value = nested["error"]
	}
	typ, _ := doc["type"].(string)
	if value == nil && typ != "error" && typ != "response.failed" {
		return nil
	}
	if value == nil {
		// 部分 Responses error 事件直接携带 code/message，不应被通用文案覆盖。
		value = map[string]any{"message": "upstream failure event without error details"}
		fields := value.(map[string]any)
		for _, key := range []string{"code", "message", "param"} {
			if v, ok := doc[key]; ok {
				fields[key] = v
			}
		}
	}
	envelope := map[string]any{"error": value}
	for _, key := range []string{"request_id", "event_id"} {
		if v, ok := doc[key]; ok {
			envelope[key] = v
		} else if v, ok := nested[key]; ok {
			envelope[key] = v
		}
	}
	if id, ok := nested["id"]; ok {
		envelope["response_id"] = id
	}
	return &UpstreamError{Envelope: envelope}
}

// ErrorEnvelope 为客户端失败事件提供结构化错误，保留供应商字段；参数 err 为失败原因。
// 原生和跨协议流调用；普通网络/解析错误使用安全文字，nil 返回 nil；返回独立顶层映射。
func ErrorEnvelope(err error) map[string]any {
	if err == nil {
		return nil
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		out := make(map[string]any, len(upstream.Envelope))
		for key, value := range upstream.Envelope {
			out[key] = value
		}
		return out
	}
	return map[string]any{"error": map[string]any{"type": "upstream_error", "code": "upstream_error", "message": SafeErr(err)}}
}
