package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// bulkBudgetRequest 构造显式用户预算数组；参数为ID与相同上限（nil明确取消），返回请求体，供真实HTTP回归共用，无外部副作用。
func bulkBudgetRequest(ids []string, limit any) map[string]any {
	users := make([]any, 0, len(ids))
	for _, id := range ids {
		users = append(users, map[string]any{"user_id": id, "max_budget": limit})
	}
	return map[string]any{"users": users}
}

// TestUserBulkBudgetContract 验证真实用户批量接口、零/取消语义、角色边界、错误原子性和数据面额度执行。
// 前置私有PostgreSQL、真实网关和可控HTTP上游；创建两名测试成员与Key，验证保存回读、计费/审计，harness结束清理整个schema。
func TestUserBulkBudgetContract(t *testing.T) {
	dep := chatDeployment("bulk-budget-chat")
	dep.ModelInfo["max_input_tokens"] = 1024
	dep.ModelInfo["max_output_tokens"] = 8192
	dep.ModelInfo["supported_parameters"] = []string{"max_tokens"}
	dep.ModelInfo["pricing_source"] = "manual"
	h := newHarness(t, dep)
	admin := h.adminSession()
	a := h.provision(t, admin, "bulk-a")
	b := h.provision(t, admin, "bulk-b")
	ids := []string{a.userID, b.userID}
	read := func(id string) map[string]any {
		t.Helper()
		return h.ok(http.MethodGet, "/user/info?user_id="+id, admin, nil).json()["user_info"].(map[string]any)
	}
	saved := h.ok(http.MethodPost, "/user/bulk_update", admin, bulkBudgetRequest(ids, 10)).json()
	if saved["atomic"] != true || saved["successful_updates"] != float64(2) || saved["failed_updates"] != float64(0) || saved["batch_id"] == "" {
		t.Fatalf("批量响应未反映真实原子结果: %+v", saved)
	}
	for _, id := range ids {
		if read(id)["max_budget"] != float64(10) {
			t.Fatal("保存后逐用户回读不一致")
		}
	}
	for _, tc := range []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"empty", map[string]any{"users": []any{}}, 400}, {"duplicate", bulkBudgetRequest([]string{a.userID, a.userID}, 3), 400},
		{"missing user", bulkBudgetRequest([]string{a.userID, "missing-user"}, 3), 404}, {"negative", bulkBudgetRequest(ids, -1), 400},
		{"blank", bulkBudgetRequest(ids, ""), 400}, {"string", bulkBudgetRequest(ids, "3"), 400},
		{"unsupported role", map[string]any{"users": []any{map[string]any{"user_id": a.userID, "max_budget": 3}, map[string]any{"user_id": b.userID, "max_budget": 3, "user_role": "proxy_admin"}}}, 400},
		{"missing budget", map[string]any{"users": []any{map[string]any{"user_id": a.userID}}}, 400},
		{"unsupported period", map[string]any{"all_users": true, "user_updates": map[string]any{"max_budget": 3, "budget_duration": "1d"}}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := h.do(http.MethodPost, "/user/bulk_update", admin, tc.body)
			if r.status != tc.status {
				t.Fatalf("错误批次状态错误: %s", r.describe())
			}
			for _, id := range ids {
				if read(id)["max_budget"] != float64(10) {
					t.Fatal("失败批次部分保存")
				}
			}
		})
	}
	t.Run("management permission", func(t *testing.T) {
		for _, token := range []string{"", a.session, a.key} {
			r := h.do(http.MethodPost, "/user/bulk_update", token, bulkBudgetRequest(ids, 1))
			if r.status != 401 && r.status != 403 {
				t.Fatalf("非平台管理身份越权: %s", r.describe())
			}
		}
		h.ok(http.MethodPost, "/team/member_update", admin, map[string]any{"team_id": a.teamID, "user_id": a.userID, "role": "admin"})
		if r := h.do(http.MethodPost, "/user/bulk_update", a.session, bulkBudgetRequest(ids, 1)); r.status != 403 {
			t.Fatalf("团队管理员越权: %s", r.describe())
		}
		if err := h.db.SetOrgAdmin(t.Context(), iam.Actor{Kind: "master"}, a.orgID, a.userID, true); err != nil {
			t.Fatal(err)
		}
		if r := h.do(http.MethodPost, "/user/bulk_update", a.session, bulkBudgetRequest(ids, 1)); r.status != 403 {
			t.Fatalf("组织管理员越权: %s", r.describe())
		}
	})
	h.ok(http.MethodPost, "/user/bulk_update", admin, bulkBudgetRequest(ids, 0))
	calls := len(h.upstreamCalls())
	chat := map[string]any{"model": "bulk-budget-chat", "messages": []any{map[string]any{"role": "user", "content": "Verify budget execution."}}, "max_tokens": 8}
	if r := h.do(http.MethodPost, "/v1/chat/completions", a.key, chat); r.status != 403 {
		t.Fatalf("零预算未拦截实际Key调用: %s", r.describe())
	}
	if len(h.upstreamCalls()) != calls {
		t.Fatal("零预算仍调用了上游")
	}
	h.ok(http.MethodPost, "/user/bulk_update", admin, bulkBudgetRequest(ids, 10))
	h.ok(http.MethodPost, "/v1/chat/completions", a.key, chat)
	h.flushSpend()
	spent := read(a.userID)["spend"].(float64)
	if spent <= 0 {
		t.Fatal("恢复预算后实际调用未计费")
	}
	if r := h.do(http.MethodPost, "/user/bulk_update", admin, bulkBudgetRequest(ids, 0)); r.status != 400 {
		t.Fatalf("降低额度穿透已有消费: %s", r.describe())
	}
	if read(b.userID)["max_budget"] != float64(10) {
		t.Fatal("非法混合消费批次修改了另一用户")
	}
	h.ok(http.MethodPost, "/user/bulk_update", admin, bulkBudgetRequest(ids, nil))
	for _, id := range ids {
		if read(id)["max_budget"] != nil {
			t.Fatal("明确null未取消个人上限")
		}
	}
	if read(a.userID)["spend"] != spent {
		t.Fatal("取消上限重置了历史消费")
	}
	audit := h.ok(http.MethodGet, "/audit/logs?action=user.bulk_budget&page_size=100", admin, nil).json()
	rows := rowsOfFrom(audit, "logs", "data", "audit_logs")
	if len(rows) != 8 {
		t.Fatalf("失败批次留下审计/成功遗漏审计: %+v", audit)
	}
	// 显式清理用户，并验证凭据失效；其余组织/审计由独立schema回收，不能碰既有数据。
	h.ok(http.MethodPost, "/team/member_update", admin, map[string]any{"team_id": a.teamID, "user_id": a.userID, "role": "user"})
	for _, id := range ids {
		h.ok(http.MethodPost, "/user/delete", admin, map[string]any{"user_id": id})
	}
	if r := h.do(http.MethodGet, "/v1/models", a.key, nil); r.status != 401 {
		t.Fatalf("删除后Key仍有效: %s", r.describe())
	}
}

// TestUserBulkBudgetAllAndAllocations 验证全部范围包括管理员/停用用户、最终父级分配与硬预占原子约束。
// 前置真实私有网关和三位自建账号；全部更新只作用于本例schema，结束自动清理账户、预占和审计。
func TestUserBulkBudgetAllAndAllocations(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	org := h.ok(http.MethodPost, "/organization/new", admin, map[string]any{"organization_alias": "bulk-org", "max_budget": 100}).json()["organization_id"].(string)
	team := h.ok(http.MethodPost, "/team/new", admin, map[string]any{"team_alias": "bulk-team", "organization_id": org, "max_budget": 100}).json()["team_id"].(string)
	ids := []string{}
	for i, name := range []string{"bulk-first", "bulk-second"} {
		cap := 80
		if i == 1 {
			cap = 20
		}
		u := h.ok(http.MethodPost, "/user/new", admin, map[string]any{"user_email": name + "@example.invalid", "password": "bulk-password", "team_id": team, "max_budget": cap}).json()
		ids = append(ids, u["user_id"].(string))
	}
	changed := map[string]any{"users": []any{map[string]any{"user_id": ids[1], "max_budget": 70}, map[string]any{"user_id": ids[0], "max_budget": 30}}}
	h.ok(http.MethodPost, "/user/bulk_update", admin, changed)
	if _, err := h.db.ReserveBudget(t.Context(), "bulk-regression-hold", "user", ids[0], 10); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{9, 60} {
		r := h.do(http.MethodPost, "/user/bulk_update", admin, bulkBudgetRequest(ids, limit))
		if r.status != 400 || r.json()["error"].(map[string]any)["type"] != "bulk_budget_allocation" {
			t.Fatalf("预占/父级超配没有原子拒绝: %s", r.describe())
		}
	}
	if err := h.db.ReleaseBudget(t.Context(), "bulk-regression-hold"); err != nil {
		t.Fatal(err)
	}
	h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": ids[1], "status": "disabled"})
	all := h.ok(http.MethodPost, "/user/bulk_update", admin, map[string]any{"all_users": true, "user_updates": map[string]any{"max_budget": 25}}).json()
	if all["total_requested"] != float64(3) || all["successful_updates"] != float64(3) {
		t.Fatalf("全部模式漏管理员或停用账户: %+v", all)
	}
	for _, id := range ids {
		u := h.ok(http.MethodGet, "/user/info?user_id="+id, admin, nil).json()["user_info"].(map[string]any)
		if u["max_budget"] != float64(25) {
			t.Fatal("全部保存回读不一致")
		}
	}
	h.ok(http.MethodPost, "/user/bulk_update", admin, map[string]any{"all_users": true, "user_updates": map[string]any{"max_budget": nil}})
}

// TestUserBulkBudgetOverflowJSON 验证真实HTTP浮点溢出不得被当成null取消额度；两名有限额用户供selected/all/mixed正负异常使用。
// 前置隔离网关和数据库；每个错误批次均400、逐用户额度及审计不变，harness结束删除整个schema。
func TestUserBulkBudgetOverflowJSON(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	a := h.provision(t, admin, "bulk-overflow-a")
	b := h.provision(t, admin, "bulk-overflow-b")
	h.ok(http.MethodPost, "/user/bulk_update", admin, bulkBudgetRequest([]string{a.userID, b.userID}, 10))
	for _, number := range []string{"1e309", "-1e309"} {
		for _, scope := range []string{"selected", "all", "mixed"} {
			t.Run(scope+" "+number, func(t *testing.T) {
				raw := fmt.Sprintf(`{"users":[{"user_id":%q,"max_budget":%s}]}`, a.userID, number)
				if scope == "all" {
					raw = fmt.Sprintf(`{"all_users":true,"user_updates":{"max_budget":%s}}`, number)
				}
				if scope == "mixed" {
					raw = fmt.Sprintf(`{"users":[{"user_id":%q,"max_budget":3},{"user_id":%q,"max_budget":%s}]}`, a.userID, b.userID, number)
				}
				r := h.do(http.MethodPost, "/user/bulk_update", admin, json.RawMessage(raw))
				if r.status != 400 || r.json()["error"].(map[string]any)["type"] != "bulk_budget_invalid" {
					t.Fatalf("溢出JSON未拒绝: %s", r.describe())
				}
				for _, id := range []string{a.userID, b.userID} {
					u, err := h.db.GetUser(t.Context(), id)
					if err != nil || u.MaxBudget == nil || *u.MaxBudget != 10 {
						t.Fatalf("错误JSON取消或部分保存额度: %+v %v", u, err)
					}
				}
				count, err := h.db.Engine.Where("action = ?", "user.bulk_budget").Count(new(iam.AuditEntry))
				if err != nil || count != 2 {
					t.Fatalf("异常正文留下审计: %d %v", count, err)
				}
			})
		}
	}
}
