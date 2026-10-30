package diagnostics

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestObserveFailure 验证正常、取消、超时、超限和截断证据不混淆，错误脱敏且有界；纯内存，取消上下文随例清理。
func TestObserveFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		kind   string
		status int
	}{
		{"none", nil, "transport_error", 400}, {"limit", &http.MaxBytesError{Limit: 64 << 20}, "body_too_large", 413},
		{"canceled", context.Canceled, "request_canceled", 499}, {"deadline", context.DeadlineExceeded, "timeout", 408},
		{"socket_timeout", &net.DNSError{IsTimeout: true}, "timeout", 408}, {"truncated", io.ErrUnexpectedEOF, "unexpected_eof", 400},
		{"wrapped", errors.Join(errors.New("outer"), io.ErrUnexpectedEOF), "unexpected_eof", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := ObserveFailure(context.Background(), "request_body", tc.err, 125*time.Millisecond)
			status, _, _ := BodyFailureStatus(f)
			if f.Kind != tc.kind || status != tc.status || f.ElapsedMS != 125 {
				t.Fatalf("错误分类或计时丢失: %+v status=%d", f, status)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := ObserveFailure(ctx, "upstream_request", errors.New("POST https://user:password@vendor.test/private?key=secret Bearer hidden sk-private"), time.Second)
	if f.RequestContextError != "context canceled" || f.CancellationSource != "unknown" {
		t.Fatal("取消来源不应被推断", f)
	}
	for _, secret := range []string{"password", "hidden", "private", "secret"} {
		if strings.Contains(f.TransportError, secret) {
			t.Fatal("诊断泄漏凭据", f)
		}
	}
	if len(ObserveFailure(nil, "response_transfer", errors.New(strings.Repeat("x", 2048)), 0).TransportError) > 1024 {
		t.Fatal("错误文本无界")
	}
}

// failureSink 仅模拟请求级记录器，验证诊断不通过公开HTTP头输出。
type failureSink struct {
	http.ResponseWriter
	saved *UpstreamDiagnostics
}

// SetFailureDiagnostics 保存测试对象；参数为诊断，无返回，无持久化副作用。
func (s *failureSink) SetFailureDiagnostics(d *UpstreamDiagnostics) { s.saved = d }

// failureWrapper 模拟Gin和缓冲写入器，底层由测试创建，无网络资源。
type failureWrapper struct{ http.ResponseWriter }

// Unwrap 返回底层记录器，供诊断方法遍历，不写响应。
func (w failureWrapper) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// TestRecordEarlyFailure 验证嵌套记录器成功接收、普通写入器与nil安全失败，响应头和正文保持空；纯内存无需清理。
func TestRecordEarlyFailure(t *testing.T) {
	plain := httptest.NewRecorder()
	sink := &failureSink{ResponseWriter: plain}
	d := &UpstreamDiagnostics{RateLimit: map[string]any{"rejected": "key rpm_limit"}}
	if !RecordEarlyFailure(failureWrapper{failureWrapper{sink}}, d) || sink.saved != d {
		t.Fatal("嵌套响应记录器丢失诊断")
	}
	if RecordEarlyFailure(nil, d) || RecordEarlyFailure(plain, d) {
		t.Fatal("没有记录器时不应宣称已保存")
	}
	if len(plain.Header()) != 0 || plain.Body.Len() != 0 {
		t.Fatal("内部诊断泄漏到客户端")
	}
}
