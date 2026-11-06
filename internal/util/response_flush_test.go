package util

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// flushTestWriter 用可控错误和刷新次数模拟不同 HTTP 包装层，不访问外部资源。
type flushTestWriter struct {
	http.ResponseWriter
	err     error
	flushed int
}

// FlushError 返回测试指定的底层失败，供调用方检查传播。
func (w *flushTestWriter) FlushError() error { w.flushed++; return w.err }

// flushDiscardingWrapper 模拟只支持无错误返回 Flush 的中间件，同时暴露被包装的写入器。
type flushDiscardingWrapper struct {
	http.ResponseWriter
	calls int
}

// Flush 模拟会丢掉底层错误的旧接口，测试要求不要优先调用。
func (w *flushDiscardingWrapper) Flush() { w.calls++ }

// WriteHeaderNow 模拟 Gin 已落实状态码的行为，供刷新时安全解包。
func (w *flushDiscardingWrapper) WriteHeaderNow() {}

// Unwrap 返回被包装的响应写入器，供测试沿链找到可返回错误的实现。
func (w *flushDiscardingWrapper) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// TestFlushResponsePropagation 验证包装层不会吞掉错误，无刷新支持和空写入器保持可用。
// 使用内存响应器，覆盖成功、失败和边界，测试不产生外部资源。
func TestFlushResponsePropagation(t *testing.T) {
	for _, err := range []error{nil, errors.New("broken")} {
		inner := &flushTestWriter{ResponseWriter: httptest.NewRecorder(), err: err}
		outer := &flushDiscardingWrapper{ResponseWriter: inner}
		if got := FlushResponse(outer); !errors.Is(got, err) || inner.flushed != 1 || outer.calls != 0 {
			t.Fatalf("刷新错误或包装层处理不正确: got=%v inner=%d outer=%d", got, inner.flushed, outer.calls)
		}
	}
	if err := FlushResponse(nil); err != nil {
		t.Fatalf("空写入器不应失败: %v", err)
	}
}
