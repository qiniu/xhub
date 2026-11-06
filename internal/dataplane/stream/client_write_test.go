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

// TestWriteClient 验证写入成功、短写和底层错误保持长度与错误分类。
// 仅使用内存写入器；不依赖网络或外部状态，无需清理。
func TestWriteClient(t *testing.T) {
	broken := errors.New("write failed")
	for _, tc := range []struct {
		name    string
		writer  io.Writer
		want    error
		written int
	}{
		{"success", &strings.Builder{}, nil, 3},
		{"short", &failingWriter{header: make(http.Header), short: true}, io.ErrShortWrite, 2},
		{"error", &failingWriter{header: make(http.Header), err: broken}, broken, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := WriteClient(tc.writer, []byte("abc"))
			if n != tc.written || (tc.want != nil && (!errors.Is(err, tc.want) || !errors.Is(err, ErrClientWrite))) || (tc.want == nil && err != nil) {
				t.Fatalf("写入结果错误: n=%d err=%v", n, err)
			}
		})
	}
}

// flushFailureWriter 模拟写入缓冲成功、刷新时底层连接失败；不访问外部网络。
type flushFailureWriter struct {
	header http.Header
	err    error
}

// Header 返回测试响应头，供 PipeStream 设置协议类型。
func (w *flushFailureWriter) Header() http.Header { return w.header }

// WriteHeader 接收状态码，模拟响应已经开始。
func (w *flushFailureWriter) WriteHeader(int) {}

// Write 假装数据已写入缓冲区，供刷新失败路径测试。
func (w *flushFailureWriter) Write(p []byte) (int, error) { return len(p), nil }

// FlushError 返回连接错误，模拟真实 HTTP 缓冲区在刷新时才发现断连。
func (w *flushFailureWriter) FlushError() error { return w.err }

// TestPipeStreamReportsFlushFailure 验证最后一帧写缓冲成功但刷新失败不能记为成功。
// 先使用内存上游和可返回错误的刷新器，验证用量仍记录、错误归类且响应关闭，无外部数据需清理。
func TestPipeStreamReportsFlushFailure(t *testing.T) {
	broken := errors.New("flush failed")
	body := &closeProbe{Reader: strings.NewReader("data: {\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2}}\n\n")}
	w := &flushFailureWriter{header: make(http.Header), err: broken}
	started, usage, _, _, err := PipeStream(w, &http.Response{StatusCode: 200, Body: body}, time.Now())
	if !started || !errors.Is(err, ErrClientWrite) || !errors.Is(err, broken) || !body.closed || usage["prompt_tokens"] != float64(8) {
		t.Fatalf("刷新失败或上游用量未正确记录: started=%v usage=%v err=%v closed=%v", started, usage, err, body.closed)
	}
}
