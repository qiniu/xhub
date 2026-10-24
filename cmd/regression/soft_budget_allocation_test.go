package regression

import (
	"net/http"
	"testing"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestSoftBudgetAllocationHTTP 验证真实推理造成个人超支后，同组织创建团队和用户仍成功。
// 前置独立数据库、本地上游及微小个人预算；验证余额拦截、持久化、真实父级超配回滚和历史消费不变，harness 删除隔离 schema。
func TestSoftBudgetAllocationHTTP(t *testing.T) {
	h := newHarness(t, chatDeployment("allocation-soft"))
	admin := h.adminSession()
	owner := h.provision(t, admin, "allocation-soft")
	h.setUserBudget(t, admin, owner.userID, 0.000001)
	h.ok(http.MethodPost, "/v1/chat/completions", owner.key, chatRequest("allocation-soft", "Complete this request."))
	h.flushSpend()
	before, err := h.db.GetUser(t.Context(), owner.userID)
	if err != nil || before.Spend <= *before.MaxBudget {
		t.Fatalf("前置条件未产生真实软预算超支: %+v %v", before, err)
	}
	mark := len(h.upstreamCalls())
	denied := h.do(http.MethodPost, "/v1/chat/completions", owner.key, chatRequest("allocation-soft", "Reject after exhaustion."))
	if denied.status != http.StatusForbidden || len(h.upstreamSince(mark)) != 0 {
		t.Fatalf("超支用户必须继续拒绝且不出站: %s", denied.describe())
	}
	team := h.ok(http.MethodPost, "/team/new", admin, map[string]any{"organization_id": owner.orgID, "team_alias": "new-after-overspend"}).json()
	teamID := firstString(team, "team_id")
	for i, target := range []string{owner.teamID, teamID} {
		email := []string{"same-team@example.invalid", "other-team@example.invalid"}[i]
		created := h.ok(http.MethodPost, "/user/new", admin, map[string]any{"user_email": email, "password": "allocation-password", "user_role": "user", "team_id": target, "max_budget": 500}).json()
		id := firstString(created, "user_id")
		user, err := h.db.GetUser(t.Context(), id)
		if err != nil || user.MaxBudget == nil || *user.MaxBudget != 500 || user.Spend != 0 {
			t.Fatalf("新增用户额度未持久化: %+v %v", user, err)
		}
		// 同一组织的批量额度入口也必须忽略未修改用户的历史超支。
		h.ok(http.MethodPost, "/user/bulk_update", admin, map[string]any{"users": []any{map[string]any{"user_id": id, "max_budget": 500}}})
		session := h.ok(http.MethodPost, "/v2/login", "", map[string]any{"username": email, "password": "allocation-password"}).json()
		h.ok(http.MethodPost, "/v1/chat/completions", firstString(session, "key"), chatRequest("allocation-soft", "New sibling can use its own budget."))
	}
	// 修改目标的预算下限仍受已有消费约束，失败不能覆盖原额度或账单。
	lowered := h.do(http.MethodPost, "/user/update", admin, map[string]any{"user_id": owner.userID, "max_budget": 0})
	if lowered.status != 400 {
		t.Fatalf("直接降到消费以下未拒绝: %s", lowered.describe())
	}
	h.flushSpend()
	// 固定父级仍保留所有下级额度，不能因为忽略已发生超支就放过真实超配。
	h.setOrgBudget(t, admin, owner.orgID, 1001)
	rejected := h.do(http.MethodPost, "/team/new", admin, map[string]any{"organization_id": owner.orgID, "team_alias": "must-rollback", "max_budget": 2})
	if rejected.status != 400 || firstString(rejected.json()["error"].(map[string]any), "type") != "quota_allocation_exceeded" {
		t.Fatalf("真实父级超配未拒绝: %s", rejected.describe())
	}
	count, err := h.db.Engine.Where("name = ? AND organization_id = ?", "must-rollback", owner.orgID).Count(new(iam.Team))
	if err != nil || count != 0 {
		t.Fatalf("拒绝后团队未回滚: count=%d err=%v", count, err)
	}
	after, err := h.db.GetUser(t.Context(), owner.userID)
	if err != nil || after.Spend != before.Spend || *after.MaxBudget != *before.MaxBudget {
		t.Fatalf("管理操作改写历史用量或预算: before=%+v after=%+v err=%v", before, after, err)
	}
}
