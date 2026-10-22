package regression

import (
	"net/http"
	"testing"
)

// TestKeyRotationPendingBudget 验证真实推理、轮换、额度拒绝和恢复后的累计费用头。
// 前置隔离 PostgreSQL、本地模型与专用 Redis；轮换前费用尚未刷库，最后刷写并清理私有 schema/进程。
func TestKeyRotationPendingBudget(t *testing.T) {
	isolatedSpendRedis(t)
	const model = "rotation-pending-budget"
	h := newHarness(t, chatDeployment(model))
	admin := h.adminSession()
	c := h.openScope(t, admin, "rotation-pending")
	h.setKeyBudget(t, admin, c.key, expectedCost())
	h.ok(http.MethodPost, "/v1/chat/completions", c.key, chatRequest(model, "Exhaust original key budget."))
	if got := h.do(http.MethodPost, "/v1/chat/completions", c.key, chatRequest(model, "Must be refused.")); got.status != 403 {
		t.Fatalf("轮换前未用尽: %s", got.describe())
	}
	rotated := h.ok(http.MethodPost, "/key/regenerate", c.session, map[string]any{"key": c.key}).json()
	fresh := firstString(rotated, "key")
	if got := h.do(http.MethodGet, "/v1/models", c.key, nil); got.status != 401 {
		t.Fatalf("旧凭据未撤销: %s", got.describe())
	}
	if got := h.do(http.MethodPost, "/v1/chat/completions", fresh, chatRequest(model, "Rotated key remains exhausted.")); got.status != 403 {
		t.Fatalf("轮换丢失待结算额度: %s", got.describe())
	}
	if len(h.upstreamCalls()) != 1 {
		t.Fatalf("额度拒绝仍调用上游: %d", len(h.upstreamCalls()))
	}
	h.setKeyBudget(t, admin, c.keyID, 10*expectedCost())
	next := h.ok(http.MethodPost, "/v1/chat/completions", fresh, chatRequest(model, "Verify accumulated cost after rotation."))
	if got := parseFloatOrZero(next.header("x-litellm-key-spend")); !nearlyEqual(got, 2*expectedCost()) {
		t.Fatalf("累计费用头未保留旧哈希消费: %v", got)
	}
	h.flushSpend()
	key, err := h.db.GetKey(t.Context(), c.keyID)
	if err != nil || !nearlyEqual(key.Spend, 2*expectedCost()) {
		t.Fatalf("刷库账单错误: %+v %v", key, err)
	}
	if got, err := h.gw.Live.HotKeySpend(c.keyID); err != nil || got != 0 {
		t.Fatalf("刷库未释放 Key 索引: %v %v", got, err)
	}
}

// TestKeyPendingUnavailableContract 验证依赖待结算费用的请求在索引故障时返回 503，且不访问上游。
// 无独立 Key 额度的请求不依赖累计展示，可继续执行但省略该头；仅破坏私有 Redis 索引，恢复后夹具清理。
func TestKeyPendingUnavailableContract(t *testing.T) {
	rdb := isolatedSpendRedis(t)
	const model = "pending-unavailable"
	h := newHarness(t, chatDeployment(model))
	admin := h.adminSession()
	c := h.openScope(t, admin, "pending-unavailable")
	h.setKeyBudget(t, admin, c.key, 1)
	if err := rdb.Set(t.Context(), "xhub:spend:key-indexed", "broken", 0).Err(); err != nil {
		t.Fatal(err)
	}
	got := h.do(http.MethodPost, "/v1/chat/completions", c.key, chatRequest(model, "Do not use unknown budget."))
	if got.status != 503 || len(h.upstreamCalls()) != 0 {
		t.Fatalf("热费用异常未明确拒绝: %s upstream=%d", got.describe(), len(h.upstreamCalls()))
	}
	h.ok(http.MethodPost, "/key/update", admin, map[string]any{"key": c.keyID, "max_budget": nil})
	got = h.ok(http.MethodPost, "/v1/chat/completions", c.key, chatRequest(model, "Unlimited key does not require spend display."))
	if got.header("x-litellm-key-spend") != "" {
		t.Fatal("损坏热费用仍展示累计金额")
	}
	if err := rdb.Del(t.Context(), "xhub:spend:key-indexed").Err(); err != nil {
		t.Fatal(err)
	}
	h.flushSpend()
}
