package transport

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dpstream "github.com/sunqirui1987/xhub/internal/dataplane/stream"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
)

// bypassWriteProbe 模拟透传流首帧短写或写入失败，仅供内存转发测试。
type bypassWriteProbe struct {
	header http.Header
	short  bool
	calls  int
}

// Header 返回独立响应头，不访问外部资源。
func (w *bypassWriteProbe) Header() http.Header { return w.header }

// WriteHeader 接受上游响应状态，不访问外部资源。
func (w *bypassWriteProbe) WriteHeader(int) {}

// Write 首次返回短写或明确错误，记录调用次数。
func (w *bypassWriteProbe) Write(p []byte) (int, error) {
	w.calls++
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("synthetic write failure")
}

// Flush 满足流式输出接口；内存夹具无需刷新。
func (w *bypassWriteProbe) Flush() {}

// TestForwardOfficialClientWriteFailure 验证透传短写和错误会停止转发，返回客户端写入失败并关闭上游。
// 内存 HTTP 响应与故障写入器覆盖错误和短写，无外部服务或数据需清理。
func TestForwardOfficialClientWriteFailure(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "short"}[short], func(t *testing.T) {
			body := &terminalReader{Reader: strings.NewReader("data: {\"id\":\"chat-1\"}\n\n")}
			client := &http.Client{Transport: terminalRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
			})}
			w := &bypassWriteProbe{header: make(http.Header), short: short}
			result, err := ForwardOfficial(client, httptest.NewRequest("POST", "/", nil), "POST", "http://fixture.invalid", "fake", []byte(`{}`), http.Header{}, providercontract.Transport{Protocol: "openai-chat", Auth: providercontract.AuthConfig{Header: "Authorization"}}, w)
			if !errors.Is(err, dpstream.ErrClientWrite) || !result.Streamed || !body.closed || w.calls != 1 {
				t.Fatalf("透传写失败处理不符: err=%v streamed=%v closed=%v writes=%d", err, result.Streamed, body.closed, w.calls)
			}
			if short && !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("未保留短写原因: %v", err)
			}
		})
	}
}
