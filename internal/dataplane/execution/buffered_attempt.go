package execution

import (
	"io"
	"net/http"
)

// bufferedAttempt 保存标准入口非流式正文或 4xx 错误预读的状态、已读正文和读取错误。
// 转换可以替换本次正文；读取成功只代表传输完成，不代表协议内容成功或允许重放。
// 普通 4xx 会先建立预读结果判断回退，再替换正文供后续分支消费；后续结果不会继承预读错误。
// 结果仅属于当前尝试，不跨候选共享，也不决定预算、记账或响应提交。
type bufferedAttempt struct {
	status  int
	body    []byte
	readErr error
}

// readBufferedAttempt 读取并关闭非流式上游响应或 4xx 预读正文，供标准执行器在释放尝试上下文前调用。
// resp 是已经成功收到的 HTTP 响应；返回实际状态、已读字节和读取错误，关闭错误沿用原有忽略规则。
// 失败时保留已读字节供取消诊断，不重试、不写客户端或日志，也不吞掉读取错误。
func readBufferedAttempt(resp *http.Response) bufferedAttempt {
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return bufferedAttempt{status: resp.StatusCode, body: body, readErr: err}
}
