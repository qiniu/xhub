package regression

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestSoftBudgetProtocols 验证三协议同步/流式请求在微小正余额下完成，按实测扣费后拒绝后续调用。
// 前置独立PG、本地上游，余额小于任何整窗预占；断言响应、持久账单与无预占，无付费依赖，夹具自动清理。
func TestSoftBudgetProtocols(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", path, stream), func(t *testing.T) {
				dep := chatDeployment("soft-budget")
				h := newHarness(t, dep)
				admin := h.adminSession()
				c := h.openScope(t, admin, dep.ModelName)
				h.setKeyBudget(t, admin, c.key, 0.000001)
				body := map[string]any{"model": dep.ModelName, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "Reply briefly."}}}
				if path == "/v1/responses" {
					delete(body, "messages")
					body["input"] = "Reply briefly."
				}
				if path == "/v1/messages" {
					body["max_tokens"] = 32
				}
				got := h.do("POST", path, c.key, body)
				if got.status != 200 {
					t.Fatalf("小额余额应放行: %s", got.describe())
				}
				if got.header("X-XHub-Budget-Guarantee") != "soft" || got.header("X-XHub-Budget-Reservation-Id") != "" {
					t.Fatal("未统一软预算")
				}
				key, err := h.db.GetKey(t.Context(), c.keyID)
				if err != nil {
					t.Fatal(err)
				}
				if key.Spend <= 0.000001 {
					t.Fatalf("实际消费未完整落库: %g", key.Spend)
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), c.keyID)
				if err != nil || len(holds) != 0 {
					t.Fatalf("软预算不应预占: %v %v", holds, err)
				}
				got = h.do("POST", path, c.key, body)
				if got.status != 403 {
					t.Fatalf("负余额未拒绝: %s", got.describe())
				}
			})
		}
	}
}

// TestSoftBudgetConcurrent 验证多个已经放行的请求均可完成，允许并发超支且不会截断在途请求。
// 前置独立PG与阻塞本地上游；等待全部进入后释放，逐笔实际扣费并拒绝下一请求，自动清理。
func TestSoftBudgetConcurrent(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := chatDeployment("soft-concurrent")
	h := newHarness(t, dep)
	admin := h.adminSession()
	c := h.openScope(t, admin, dep.ModelName)
	h.setKeyBudget(t, admin, c.key, 0.000001)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	h.mu.Lock()
	h.rules = map[string]upstreamRule{dep.ModelName: {hold: release}}
	h.mu.Unlock()
	results := make(chan reply, 3)
	for i := 0; i < 3; i++ {
		go func(i int) {
			results <- h.do("POST", "/v1/chat/completions", c.key, chatRequest(dep.ModelName, fmt.Sprintf("Concurrent %d", i)))
		}(i)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(h.upstreamCalls()) < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(h.upstreamCalls()) != 3 {
		t.Fatal("正余额并发请求被预占阻断")
	}
	once.Do(func() { close(release) })
	for i := 0; i < 3; i++ {
		if got := <-results; got.status != 200 {
			t.Fatalf("在途请求中断: %s", got.describe())
		}
	}
	key, err := h.db.GetKey(t.Context(), c.keyID)
	if err != nil || !nearlyEqual(key.Spend, 3*expectedCost()) {
		t.Fatalf("并发请求必须逐笔扣费且不重复: key=%v err=%v", key, err)
	}
	if got := h.do("POST", "/v1/chat/completions", c.key, chatRequest(dep.ModelName, "After exhaustion")); got.status != 403 {
		t.Fatal("耗尽后未拒绝")
	}
}

// TestSoftBudgetCancellation 验证客户端取消后无遗留预占，后续正常请求仍能完成。
// 前置私有数据库与阻塞上游；取消真实 HTTP 客户端，释放阻塞后验证后续请求，清理所有资源。
func TestSoftBudgetCancellation(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := hardBudgetDeployment("hard-cancel")
	h := newHarness(t, dep)
	admin := h.adminSession()
	c := h.openScope(t, admin, "cancel")
	h.setKeyBudget(t, admin, c.key, hardBudgetAmount(t, dep))
	release := h.holdUpstream(dep.ModelName)
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	raw, _ := json.Marshal(chatRequest(dep.ModelName, "Cancel after submission."))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.server.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.upstreamCalls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(h.upstreamCalls()) != 1 {
		t.Fatal("取消前没有真正出站")
	}
	cancel()
	<-done
	release()
	holds, err := h.db.PendingKeyBudget(t.Context(), c.keyID)
	if err != nil || len(holds) != 0 {
		t.Fatalf("取消遗留预占: %v %v", holds, err)
	}
	h.ok(http.MethodPost, "/v1/chat/completions", c.key, chatRequest(dep.ModelName, "Continue after cancellation."))
}

// TestSoftBudgetRetry 验证上游失败按配置重试，不因整窗预占阻断；失败不扣费也不留下预占。
// 前置独立PG、本地503上游和两次尝试模板，测试结束由harness清理全部数据。
func TestSoftBudgetRetry(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := chatDeployment("soft-retry")
	h := newHarness(t, dep)
	admin := h.adminSession()
	c := h.openScope(t, admin, "retry")
	h.setKeyBudget(t, admin, c.key, 0.000001)
	h.scriptStatus(dep.ModelName, 503)
	routeID := routeTemplate(t, h, admin, "two soft attempts", routeTemplateBody([]any{}, 2, 60, 0, 0))
	bindTemplate(t, h, admin, "key", c.keyID, routeID)
	got := h.do("POST", "/v1/chat/completions", c.key, chatRequest(dep.ModelName, "Retry without reservations."))
	if got.status != 503 || len(h.upstreamCalls()) != 2 {
		t.Fatalf("重试被错误预算策略中断: %s calls=%d", got.describe(), len(h.upstreamCalls()))
	}
	key, err := h.db.GetKey(t.Context(), c.keyID)
	if err != nil || key.Spend != 0 {
		t.Fatalf("失败请求被错误扣费: %v %v", key, err)
	}
	holds, err := h.db.PendingKeyBudget(t.Context(), c.keyID)
	if err != nil || len(holds) != 0 {
		t.Fatalf("重试遗留预占: %v %v", holds, err)
	}
}
