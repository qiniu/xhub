package execution

import (
	"context"
	"fmt"
)

// clientCachePolicy 表示标准对话请求的响应缓存读写控制，不涉及供应商提示词缓存。
type clientCachePolicy struct{ read, write bool }

// consumeClientCachePolicy 解析并消费本次已解码JSON中的no-cache/no-store布尔开关。
// 参数body为Serve独占的请求对象；返回读写策略或400原因，默认允许读写。仅移除已识别字段，保留未知扩展。
// 调用方在鉴权后、协议解析前执行；不修改身份/预算/护栏。null等同缺省，非布尔控制明确拒绝。
func consumeClientCachePolicy(body map[string]any) (clientCachePolicy, error) {
	p := clientCachePolicy{read: true, write: true}
	if body["cache"] == nil {
		return p, nil
	}
	options, ok := body["cache"].(map[string]any)
	if !ok {
		return p, fmt.Errorf("cache must be an object")
	}
	for _, key := range []string{"no-cache", "no-store"} {
		if value, exists := options[key]; exists {
			flag, valid := value.(bool)
			if !valid {
				return p, fmt.Errorf("cache.%s must be a boolean", key)
			}
			if key == "no-cache" {
				p.read = !flag
			} else {
				p.write = !flag
			}
		}
	}
	// 控制字段不属于模型输入，也不应产生不同缓存键；否则no-store无法读取普通请求留下的值。
	delete(options, "no-cache")
	delete(options, "no-store")
	if len(options) == 0 {
		delete(body, "cache")
	}
	return p, nil
}

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
