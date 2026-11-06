package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	// 幂等拒绝也必须产生本次独立的零费用失败日志，不能只检查余额未变化。
	if id := replay.Header().Get("x-litellm-call-id"); id == "" {
		t.Fatal("跨实例拒绝缺少调用 ID")
	} else {
		entry := logDetail(t, h, admin, id)
		if entry["status"] != "error" || parseFloatOrZero(fmt.Sprint(entry["spend"])) != 0 || fmt.Sprint(entry["metadata"].(map[string]any)["http_status"]) != "409" {
			t.Fatalf("跨实例拒绝日志或费用错误: %v", entry)
		}
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

// TestIdempotencyStorageFailureUsesAnthropicEnvelope 验证幂等存储故障遵循 Messages 协议的错误格式。
// 使用隔离 PostgreSQL 和真实网关，注入数据库约束并核对失败响应、零上游调用；清理由隔离 schema 完成。
func TestIdempotencyStorageFailureUsesAnthropicEnvelope(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", "")
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	h := newHarness(t, chatDeployment("fence-anthropic"))
	scope := h.openScope(t, h.adminSession(), "fence-anthropic")
	if _, err := h.db.Engine.Exec(`ALTER TABLE inference_idempotency_fences ADD CONSTRAINT reject_anthropic_fence CHECK (false)`); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"fence-anthropic","messages":[{"role":"user","content":"Hello"}],"max_tokens":16}`))
	r.Header.Set("Authorization", "Bearer "+scope.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "anthropic-fault")
	w := httptest.NewRecorder()
	h.gw.Handler().ServeHTTP(w, r)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("错误响应不是 JSON: %v", err)
	}
	errorBody, _ := body["error"].(map[string]any)
	if w.Code != 503 || body["type"] != "error" || errorBody["type"] != "idempotency_unavailable" || len(h.upstreamCalls()) != 0 {
		t.Fatalf("存储失败未按 Messages 协议返回或调用了上游: status=%d body=%v calls=%d", w.Code, body, len(h.upstreamCalls()))
	}
}

// TestIdempotencySlowStorageFailsClosed 验证幂等持久化卡顿时及时拒绝，不能调用上游。
// 使用隔离 PostgreSQL 的触发器模拟慢写，验证真实 HTTP 状态、时延和调用次数；schema 自动清理。
func TestIdempotencySlowStorageFailsClosed(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", "")
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	h := newHarness(t, chatDeployment("fence-slow"))
	scope := h.openScope(t, h.adminSession(), "fence-slow")
	_, err := h.db.Engine.Exec(`CREATE FUNCTION test_slow_fence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(5); RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Engine.Exec(`CREATE TRIGGER slow_fence BEFORE INSERT ON inference_idempotency_fences FOR EACH ROW EXECUTE FUNCTION test_slow_fence()`); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/chat/completions", strings.NewReader(`{"model":"fence-slow","messages":[{"role":"user","content":"Hello"}]}`))
	r.Header.Set("Authorization", "Bearer "+scope.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "slow-storage")
	w := httptest.NewRecorder()
	start := time.Now()
	h.gw.Handler().ServeHTTP(w, r)
	if elapsed := time.Since(start); w.Code != 503 || elapsed > 4*time.Second || len(h.upstreamCalls()) != 0 {
		t.Fatalf("慢存储应及时拒绝且不能调用上游: status=%d elapsed=%s calls=%d", w.Code, elapsed, len(h.upstreamCalls()))
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
	if id := result.Header().Get("x-litellm-call-id"); id == "" {
		t.Fatal("存储故障拒绝缺少调用 ID")
	} else {
		entry := logDetail(t, h, admin, id)
		if entry["status"] != "error" || parseFloatOrZero(fmt.Sprint(entry["spend"])) != 0 || fmt.Sprint(entry["metadata"].(map[string]any)["http_status"]) != "503" {
			t.Fatalf("存储故障拒绝日志或费用错误: %v", entry)
		}
	}
}
