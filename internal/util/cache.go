// Package util is the in-process response cache. Capacity is fixed, and reads and writes copy bytes so a caller cannot mutate a stored value.
package util

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

// cacheEntries is the fixed LRU capacity. The hashicorp LRU keeps the map from growing without a limit, and the library is safe for concurrent use.
const cacheEntries = 8192

// DualCache 是容量为 8192 的并发安全进程内响应缓存；读写复制正文及授权范围，避免调用方修改缓存。
type DualCache struct {
	c *lru.Cache[string, CachedResponse]
}

// Ready 判断进程缓存是否已初始化；无参数，由管理状态与清理入口调用，返回可用状态。
// nil接收器及零值均不可用；只读固定实例指针，不增删条目、不访问外部网络。
func (c *DualCache) Ready() bool {
	return c != nil && c.c != nil
}

// CachedResponse 保存成功正文及其实际模型权限范围；命中时必须按当前身份复核 Models，
// 不能把首次调用的授权当成永久许可。模型/组名仅供权限检查，不提交会话偏好。
type CachedResponse struct {
	Body   []byte
	Models []string
}

var logTraceOnceCache sync.Once

// NewCache builds an in-process cache of 8192 entries. It panics if that capacity is illegal, which would be a programming error.
// 参数：无。
// 调用：authz/authz.go、authz/decide.go、dataplane/serve.go、gateway/engine.go
// 测试：activity_http_test.go、authz_test.go、builtin_providers_test.go
// 返回：容量 8192 的进程内缓存。容量非法才会 panic，正常调用不会返回 nil。
func NewCache() *DualCache {
	logTraceOnceCache.Do(func() { Trace("enter util.New") })

	c, err := lru.New[string, CachedResponse](cacheEntries)
	if err != nil {
		// NewCache fails only when the capacity is below 1. The constant is not that case, so a failure means the library was called incorrectly.
		panic(err)
	}
	return &DualCache{c: c}
}

// CacheKey hashes the tenant, operation, model, and body into a cache key. Parts are separated by a zero byte so "ab"+"c" and "a"+"bc" do not collide.
// 调用：dataplane/serve.go
// 测试：failure_log_test.go
// 参数 parts（...string）：拼缓存键的片段，顺序敏感。
// 返回：sha256 十六进制。parts 用 0 字节隔开，所以 "ab"+"c" 和 "a"+"bc" 不会撞键。
func CacheKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get 返回独立的正文及权限范围副本；参数为缓存键，供 Serve 在鉴权后读取。
// 参数 key（string）：Key 拼出的缓存键，不是模型字段名。
// 返回：缓存条目及是否命中；未命中返回零值。修改任一切片均不会改动缓存，无其他副作用。
// 调用：Serve 在扩展通过之后。
// 测试：failure_log_test.go TestServeLogsCacheHitAndStreamMetrics。
func (c *DualCache) Get(key string) (CachedResponse, bool) {
	value, ok := c.c.Get(key)
	if !ok {
		return CachedResponse{}, false
	}
	return CachedResponse{Body: append([]byte(nil), value.Body...), Models: append([]string(nil), value.Models...)}, true
}

// Set 保存正文及实际授权模型/组的副本，供成功 JSON 响应写入；不判断路由配置是否存在。
// 调用：gateway/spend.go 写入成功响应；测试可预存私有条目。
// 测试：util/cache_test.go、gateway/response_cache_test.go。
// 参数 key 为缓存键，value 为成功正文，models 为执行时已检查的实际模型/组；省略表示仅请求别名。
// 调用方修改输入切片不影响缓存；同键替换正文和权限范围，避免两者更新不同步。
// 返回：无。不写 HTTP 响应。
func (c *DualCache) Set(key string, value []byte, models ...string) {
	c.c.Add(key, CachedResponse{Body: append([]byte(nil), value...), Models: append([]string(nil), models...)})
}

// Flush removes every cached response. It does not write spend or talk to Redis.
// 参数：无。
// 调用：gateway/access.go 的管理端清空缓存接口。
// 测试：cache_test.go TestFlush；regression/consistency_test.go。
// 返回：无。进程内的响应缓存已清空。不写用量，也不访问 Redis。
func (c *DualCache) Flush() {
	c.c.Purge()
}
