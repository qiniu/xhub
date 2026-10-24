package regression

import (
	"testing"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestKeyContractOrganizationFilter 验证真实 HTTP 组织筛选、组合过滤和分页契约，并证明权限与推理预算不变。
// 前置两个隔离租户和无团队用户；所有请求经过真实鉴权/PG，显式撤销 Key，harness 最终清理 schema 与上游。
func TestKeyContractOrganizationFilter(t *testing.T) {
	c := newKeyContractClient(t)
	h := c.h
	a := h.provision(t, c.admin, "org-filter-a")
	b := h.provision(t, c.admin, "org-filter-b")
	if err := h.db.SetOrgAdmin(t.Context(), iam.Actor{Kind: "master"}, a.orgID, a.userID, false); err != nil {
		t.Fatal(err)
	}
	second := c.call("POST", "/key/generate", c.admin, map[string]any{"user_id": a.userID, "key_alias": "second-filter", "max_budget": 10}, 200, true).json()
	id := firstString(second, "token_id")
	// 创建团队会为管理员建立成员关系，必须单独创建无团队账户，不能把管理员误当无团队夹具。
	teamless := h.ok("POST", "/user/new", c.admin, map[string]any{"user_email": "teamless-filter@example.com", "password": "teamless-filter-password"}).json()
	teamlessSession := h.login("teamless-filter@example.com", "teamless-filter-password")
	teamlessKey := c.call("POST", "/key/generate", c.admin, map[string]any{"user_id": firstString(teamless, "user_id"), "key_alias": "teamless-filter"}, 200, true).json()
	for _, tc := range []struct {
		name, token, query string
		count, rows        int
	}{
		{"missing organization", a.session, "scope=personal&organization_id=missing-audit-org", 0, 0},
		{"matching organization", a.session, "scope=personal&organization_id=" + a.orgID, 2, 2},
		{"other organization", a.session, "scope=personal&organization_id=" + b.orgID, 0, 0},
		{"empty organization", a.session, "scope=personal&organization_id=", 2, 2},
		{"teamless excluded", teamlessSession, "scope=personal&organization_id=" + a.orgID, 0, 0},
		{"teamless unfiltered", teamlessSession, "scope=personal&organization_id=", 1, 1},
		{"admin management", c.admin, "organization_id=" + a.orgID, 2, 2},
		{"ordinary scope", a.session, "organization_id=" + b.orgID, 0, 0},
		{"team conflict", a.session, "scope=personal&organization_id=" + a.orgID + "&team_id=" + b.teamID, 0, 0},
		{"search", a.session, "scope=personal&organization_id=" + a.orgID + "&search=second-filter", 1, 1},
		{"key id", a.session, "scope=personal&organization_id=" + a.orgID + "&key_hash=" + id, 1, 1},
		{"first page", a.session, "scope=personal&organization_id=" + a.orgID + "&size=1&page=1", 2, 1},
		{"second page", a.session, "scope=personal&organization_id=" + a.orgID + "&size=1&page=2", 2, 1},
		{"past last page", a.session, "scope=personal&organization_id=" + a.orgID + "&size=1&page=3", 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub := *c
			sub.t = t
			r := sub.call("GET", "/key/list?"+tc.query, tc.token, nil, 200, true)
			if r.status != 200 || r.json()["total_count"] != float64(tc.count) || len(rowsOf(r, "keys")) != tc.rows {
				t.Fatalf("组织筛选/分页不符: %s", r.describe())
			}
			for _, row := range rowsOf(r, "keys") {
				if tc.token == a.session && row["user_id"] != a.userID {
					t.Fatalf("筛选扩大权限范围: %v", row["user_id"])
				}
			}
		})
	}
	c.call("GET", "/key/list?scope=personal&organization_id="+a.orgID, a.session, nil, 200, true)
	c.call("GET", "/key/list?organization_id="+a.orgID+"&size=0", a.session, nil, 400, false)
	c.call("GET", "/key/list?organization_id="+a.orgID, "", nil, 401, false)
	before := c.call("GET", "/key/info?key="+id, a.session, nil, 200, true).json()["info"].(map[string]any)
	if before["max_budget"] != float64(10) || before["spend"] != float64(0) {
		t.Fatalf("查询修改了预算: %v", before)
	}
	r := h.ok("POST", "/v1/chat/completions", firstString(second, "key"), chatRequest("key-contract-chat", "Reply with a short greeting."))
	if len(rowsOf(r, "choices")) == 0 || len(h.upstreamCalls()) == 0 {
		t.Fatal("组织筛选后密钥不能实际推理")
	}
	h.flushSpend()
	c.call("POST", "/key/delete", c.admin, map[string]any{"keys": []string{id, a.key, b.key, firstString(teamlessKey, "token_id")}}, 200, true)
	if r := h.do("POST", "/v1/chat/completions", firstString(second, "key"), chatRequest("key-contract-chat", "Reply with a short greeting.")); r.status != 401 {
		t.Fatalf("已清理 Key 仍可调用: %s", r.describe())
	}
}

// TestKeyContractOrganizationQueryFailure 验证组织关联查询失败必须返回契约错误而非成功空列表。
// 前置私有 PG 和一把 Key，通过重命名隔离表注入 SQL 故障；t.Cleanup 先恢复表，harness 再清理 schema。
func TestKeyContractOrganizationQueryFailure(t *testing.T) {
	c := newKeyContractClient(t)
	c.call("POST", "/key/generate", c.admin, map[string]any{"key_alias": "query-failure"}, 200, true)
	catalogSQL(t, c.h, `ALTER TABLE teams RENAME TO teams_org_filter_fault`)
	t.Cleanup(func() { catalogSQL(t, c.h, `ALTER TABLE teams_org_filter_fault RENAME TO teams`) })
	c.call("GET", "/key/list?organization_id=missing-org", c.admin, nil, 500, true)
}
