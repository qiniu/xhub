package regression

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/gateway"
)

// TestIdempotencyFenceAcrossInstances 验证新网关实例遇到同一幂等键时安全拒绝，不返回未保存的正文，也不重复调用或扣费。
// 使用私有 PostgreSQL、本机上游及两次真实网关 handler；隔离资源由 harness 回收，测试不连接生产。
func TestIdempotencyFenceAcrossInstances(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", "")
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := chatDeployment("restart-idempotent")
	h := newHarness(t, dep)
	admin := h.adminSession()
	scope := h.openScope(t, admin, "restart-idempotent")
	body := `{"model":"restart-idempotent","messages":[{"role":"user","content":"Test idempotent retry."}],"stream":false}`
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/chat/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+scope.key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "restart-fixture")
		return r
	}
	first := httptest.NewRecorder()
	h.gw.Handler().ServeHTTP(first, request())
	if first.Code != 200 || len(h.upstreamCalls()) != 1 {
		t.Fatalf("第一次请求未完成: status=%d calls=%v", first.Code, h.upstreamCalls())
	}
	h.flushSpend()
	before := h.moneyOf(t, scope)
	fresh := gateway.New(h.gw.Cfg, h.store, h.db)
	t.Cleanup(func() {
		_ = fresh.CloseUsageSpool()
		if fresh.Live != nil {
			_ = fresh.Live.Close()
		}
	})
	replay := httptest.NewRecorder()
	fresh.Handler().ServeHTTP(replay, request())
	fresh.FlushSpend()
	after := h.moneyOf(t, scope)
	if replay.Code != 409 || !strings.Contains(replay.Body.String(), "idempotency_result_unavailable") || strings.Contains(replay.Body.String(), "regression-ok") || len(h.upstreamCalls()) != 1 || after.key != before.key {
		t.Fatalf("新实例同一幂等键重复调用或扣费: status=%d calls=%d key_before=%v key_after=%v", replay.Code, len(h.upstreamCalls()), before.key, after.key)
	}
	conflicting := request()
	conflicting.Body = http.NoBody
	conflict := httptest.NewRecorder()
	fresh.Handler().ServeHTTP(conflict, conflicting)
	if conflict.Code != 409 || !strings.Contains(conflict.Body.String(), "idempotency_conflict") || len(h.upstreamCalls()) != 1 {
		t.Fatalf("同键不同正文未拒绝: status=%d body=%s calls=%d", conflict.Code, conflict.Body.String(), len(h.upstreamCalls()))
	}
	newRequest := request()
	newRequest.Header.Set("Idempotency-Key", "another-fixture")
	newResult := httptest.NewRecorder()
	fresh.Handler().ServeHTTP(newResult, newRequest)
	if newResult.Code != 200 || len(h.upstreamCalls()) != 2 {
		t.Fatalf("新幂等键被错误阻止: status=%d calls=%d", newResult.Code, len(h.upstreamCalls()))
	}
}

// TestIdempotencyFenceStorageFailure 验证幂等标识无法登记时拒绝请求，不能调用上游或产生费用。
// 前置私有 PostgreSQL、本机假上游及一次真实网关处理；约束和隔离 schema 由清理函数删除。
func TestIdempotencyFenceStorageFailure(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", "")
	h := newHarness(t, chatDeployment("fence-storage-failure"))
	admin := h.adminSession()
	scope := h.openScope(t, admin, "fence-storage-failure")
	if _, err := h.db.Engine.Exec(`ALTER TABLE inference_idempotency_fences ADD CONSTRAINT test_reject_fence CHECK (false)`); err != nil {
		t.Fatalf("注入存储失败: %v", err)
	}
	t.Cleanup(func() {
		if _, err := h.db.Engine.Exec(`ALTER TABLE inference_idempotency_fences DROP CONSTRAINT test_reject_fence`); err != nil {
			t.Errorf("清理测试约束: %v", err)
		}
	})
	before := h.moneyOf(t, scope)
	r := httptest.NewRequest("POST", "/chat/completions", strings.NewReader(`{"model":"fence-storage-failure","messages":[{"role":"user","content":"Test storage failure."}]}`))
	r.Header.Set("Authorization", "Bearer "+scope.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "unavailable-fixture")
	result := httptest.NewRecorder()
	h.gw.Handler().ServeHTTP(result, r)
	h.flushSpend()
	after := h.moneyOf(t, scope)
	if result.Code != 503 || !strings.Contains(result.Body.String(), "idempotency_unavailable") || len(h.upstreamCalls()) != 0 || before.key != after.key {
		t.Fatalf("存储失败未安全拒绝: status=%d calls=%d key_before=%v key_after=%v", result.Code, len(h.upstreamCalls()), before.key, after.key)
	}
}
