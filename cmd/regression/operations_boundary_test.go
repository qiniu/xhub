package regression

import (
	"strings"
	"testing"
)

// TestOperationsNoFalseSuccess 通过真实HTTP/PostgreSQL验证未实现的运维操作不会冒充已执行。
// 前置隔离租户与本地模型；验证原401/403、管理员501、无配置写入及对话/费用不变；harness严格回收schema。
func TestOperationsNoFalseSuccess(t *testing.T) {
	h := newHarness(t, chatDeployment("ops-boundary"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "ops")
	for _, op := range []struct{ method, path string }{
		{"GET", "/coordination_redis/settings"}, {"POST", "/coordination_redis/settings"}, {"POST", "/coordination_redis/settings/test"},
		{"GET", "/debug/asyncio-tasks"}, {"GET", "/debug/memory/details"}, {"GET", "/debug/memory/summary"}, {"POST", "/debug/memory/gc/configure"},
		{"GET", "/memory-usage"}, {"GET", "/memory-usage-in-mem-cache"}, {"GET", "/memory-usage-in-mem-cache-items"},
		{"GET", "/health/backlog"}, {"GET", "/health/drain"}, {"GET", "/health/history"}, {"GET", "/health/license"}, {"GET", "/health/shared-status"},
		{"GET", "/health/services"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {admin, 501}} {
				got := h.do(op.method, op.path, actor.token, map[string]any{"host": "127.0.0.1", "port": 1, "password": "test-secret-must-not-echo", "generation_0": 500})
				if got.status != actor.status {
					t.Errorf("%s %s 状态应为%d: %s", op.method, op.path, actor.status, got.describe())
					continue
				}
				if actor.status == 501 {
					e, _ := got.json()["error"].(map[string]any)
					if e["type"] != "unsupported_operation" || strings.Contains(got.describe(), "test-secret-must-not-echo") {
						t.Error("未实现操作必须明确拒绝且不回显凭据", got.describe())
					}
				}
			}
		})
	}
	for _, kind := range []string{"coordination_redis", "debug", "health", "memory-usage", "memory-usage-in-mem-cache", "memory-usage-in-mem-cache-items"} {
		rows, err := h.store.ListKV(kind)
		if err != nil || len(rows) != 0 {
			t.Errorf("未实现操作污染配置 %s count=%d err=%v", kind, len(rows), err)
		}
	}
	for _, path := range []string{"/health", "/health/liveness", "/health/liveliness", "/health/readiness", "/health/readiness/details"} {
		h.ok("GET", path, "", nil)
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, map[string]any{"model": "ops-boundary", "messages": []map[string]any{{"role": "user", "content": "Verify unsupported operations did not change inference."}}})
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("旧运维操作影响对话或费用")
	}
	// 用尚未消费的独立租户验证零预算；已消费用户不能降至低于已用额度，不能为测试绕过该规则。
	blockedOwner := h.openScope(t, admin, "ops-zero-budget")
	h.setUserBudget(t, admin, blockedOwner.userID, 0)
	denied := h.do("POST", "/v1/chat/completions", blockedOwner.key, map[string]any{"model": "ops-boundary", "messages": []map[string]any{{"role": "user", "content": "Verify zero budget still blocks inference."}}})
	if denied.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算规则改变", denied.describe())
	}
}

// TestReadinessDetailsDatabaseFailure 在真实网关关闭本测试专属SQL连接，验证详情不能在数据库失败时仍报告ready。
// 前置正常PostgreSQL并先验证200；故障后详情及普通就绪503、存活200；独立schema清理连接不受影响。
func TestReadinessDetailsDatabaseFailure(t *testing.T) {
	h := newHarness(t)
	before := h.ok("GET", "/health/readiness/details", "", nil).json()
	if before["status"] != "ready" || before["litellm_version"] == nil {
		t.Fatal("就绪详情成功契约改变", before)
	}
	if err := h.store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/readiness", "/health/readiness/details"} {
		got := h.do("GET", path, "", nil)
		if got.status != 503 {
			t.Errorf("数据库不可达却报告就绪: %s %s", path, got.describe())
		}
	}
	if got := h.ok("GET", "/health/liveness", "", nil).json(); got["status"] != "ok" {
		t.Fatal("存活检查不应依赖数据库")
	}
}
