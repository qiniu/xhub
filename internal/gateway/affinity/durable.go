package affinity

import (
	"context"
	"strings"
)

// ConversationStore 保存不可随缓存过期的会话归属与响应历史；由PostgreSQL实现并按调用方隔离键。
// 不包含普通路由偏好；读取失败必须保留错误，比较写入必须原子执行。
type ConversationStore interface {
	ReadConversation(context.Context, string) (string, error)
	CompareConversation(context.Context, string, string, string) (string, error)
	WriteConversation(context.Context, string, string) error
}

// NewDurable 组装生产会话状态；redis保存短期路由偏好，store保存续聊必需状态。
// 返回独立状态；store为空时保留旧内存测试模式，不连接存储、不启动后台任务。
func NewDurable(redis Redis, store ConversationStore) *State {
	return &State{redis: redis, durable: store}
}

// durableKey 判断键是否属于续聊必需状态；供缓存读写分流，未知命名空间不自动持久化。
// 参数为内部生成的完整键，返回是否持久保存，不修改键、不授予权限。
func durableKey(key string) bool {
	for _, kind := range []string{"owner", "response", "history", "messages"} {
		if strings.HasPrefix(key, "session_route:v1:"+kind+":") {
			return true
		}
	}
	return false
}
