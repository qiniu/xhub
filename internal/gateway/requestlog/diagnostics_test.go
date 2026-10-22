package requestlog

import (
	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
	"strings"
	"testing"
)

// TestFailureJSONPreservesTerminalError 验证超过正文捕获上限后的失败仍独立保留，正常/无诊断/非法快照回退原文。
// 参数 t 为上下文；内存夹具无外部状态，核对原 HTTP 错误不被改写、对象不被修改，无需清理。
func TestFailureJSONPreservesTerminalError(t *testing.T) {
	fallback := strings.Repeat("partial", 400000)
	detail := map[string]any{"error": map[string]any{"message": "terminal vendor cause", "code": "vendor_busy"}, "request_id": "req-original"}
	for _, tc := range []struct {
		name     string
		diag     *dpdiagnostics.UpstreamDiagnostics
		original bool
	}{
		{"missing", nil, true},
		{"success", &dpdiagnostics.UpstreamDiagnostics{StatusCode: 200}, true},
		{"http_failure", &dpdiagnostics.UpstreamDiagnostics{StatusCode: 503, Error: detail}, true},
		{"stream_failure", &dpdiagnostics.UpstreamDiagnostics{StatusCode: 200, Error: detail}, false},
		{"invalid", &dpdiagnostics.UpstreamDiagnostics{StatusCode: 200, Error: map[string]any{"bad": func() {}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := FailureJSON(tc.diag, fallback)
			if tc.original {
				if got != fallback {
					t.Fatal("错误修改回退正文")
				}
			} else if !strings.Contains(got, "terminal vendor cause") || !strings.Contains(got, "req-original") {
				t.Fatalf("尾部错误丢失: %s", got)
			}
		})
	}
}
