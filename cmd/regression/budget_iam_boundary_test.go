package regression

import (
	"fmt"
	"testing"
)

// TestLegacyIAMNoFalseSuccess 通过真实HTTP证明旧目录入口不能冒充IAM操作；t为隔离数据库测试上下文。
// 前置真实组织/团队/用户及本地上游；匿名和成员权限不变，管理员得到明确501，不写目录或改IAM，schema由harness清理。
func TestLegacyIAMNoFalseSuccess(t *testing.T) {
	h := newHarness(t, chatDeployment("iam-boundary"))
	admin := h.personalBillingSession()
	a := h.provision(t, admin, "legacy-iam")
	org := h.ok("GET", "/organization/info?organization_id="+a.orgID, admin, nil).json()
	team := h.ok("GET", "/team/info?team_id="+a.teamID, admin, nil).json()
	for _, op := range []struct{ method, path string }{
		{"POST", "/team/block"}, {"POST", "/team/unblock"}, {"PATCH", "/team/" + a.teamID},
		{"PATCH", "/organization/member_update"}, {"PATCH", "/v2/organization/" + a.orgID},
		{"POST", "/organization/info"}, {"POST", "/team/model/add"}, {"POST", "/team/bulk_member_add"},
		{"GET", "/team/permissions_list"}, {"GET", "/organization/spend/report"},
		{"GET", "/team/filter/ui"}, {"GET", "/team/metadata_schema"}, {"GET", "/team/spend/report"},
		{"POST", "/team/key/bulk_update"}, {"POST", "/team/model/delete"},
		{"POST", "/team/permissions_bulk_update"}, {"POST", "/team/permissions_update"},
		{"POST", "/team/" + a.teamID + "/disable_logging"},
		{"POST", "/team/" + a.teamID + "/member/" + a.userID + "/reset_spend"},
		{"GET", "/team/" + a.teamID + "/members/me"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			body := map[string]any{"organization_id": a.orgID, "team_id": a.teamID, "user_id": a.userID, "max_budget": 0, "status": "blocked", "role": "admin"}
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {a.session, 403}, {admin, 501}} {
				got := h.do(op.method, op.path, actor.token, body)
				if got.status != actor.status {
					t.Fatalf("旧入口未明确拒绝: %s %s expected=%d %s", op.method, op.path, actor.status, got.describe())
				}
				if actor.status == 501 {
					e, _ := got.json()["error"].(map[string]any)
					if e["type"] != "unsupported_iam_operation" {
						t.Fatal("错误契约不明确", got.describe())
					}
				}
			}
		})
	}
	if got := h.ok("GET", "/organization/info?organization_id="+a.orgID, admin, nil).json(); fmt.Sprint(got) != fmt.Sprint(org) {
		t.Fatal("旧入口修改了真实组织", got)
	}
	if got := h.ok("GET", "/team/info?team_id="+a.teamID, admin, nil).json(); fmt.Sprint(got) != fmt.Sprint(team) {
		t.Fatal("旧入口修改了真实团队", got)
	}
	for _, kind := range []string{"organization", "team"} {
		rows, err := h.store.ListKV(kind)
		if err != nil || len(rows) != 0 {
			t.Fatal("旧接口污染目录", kind, rows, err)
		}
	}
	result := h.ok("POST", "/v1/chat/completions", a.key, map[string]any{"model": "iam-boundary", "messages": []map[string]any{{"role": "user", "content": "Verify legacy calls did not change account access."}}})
	if len(h.upstreamCalls()) != 1 || result.json()["choices"] == nil {
		t.Fatal("旧请求影响推理")
	}
}

// TestBudgetConfigurationBoundary 固定预算元数据CRUD与真实额度隔离，迁移前后用同一HTTP测试验证。
// 前置本地上游和真实租户；配置预算0不等于账号预算0，主IAM更新及零预算阻断仍有效；配置显式删除、账户schema严格清理。
func TestBudgetConfigurationBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("budget-boundary"))
	admin := h.personalBillingSession()
	a := h.provision(t, admin, "budget-config")
	const id = "named-budget"
	for _, actor := range []struct {
		token  string
		status int
	}{{"", 401}, {a.session, 403}} {
		for _, op := range []struct{ method, path string }{{"GET", "/budget/list"}, {"POST", "/budget/new"}, {"POST", "/budget/info"}, {"POST", "/budget/update"}, {"POST", "/budget/delete"}} {
			if got := h.do(op.method, op.path, actor.token, map[string]any{"budget_id": id}); got.status != actor.status {
				t.Fatal("配置权限改变", got.describe())
			}
		}
	}
	created := h.ok("POST", "/budget/new", admin, map[string]any{"budget_id": id, "max_budget": 0, "soft_budget": nil, "rpm_limit": 0, "password": "remove", "skill": map[string]any{"note": "原始说明"}}).json()
	if created["budget_id"] != id || created["max_budget"] != float64(0) || created["password"] != nil || created["note"] != "原始说明" {
		t.Fatal("配置默认改变", created)
	}
	h.ok("POST", "/budget/update", admin, map[string]any{"budget_id": id, "soft_budget": 0, "note": nil})
	read := h.ok("POST", "/budget/info", admin, map[string]any{"budget_id": id}).json()
	if read["soft_budget"] != float64(0) || read["note"] != nil {
		t.Fatal("补丁变化", read)
	}
	if len(rowsOf(h.ok("GET", "/budget/list", admin, nil), "data")) != 1 {
		t.Fatal("预算列表未读回")
	}
	if len(rowsOf(h.ok("GET", "/budgets", admin, nil), "data")) != 0 {
		t.Fatal("单复数命名空间被合并")
	}
	for _, raw := range []string{`[]`, `{"budget_id":`, `{"jsonrpc":"2.0"}`, `{} {}`} {
		if got := catalogRawRequest(t, h, "POST", "/budget/update", admin, raw); got.status != 400 {
			t.Fatal("非法请求未拒绝", got.describe())
		}
	}
	// 只修改真实主接口的名称，不触碰权限/预算规则，回读必须来自IAM而非KV。
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": a.orgID, "organization_alias": "Updated organization"})
	h.ok("POST", "/team/update", admin, map[string]any{"team_id": a.teamID, "team_alias": "Updated team"})
	if got := h.ok("GET", "/organization/info?organization_id="+a.orgID, admin, nil).json(); got["organization_alias"] != "Updated organization" {
		t.Fatal("主组织接口未生效", got)
	}
	if got := h.ok("GET", "/team/info?team_id="+a.teamID, admin, nil).json(); got["team_info"].(map[string]any)["team_alias"] != "Updated team" {
		t.Fatal("主团队接口未生效", got)
	}
	result := h.ok("POST", "/v1/chat/completions", a.key, map[string]any{"model": "budget-boundary", "messages": []map[string]any{{"role": "user", "content": "Verify metadata budget does not become an account limit."}}})
	h.flushSpend()
	cost, ok := floatField(logDetail(t, h, admin, result.header("x-litellm-call-id")), "spend")
	if !ok || cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("配置改变真实推理/费用")
	}
	for _, scope := range []string{"organization", "team"} {
		blocked := h.provision(t, admin, "zero-"+scope)
		if scope == "organization" {
			h.setOrgBudget(t, admin, blocked.orgID, 0)
		} else {
			h.ok("POST", "/team/update", admin, map[string]any{"team_id": blocked.teamID, "max_budget": 0})
		}
		before := len(h.upstreamCalls())
		denied := h.do("POST", "/v1/chat/completions", blocked.key, map[string]any{"model": "budget-boundary", "messages": []map[string]any{{"role": "user", "content": "Verify real zero budget enforcement."}}})
		if denied.status != 403 || len(h.upstreamCalls()) != before {
			t.Fatal("真实零预算规则改变", scope, denied.describe())
		}
	}
	if h.ok("POST", "/budget/delete", admin, map[string]any{"budget_id": id}).json()["deleted"] != float64(1) {
		t.Fatal("删除未生效")
	}
	if got := h.do("POST", "/budget/info", admin, map[string]any{"budget_id": id}); got.status != 404 {
		t.Fatal("删除残留")
	}
	h.ok("POST", "/budget/update", admin, map[string]any{"budget_id": id, "note": "upsert"})
	h.ok("POST", "/budget/new", admin, map[string]any{"budget_id": id, "note": "replacement"})
	if h.ok("POST", "/budget/info", admin, map[string]any{"budget_id": id}).json()["note"] != "replacement" {
		t.Fatal("旧覆盖语义改变")
	}
	h.ok("POST", "/budget/delete", admin, map[string]any{"budget_id": id})
	if len(rowsOf(h.ok("GET", "/budget/list", admin, nil), "data")) != 0 {
		t.Fatal("配置未清理")
	}
}
