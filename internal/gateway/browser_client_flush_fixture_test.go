package gateway

import (
	"errors"
	"net/http"
)

// browserFlushFailureWriter 只在隔离浏览器夹具中模拟实际写入缓冲后刷新失败，不编入生产二进制。
type browserFlushFailureWriter struct{ http.ResponseWriter }

// FlushError 模拟网络刷新故障，供真实网关记录失败日志；不修改业务数据。
func (w *browserFlushFailureWriter) FlushError() error {
	return errors.New("isolated browser flush failure")
}

// Unwrap 返回真实写入器，供通用响应控制器查找能力；没有额外副作用。
func (w *browserFlushFailureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
