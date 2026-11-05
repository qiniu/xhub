package execution

import dptransport "github.com/sunqirui1987/xhub/internal/dataplane/transport"

// bypassAttempt 保存一次原生转发的响应事实及错误，只在当前请求的执行栈中使用。
// response 的状态、已输出标记和正文由转发器产生；错误为空不代表所有协议都已完成。
// 不改变流转发、预算或任务结算，也不在不同候选之间共享。
type bypassAttempt struct {
	response dptransport.ForwardResponse
	err      error
}

// bypassFailure 表示原生转发失败后的既有处置，不承担 HTTP 失败分类或结算。
type bypassFailure uint8

const (
	bypassNoFailure bypassFailure = iota
	bypassCanceled
	bypassStarted
	bypassRetry
	bypassStop
)

// failure 根据入站上下文、是否已写响应和现有对话重放资格分类一次转发错误。
// callerErr 为原始请求的上下文错误，不得传入已主动释放的尝试上下文；replay 表示现有路由允许重放对话。
// 返回取消、已开始输出、允许重试或停止；无错误返回 bypassNoFailure。只分类，不写日志或修改冷却。
func (a bypassAttempt) failure(callerErr error, replay bool) bypassFailure {
	if a.err == nil {
		return bypassNoFailure
	}
	if callerErr != nil {
		return bypassCanceled
	}
	if a.response.Streamed {
		return bypassStarted
	}
	if replay {
		return bypassRetry
	}
	return bypassStop
}
