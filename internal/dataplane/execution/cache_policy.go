package execution

import "context"

// responseCacheDisabledKey 是仅供网关内部诊断调用使用的上下文键；HTTP参数和头不能自行设置该标记。
type responseCacheDisabledKey struct{}

// WithoutResponseCache 标记一次内部诊断请求必须读取当前上游结果；ctx是原入站上下文，返回保留值、期限和取消的新上下文。
// 供提示词测试入口调用，仅禁用响应缓存读写，不绕过鉴权、模型权限、预算、护栏、路由、日志或计费；普通推理入口保持原策略。
func WithoutResponseCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, responseCacheDisabledKey{}, true)
}

// responseCacheDisabled 读取内部诊断缓存策略；ctx为当前请求上下文，返回是否禁读写响应缓存，缺少标记时为false。
// 数据面执行器调用，无副作用，也不解释客户端header或JSON字段。
func responseCacheDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(responseCacheDisabledKey{}).(bool)
	return disabled
}
