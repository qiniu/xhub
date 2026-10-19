package regression

import "testing"

// TestUsageFailureRecovery 前置私有真实路由、数据库及本地上游；缺列让聚合/分页和管理员请求计数明确失败，迁移恢复后总计不丢失。
// 普通用户与管理员均验证接口契约；harness 删除 schema，无外部服务凭据。
func TestUsageFailureRecovery(t *testing.T) {
	h := newHarness(t, chatDeployment("usage-recovery"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "usage-recovery")
	empty := h.ok("GET", "/user/daily/activity/aggregated", owner.session, nil).json()
	if empty["metadata"].(map[string]any)["total_api_requests"] != float64(0) {
		t.Fatal("成功空范围不为零", empty)
	}
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("usage-recovery", "Verify usage failure recovery."))
	h.flushSpend()
	before := h.ok("GET", "/user/daily/activity/aggregated", owner.session, nil).json()["metadata"].(map[string]any)
	if _, err := h.db.Engine.Exec("ALTER TABLE usage_events DROP COLUMN session_source"); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{owner.session, admin} {
		for _, path := range []string{"/user/daily/activity/aggregated", "/user/daily/activity", "/gateway/daily/activity"} {
			r := h.do("GET", path, credential, nil)
			if r.status != 500 || r.json()["error"] == nil {
				t.Fatalf("后台失败不能伪装为成功空结果 %s: %s", path, r.describe())
			}
		}
	}
	if err := h.db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := h.ok("GET", "/user/daily/activity/aggregated", owner.session, nil).json()["metadata"].(map[string]any)
	for _, field := range []string{"total_api_requests", "total_spend", "total_tokens"} {
		if before[field] != after[field] {
			t.Fatalf("恢复改变 %s: %v -> %v", field, before[field], after[field])
		}
	}
}
