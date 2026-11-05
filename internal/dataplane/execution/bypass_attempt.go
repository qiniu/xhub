package execution

// bypassFailure 表示原生转发失败后的既有处置，不承担 HTTP 失败分类或结算。
type bypassFailure uint8

const (
	bypassCanceled bypassFailure = iota
	bypassStarted
	bypassRetry
	bypassStop
)

// classifyBypassFailure 在原生转发已返回错误后，根据入站上下文、输出状态和对话重放资格分类。
// callerErr 是原始请求上下文错误，streamed 为转发器记录的输出状态，replay 表示路由允许重放对话。
// 返回取消、已开始输出、允许重试或停止；只由失败分支调用，不写日志或修改冷却。
func classifyBypassFailure(callerErr error, streamed, replay bool) bypassFailure {
	if callerErr != nil {
		return bypassCanceled
	}
	if streamed {
		return bypassStarted
	}
	if replay {
		return bypassRetry
	}
	return bypassStop
}
