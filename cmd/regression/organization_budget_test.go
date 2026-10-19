package regression

import (
	"net/url"
	"testing"
	"time"
)

// TestOrganizationBudgetAndFilters 验证真实组织路由、周期持久化、搜索可见性、混合越权回滚和管理员关系。
// 前置私有数据库及本地上游；到期前拒绝、到期恢复真实推理且历史用量保留，harness 删除全部测试资源。
func TestOrganizationBudgetAndFilters(t *testing.T) {
	h := newHarness(t, chatDeployment("period-model"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "period-owner")
	other := h.openScope(t, admin, "period-foreign")
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": owner.orgID, "max_budget": 10, "budget_duration": "30d", "organization_alias": "FDE 周期试点"})
	before := h.ok("GET", "/organization/info?organization_id="+owner.orgID, admin, nil).json()
	if before["budget_duration"] != "30d" || before["budget_reset_at"] == nil {
		t.Fatal("周期没有回读", before)
	}
	for _, bad := range []any{"monthly", 30, true, []any{"30d"}} {
		r := h.do("PATCH", "/organization/update", admin, map[string]any{"organization_id": owner.orgID, "organization_alias": "bad", "budget_duration": bad})
		if r.status != 400 {
			t.Fatal("非法周期未拒绝", r.describe())
		}
		r = h.do("POST", "/organization/new", admin, map[string]any{"organization_alias": "bad", "budget_duration": bad})
		if r.status != 400 {
			t.Fatal("非法创建未拒绝", r.describe())
		}
	}
	for _, field := range []string{"max_budget", "budget_duration", "rpm_limit", "tpm_limit", "models", "status"} {
		value := map[string]any{"max_budget": nil, "budget_duration": nil, "rpm_limit": nil, "tpm_limit": nil, "models": []string{}, "status": "active"}[field]
		r := h.do("PATCH", "/organization/update", owner.session, map[string]any{"organization_id": owner.orgID, "organization_alias": "must-not-save", field: value})
		if r.status != 403 {
			t.Fatalf("混合越权 %s 未整体拒绝: %s", field, r.describe())
		}
	}
	current := h.ok("GET", "/organization/info?organization_id="+owner.orgID, admin, nil).json()
	if current["organization_alias"] != "FDE 周期试点" {
		t.Fatal("失败修改覆盖了原名称", current)
	}
	h.ok("PATCH", "/organization/update", owner.session, map[string]any{"organization_id": owner.orgID, "organization_alias": "FDE Alpha"})
	for _, tc := range []struct {
		credential, query string
		count             int
	}{{admin, "org_alias=fde", 1}, {admin, "org_alias=FDE&org_id=" + owner.orgID, 1}, {admin, "org_id=missing", 0}, {owner.session, "org_id=" + other.orgID, 0}, {owner.session, "org_alias=foreign", 0}, {admin, "org_alias=" + url.QueryEscape("Alpha"), 1}} {
		if rows := rowsOf(h.ok("GET", "/organization/list?"+tc.query, tc.credential, nil)); len(rows) != tc.count {
			t.Fatalf("筛选/可见性错误 %s: %v", tc.query, rows)
		}
	}
	if _, err := h.db.Engine.Exec("UPDATE organizations SET spend=10 WHERE id=?", owner.orgID); err != nil {
		t.Fatal(err)
	}
	mark := len(h.upstreamCalls())
	if r := h.do("POST", "/v1/chat/completions", owner.key, chatRequest("period-model", "Verify exhausted organization budget.")); r.status != 429 || len(h.upstreamCalls()) != mark {
		t.Fatal("到期前没有拒绝", r.describe())
	}
	if _, err := h.db.Engine.Exec("UPDATE organizations SET budget_reset_at=? WHERE id=?", time.Now().Add(-time.Second), owner.orgID); err != nil {
		t.Fatal(err)
	}
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("period-model", "Verify renewed organization budget."))
	h.flushSpend()
	current = h.ok("GET", "/organization/info?organization_id="+owner.orgID, admin, nil).json()
	if current["spend"].(float64) <= 0 || current["spend"].(float64) >= 10 {
		t.Fatal("重置后未按新周期记账", current)
	}
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": owner.orgID, "budget_duration": nil})
	stopped := h.ok("GET", "/organization/info?organization_id="+owner.orgID, admin, nil).json()
	if stopped["budget_duration"] != "" || stopped["budget_reset_at"] != nil || stopped["spend"] != current["spend"] {
		t.Fatal("取消重置改变消费", stopped)
	}
	// 组织关系只能增加/撤销 org_admin，不修改平台角色；撤销后不得继续改名。
	h.ok("POST", "/organization/member_delete", admin, map[string]any{"organization_id": owner.orgID, "user_id": owner.userID})
	if r := h.do("PATCH", "/organization/update", owner.session, map[string]any{"organization_id": owner.orgID, "organization_alias": "unauthorized"}); r.status != 403 {
		t.Fatal("撤销管理员后仍能修改", r.describe())
	}
	h.ok("POST", "/organization/member_add", admin, map[string]any{"organization_id": owner.orgID, "member": map[string]any{"user_email": owner.email, "role": "org_admin"}})
	u, err := h.db.GetUser(t.Context(), owner.userID)
	if err != nil || u.Admin() {
		t.Fatal("组织关系改变平台角色", u, err)
	}
}
