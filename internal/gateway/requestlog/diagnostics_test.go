package requestlog

import (
	"encoding/json"
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

// TestCancellationFailureJSON 保留独立上游错误且没有上游证据时明确为空，不再把部分生成内容当成错误；纯内存无清理。
func TestCancellationFailureJSON(t *testing.T) {
	for _, observed := range []bool{false, true} {
		d := &dpdiagnostics.UpstreamDiagnostics{StatusCode: 200, Failure: &dpdiagnostics.Failure{Kind: "request_canceled", Phase: "response_transfer", CancellationSource: "unknown"}}
		if observed {
			d.Error = map[string]any{"error": map[string]any{"code": "vendor_overloaded"}, "request_id": "vendor-trace"}
		}
		raw := FailureJSON(d, "data: partial generated content")
		var got map[string]any
		if json.Unmarshal([]byte(raw), &got) != nil || got["diagnostics"] == nil || (got["upstream_error"] != nil) != observed {
			t.Fatalf("取消证据丢失: %s", raw)
		}
	}
}

// TestClientWriteFailureJSON 验证客户端写失败单独记录，不把部分模型输出冒充错误或编造上游原因。
// 内存构造有无上游错误两种状态；只验证结构和原文保护，没有外部资源需要清理。
func TestClientWriteFailureJSON(t *testing.T) {
	for _, observed := range []bool{false, true} {
		d := &dpdiagnostics.UpstreamDiagnostics{StatusCode: 200, Failure: &dpdiagnostics.Failure{Kind: "client_write_error", Phase: "client_write"}}
		if observed {
			d.Error = map[string]any{"error": map[string]any{"code": "vendor_overloaded"}}
		}
		raw := FailureJSON(d, "data: partial generated content")
		var got map[string]any
		if json.Unmarshal([]byte(raw), &got) != nil || got["diagnostics"] == nil || (got["upstream_error"] != nil) != observed || strings.Contains(raw, "partial generated content") {
			t.Fatalf("客户端写失败证据错误: %s", raw)
		}
		if errObj, ok := got["error"].(map[string]any); !ok || errObj["type"] != "client_write_error" {
			t.Fatalf("客户端写失败类型丢失: %s", raw)
		}
	}
}
