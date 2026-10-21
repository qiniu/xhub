package regression

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/gateway/affinity"
)

// historyFaultCache 在指定命名空间注入保存失败，其余缓存操作真实保存在互斥保护的内存中。
// 只替换共享缓存边界，HTTP、鉴权、预算和数据库均使用真实harness；没有外部持久资源。
type historyFaultCache struct {
	mu      sync.Mutex
	entries map[string]string
	fail    string
}

// ReadAffinity 返回精确已保存值；参数ctx不用于模拟延迟，读取不会更新TTL。
func (c *historyFaultCache) ReadAffinity(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[key], nil
}

// CompareAffinity 以互斥锁执行比较更新，返回实际值；只用于本地缓存归属测试。
func (c *historyFaultCache) CompareAffinity(_ context.Context, key, old, value string, _ time.Duration) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[key] == old {
		c.entries[key] = value
	}
	return c.entries[key], nil
}

// WriteAffinity 对所选阶段返回故障，其他值写入；参数TTL不影响短时测试生命周期。
func (c *historyFaultCache) WriteAffinity(_ context.Context, key, value string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.Contains(key, c.fail) {
		return errors.New("injected history persistence failure")
	}
	c.entries[key] = value
	return nil
}

// GetString 兼容任务缓存读取，不模拟故障或隐式生成内容。
func (c *historyFaultCache) GetString(ctx context.Context, key string) (string, bool) {
	v, _ := c.ReadAffinity(ctx, key)
	return v, v != ""
}

// SetString 兼容任务缓存写入，仅操作测试内存；失败按原接口忽略。
func (c *historyFaultCache) SetString(ctx context.Context, key, value string, ttl time.Duration) {
	_ = c.WriteAffinity(ctx, key, value, ttl)
}

// TestMessagesHistoryPersistenceFailure 验证引用、父上下文或归属保存失败均不发布完成项和重试已成功上游。
// 前置真实独立PG、有限额Key、两个候选部署与可注入缓存；JSON/SSE均不伪报完成且不遗留软预算预占，harness统一清理。
func TestMessagesHistoryPersistenceFailure(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, stream := range []bool{false, true} {
		for _, phase := range []string{":messages:", ":history:", ":response:"} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, phase), func(t *testing.T) {
				var calls atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					writeSignedMessagesFixture(w, map[string]any{"id": "msg_failure", "type": "message", "role": "assistant", "content": signedMessagesContent(1), "stop_reason": "tool_use", "usage": map[string]any{"input_tokens": 8, "output_tokens": 4}}, stream)
				}))
				t.Cleanup(up.Close)
				dep := hardBudgetDeployment("signed-failure")
				dep.LiteLLMParams["api_base"] = up.URL
				dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
				dep.ModelInfo["transport"] = "bypass_anthropic_messages"
				dep.ModelInfo["endpoint_types"] = []string{"responses"}
				dep.ModelInfo["messages_reasoning_mode"] = "adaptive"
				dep.ModelInfo["id"] = "failure-a"
				raw, _ := json.Marshal(dep)
				other := dep
				_ = json.Unmarshal(raw, &other)
				other.ModelInfo["id"] = "failure-b"
				h := newHarness(t, dep, other)
				h.gw.State = *affinity.New(&historyFaultCache{entries: map[string]string{}, fail: phase})
				admin := h.adminSession()
				owner := h.openScope(t, admin, dep.ModelName)
				h.setKeyBudget(t, admin, owner.key, 1)
				got := h.do(http.MethodPost, "/v1/responses", owner.key, map[string]any{"model": dep.ModelName, "input": "Call lookup.", "max_output_tokens": 64, "stream": stream, "reasoning": map[string]any{"effort": "low"}})
				if calls.Load() != 1 {
					t.Fatalf("缓存失败重复调用上游: %d", calls.Load())
				}
				if strings.Contains(got.text(), "response.completed") || strings.Contains(got.text(), "response.output_item.done") || strings.Contains(got.text(), "PRIVATE_") {
					t.Fatalf("失败发布完成项或原始签名: %s", got.text())
				}
				if !stream && got.status != 502 {
					t.Fatalf("缓存失败未明确502: %s", got.describe())
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
				if err != nil || len(holds) != 0 {
					t.Fatalf("软预算历史保存失败不应遗留预占: %+v %v", holds, err)
				}
			})
		}
	}
}
