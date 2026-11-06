package util

import (
	"errors"
	"net/http"
)

// FlushResponse 刷新 HTTP 响应并尽量返回底层错误，供流式转发检测客户端断连。
// w 是响应写入器；返回刷新错误，不支持刷新时返回 nil。已知的 Gin 包装先落实响应头再解包，
// 因为其 Flush 丢弃底层错误；其他具有 Flush 的包装优先保留自身行为，避免绕过压缩等中间层。
// 调用时可能将已缓冲的正文发送给客户端；限制解包深度避免异常包装循环。
func FlushResponse(w http.ResponseWriter) error {
	err := FlushResponseStrict(w)
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// FlushResponseStrict 刷新响应并在不支持时返回 http.ErrNotSupported，供同步交付确认使用。
// 参数 w 为响应写入器；返回底层错误，可能发送已缓冲的响应，不推断客户端是否处理。
func FlushResponseStrict(w http.ResponseWriter) error {
	for depth := 0; w != nil && depth < 64; depth++ {
		if f, ok := w.(interface{ FlushError() error }); ok {
			return f.FlushError()
		}
		// Gin 的 Flush 只落实响应头并调用底层 Flush，会丢弃错误；手动落实头后解包。
		if ginWriter, ok := w.(interface {
			WriteHeaderNow()
			Unwrap() http.ResponseWriter
		}); ok {
			ginWriter.WriteHeaderNow()
			w = ginWriter.Unwrap()
			continue
		}
		// 其他包装可能包含压缩或缓冲行为，不能擅自绕过其 Flush。
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
			return nil
		}
		if wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
			w = wrapper.Unwrap()
			continue
		}
		return http.ErrNotSupported
	}
	return http.ErrNotSupported
}
