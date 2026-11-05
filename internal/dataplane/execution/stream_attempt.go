package execution

import "time"

// streamAttempt 保存统一入口单次流式尝试的观察事实，仅由当前请求创建和消费。
// 上游状态、客户端是否开始接收、计时、用量、正文和错误各自独立；nil 错误不证明所有协议都有成功终态。
// 它不改变协议转换、请求取消、预算、重试策略或日志存储，也不跨尝试共享。
type streamAttempt struct {
	upstreamStatus int
	clientStarted  bool
	usage          map[string]any
	ttft           time.Duration
	body           []byte
	err            error
}

// mustStopAfterFailure 在流处理失败时判断是否必须结束本次请求，供 Serve 的既有失败分支使用。
// preparationFailed 表示终态前历史保存失败，nativeResponses 表示不可盲目重放的原生响应；返回 true 表示不得重试。
// 入站取消另由调用方优先处理；成功或空错误返回 false，本方法不写出或更改状态。
func (a streamAttempt) mustStopAfterFailure(preparationFailed, nativeResponses bool) bool {
	return a.err != nil && (a.clientStarted || preparationFailed || nativeResponses)
}
