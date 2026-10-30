package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// failingRequestBody 模拟入站网络读取错误，不携带用户正文；供 handler 分类测试使用。
type failingRequestBody struct{ err error }

// Read 返回预设错误和零字节，验证异常路由；不修改缓冲且无外部副作用。
func (b failingRequestBody) Read([]byte) (int, error) { return 0, b.err }

// Close 释放测试读取器；无资源，返回 nil。
func (b failingRequestBody) Close() error { return nil }

// TestBodyReadFailureStatus 从真实 handler 入口验证超限、超时、取消和截断不能混为413；无数据库或外部数据。
func TestBodyReadFailureStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"too_large", &http.MaxBytesError{Limit: 64 << 20}, 413},
		{"timeout", context.DeadlineExceeded, 408},
		{"canceled", context.Canceled, 499},
		{"truncated", io.ErrUnexpectedEOF, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{engine: newEngine(), registered: map[string]struct{}{}, idem: map[string]*idemRec{}}
			r := httptest.NewRequest("POST", "/health/liveliness", nil)
			r.Body = failingRequestBody{tc.err}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("读取错误分类错误: got=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
