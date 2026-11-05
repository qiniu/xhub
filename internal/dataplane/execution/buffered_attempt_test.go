package execution

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// bufferedProbe 提供可部分失败且可观察关闭的内存响应体，仅用于读取边界单测。
type bufferedProbe struct {
	io.Reader
	closed bool
}

// Close 记录上游响应体关闭，无外部资源或错误。
func (b *bufferedProbe) Close() error { b.closed = true; return nil }

// partialBody 模拟上游输出一部分后读取失败，保留原始字节用于取消和失败诊断。
type partialBody struct{ sent bool }

// Read 在首次返回内容后返回意外 EOF，供 bufferedProbe 使用。
func (p *partialBody) Read(b []byte) (int, error) {
	if !p.sent {
		p.sent = true
		return copy(b, "partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}

// TestReadBufferedAttempt 验证正常、空正文和中途失败都保留状态并关闭响应体。
// 使用内存 reader，不访问上游或数据库；每个响应体由被测函数关闭，无额外清理。
func TestReadBufferedAttempt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reader  io.Reader
		status  int
		body    string
		wantErr error
	}{
		{"ok", strings.NewReader("answer"), 200, "answer", nil},
		{"empty", strings.NewReader(""), 204, "", nil},
		{"truncated", &partialBody{}, 200, "partial", io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &bufferedProbe{Reader: tc.reader}
			got := readBufferedAttempt(&http.Response{StatusCode: tc.status, Body: body})
			if got.status != tc.status || string(got.body) != tc.body || !errors.Is(got.readErr, tc.wantErr) || !body.closed {
				t.Fatalf("响应事实或关闭状态错误: %+v closed=%v", got, body.closed)
			}
		})
	}
}
