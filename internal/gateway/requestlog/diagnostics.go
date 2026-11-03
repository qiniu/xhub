package requestlog

import (
	"encoding/json"
	"net/http"

	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
)

// DiagnosticJSON 将传输事实和计费统计合并为持久化 JSON。
// 参数 upstream 为原始诊断，可为 nil；usage 为实际计费输入；返回 JSON 或空字符串。
// 调用：recordSpend；复制响应头后打码凭据，不修改原始对象，不保存生成内容。
func DiagnosticJSON(upstream *dpdiagnostics.UpstreamDiagnostics, usage map[string]any) string {
	if upstream == nil {
		return ""
	}
	saved := *upstream
	saved.Headers = diagnosticHeaders(upstream.Headers)
	saved.Trailers = diagnosticHeaders(upstream.Trailers)
	saved.BillingUsage = usage
	raw, err := json.Marshal(saved)
	if err != nil {
		return ""
	}
	return string(raw)
}

// diagnosticHeaders 克隆并脱敏完整多值响应头；参数 headers 为上游头，返回独立副本。
// 调用：诊断持久化；保留全部名称、值数量与非敏感值，密钥和 Cookie 替换为星号。
func diagnosticHeaders(headers http.Header) http.Header {
	result := headers.Clone()
	for key, values := range result {
		for i, value := range values {
			if sensitiveHeader(key) {
				values[i] = "***"
			} else {
				values[i] = redactSecretText(value)
			}
		}
	}
	return result
}

// FailureJSON 优先返回 HTTP 成功后流内明确失败的独立错误快照，避免 2 MiB 正文捕获上限吞掉尾部错误。
// 参数 upstream 为观测事实、fallback 为原失败正文；recordSpend 调用，返回日志错误详情；不改客户端或响应正文。
// 非流内供应商错误或序列化失败沿用原文，不能用 HTTP 成功握手替换最终失败状态。
func FailureJSON(upstream *dpdiagnostics.UpstreamDiagnostics, fallback string) string {
	// 取消不能把整个部分 SSE 当作错误原因；保留独立供应商错误，未观测到时显式为 null。
	if upstream != nil && upstream.Failure != nil && upstream.Failure.Kind == "request_canceled" {
		raw, err := json.Marshal(map[string]any{"error": map[string]any{"type": "request_canceled", "message": "request connection canceled; initiator unknown"}, "diagnostics": upstream.Failure, "upstream_error": upstream.Error})
		if err == nil {
			return string(raw)
		}
	}
	if upstream == nil || upstream.StatusCode >= 400 || upstream.Error == nil {
		return fallback
	}
	raw, err := json.Marshal(upstream.Error)
	if err != nil {
		return fallback
	}
	return string(raw)
}
