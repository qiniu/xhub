package dialogue

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	dpstream "github.com/sunqirui1987/xhub/internal/dataplane/stream"
)

// clientWriteProbe 在首个输出或 DONE 帧模拟短写/写入错误；测试内存响应，无外部资源。
type clientWriteProbe struct {
	header   http.Header
	target   string
	short    bool
	writes   int
	failed   bool
	failedAt int
}

// Header 返回单次响应头供流式编码器使用。
func (w *clientWriteProbe) Header() http.Header { return w.header }

// WriteHeader 接收响应状态，本夹具不写网络。
func (w *clientWriteProbe) WriteHeader(int) {}

// Write 保留成功写入长度，并在指定位置注入失败以检验分类和停止逻辑。
func (w *clientWriteProbe) Write(p []byte) (int, error) {
	w.writes++
	if !w.failed && (w.target == "first" || strings.Contains(string(p), "[DONE]")) {
		w.failed = true
		w.failedAt = w.writes
		if w.short {
			return len(p) - 1, nil
		}
		return 0, errors.New("synthetic write failure")
	}
	return len(p), nil
}

// Flush 实现流式接口，内存夹具无需刷新。
func (w *clientWriteProbe) Flush() {}

// TestDialogueClientWriteFailures 验证同协议、跨协议输出以及完成帧的短写和错误均不被当成成功。
// 内存 SSE 与注入式写入器覆盖首次/完成帧，要求分类为客户端写入失败且不继续写；无外部数据需清理。
func TestDialogueClientWriteFailures(t *testing.T) {
	const input = "data: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"
	for _, tc := range []struct {
		name, caller, target string
		short                bool
	}{
		{"native_error", "openai-chat", "first", false}, {"native_short", "openai-chat", "first", true},
		{"bridge_error", "openai-responses", "first", false}, {"bridge_short", "openai-responses", "first", true},
		{"bridge_done_error", "openai-chat", "done", false}, {"bridge_done_short", "openai-chat", "done", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &clientWriteProbe{header: make(http.Header), target: tc.target, short: tc.short}
			// 使用不同来源触发 Chat 的跨协议 DONE 输出；其余场景用 Chat 输入。
			upstream, raw := "openai-chat", input
			if tc.target == "done" {
				upstream = "anthropic-messages"
				raw = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"usage\":{\"input_tokens\":8,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			}
			_, _, _, _, err := PipeDialogue(w, &http.Response{Body: io.NopCloser(strings.NewReader(raw))}, time.Now(), "public", upstream, tc.caller)
			if !w.failed || !errors.Is(err, dpstream.ErrClientWrite) {
				t.Fatalf("客户端写入失败未正确分类: failed=%v err=%v writes=%d", w.failed, err, w.writes)
			}
			if w.writes != w.failedAt {
				t.Fatalf("写入失败后继续向客户端输出: failure=%d writes=%d", w.failedAt, w.writes)
			}
			if tc.short && !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("短写原因丢失: %v", err)
			}
		})
	}
}

// TestRefusalCompletionWriteFailure 验证供应商拒绝转换成 Chat 时完成帧短写不能记为成功。
// 用合成 Messages 拒绝流与内存短写器，要求保留短写原因且停止输出，无外部资源需清理。
func TestRefusalCompletionWriteFailure(t *testing.T) {
	raw := refusalStreamFrames(`{"type":"refusal","explanation":"Request declined."}`, false, true)
	w := &clientWriteProbe{header: make(http.Header), target: "done", short: true}
	_, _, _, _, err := PipeDialogue(w, &http.Response{Body: io.NopCloser(strings.NewReader(raw))}, time.Now(), "public", "anthropic-messages", "openai-chat")
	if !w.failed || !errors.Is(err, dpstream.ErrClientWrite) || !errors.Is(err, io.ErrShortWrite) || w.writes != w.failedAt {
		t.Fatalf("拒绝完成帧短写未报告或继续输出: err=%v failed=%v writes=%d", err, w.failed, w.writes)
	}
}

// dialogueFlushProbe 模拟正文写入缓冲成功、刷新时失败；只用于内存测试。
type dialogueFlushProbe struct {
	header http.Header
	err    error
	writes int
}

// Header 返回当前测试独占的响应头。
func (w *dialogueFlushProbe) Header() http.Header { return w.header }

// WriteHeader 接收状态码，不触及网络。
func (w *dialogueFlushProbe) WriteHeader(int) {}

// Write 模拟底层缓冲接收正文，返回完整写入长度。
func (w *dialogueFlushProbe) Write(p []byte) (int, error) { w.writes++; return len(p), nil }

// FlushError 报告刷新失败，模拟客户端连接已断开。
func (w *dialogueFlushProbe) FlushError() error { return w.err }

// TestDialogueFlushFailure 验证同协议和转换协议的刷新失败不会被伪装成成功。
// 使用内存 SSE 和失败刷新器，断言错误分类及停止输出，无外部资源需清理。
func TestDialogueFlushFailure(t *testing.T) {
	const raw = "data: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	for _, caller := range []string{"openai-chat", "openai-responses"} {
		t.Run(caller, func(t *testing.T) {
			w := &dialogueFlushProbe{header: make(http.Header), err: errors.New("flush failed")}
			_, _, _, _, err := PipeDialogue(w, &http.Response{Body: io.NopCloser(strings.NewReader(raw))}, time.Now(), "public", "openai-chat", caller)
			if !errors.Is(err, dpstream.ErrClientWrite) || w.writes != 1 {
				t.Fatalf("刷新失败未分类或继续输出: err=%v writes=%d", err, w.writes)
			}
		})
	}
}
