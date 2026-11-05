package stream

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// failingWriter 模拟客户端写入失败或短写；仅用于验证转发边界，不产生网络资源。
type failingWriter struct {
	header http.Header
	calls  int
	short  bool
	err    error
}

// Header 返回本次独立测试响应头；不读取或写入外部状态。
func (w *failingWriter) Header() http.Header { return w.header }

// WriteHeader 接受响应状态，模拟 HTTP 已开始发送。
func (w *failingWriter) WriteHeader(int) {}

// Write 返回合成写入错误或短写，供调用方验证错误传播。
func (w *failingWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.short {
		return len(p) - 1, nil
	}
	return 0, w.err
}

// closeProbe 记录上游响应是否关闭；仅在本测试中使用。
type closeProbe struct {
	io.Reader
	closed bool
}

// Close 标记上游响应已释放，不访问外部资源。
func (r *closeProbe) Close() error { r.closed = true; return nil }

// TestPipeStreamReportsClientWriteFailure 验证客户端写入失败和短写不能被 EOF 伪装成成功。
// 前置内存上游与失败写入器；要求报告失败、禁止上层误以为尚未写响应并关闭上游，无外部数据需清理。
func TestPipeStreamReportsClientWriteFailure(t *testing.T) {
	broken := errors.New("client write failed")
	for _, tc := range []struct {
		name        string
		short       bool
		cause, want error
	}{
		{"write_error", false, broken, broken},
		{"short_write", true, nil, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &closeProbe{Reader: strings.NewReader("data: hello\n\n")}
			w := &failingWriter{header: make(http.Header), short: tc.short, err: tc.cause}
			started, _, _, _, err := PipeStream(w, &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, time.Now())
			if !started || !errors.Is(err, tc.want) || !errors.Is(err, ErrClientWrite) || !body.closed || w.calls != 1 {
				t.Fatalf("客户端写失败未正确报告：started=%v err=%v closed=%v calls=%d", started, err, body.closed, w.calls)
			}
		})
	}
}
