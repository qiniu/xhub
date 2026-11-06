package gateway

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// browserWriteFailure 只用于隔离浏览器测试，转发响应头但模拟客户端正文写入失败。
type browserWriteFailure struct{ http.ResponseWriter }

// browserFlushFailure 在隔离浏览器测试中只模拟连接刷新失败，不更改请求或响应正文。
type browserFlushFailure struct{ http.ResponseWriter }

// FlushError 返回可控连接错误，供浏览器端验证小响应刷新失败后的零费用日志。
func (w browserFlushFailure) FlushError() error { return io.ErrClosedPipe }

// Write 模拟连接在写正文时断开；参数为未持久化的响应字节，返回零字节和标准连接错误。
// 浏览器夹具包裹真实 HTTP 输出调用，不读取、复制或记录响应内容。
func (w browserWriteFailure) Write(_ []byte) (int, error) {
	return 0, &net.OpError{Op: "write", Net: "tcp", Source: &net.TCPAddr{IP: net.ParseIP("10.20.30.40"), Port: 1234}, Addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.123"), Port: 5678}, Err: io.ErrClosedPipe}
}

// TestBrowserWriteFailureFixture 验证隔离夹具只转发响应头并报告正文写入失败，不写入用户数据。
// 使用本地 ResponseRecorder；测试无需数据库、网络或额外清理。
func TestBrowserWriteFailureFixture(t *testing.T) {
	base := httptest.NewRecorder()
	w := browserWriteFailure{ResponseWriter: base}
	w.Header().Set("X-Test", "value")
	w.WriteHeader(http.StatusOK)
	if n, err := w.Write([]byte("fixture")); n != 0 || !errors.Is(err, io.ErrClosedPipe) || base.Body.Len() != 0 || base.Header().Get("X-Test") != "value" {
		t.Fatal("故障夹具未正确模拟客户端写失败")
	}
}
