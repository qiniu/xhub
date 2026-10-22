package regression

import (
	"net/http"
	"net/url"
	"testing"
)

// TestTeamSearchContract 使用真实后台与隔离数据库验证搜索、分页和租户边界；harness 在结束时删除所有测试数据。
func TestTeamSearchContract(t *testing.T) {
	h := newHarness(t, chatDeployment("team-search"))
	admin := h.adminSession()
	a := h.provision(t, admin, "search-a")
	b := h.provision(t, admin, "search-b")
	h.ok(http.MethodPost, "/team/update", admin, map[string]any{"team_id": a.teamID, "team_alias": "研发 Alpha"})
	h.ok(http.MethodPost, "/team/update", admin, map[string]any{"team_id": b.teamID, "team_alias": "研发 Beta"})
	for _, tc := range []struct {
		name, search, mode, session string
		count                       int
	}{
		{"中文", "研发", "", admin, 2}, {"大小写空白", " ALPHA ", "", admin, 1},
		{"不存在", "missing", "", admin, 0}, {"清空", "", "", admin, 2},
		{"ID完整", a.teamID, "exact", admin, 1}, {"ID精确非前缀", a.teamID[:8], "exact", admin, 0},
		{"ID前缀", a.teamID[:8], "prefix", admin, 1},
		{"不能跨租户", b.teamID, "prefix", a.session, 0}, {"名称不跨租户", "研发", "", a.session, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/v2/team/list?page_size=1&search=" + url.QueryEscape(tc.search) + "&search_team_id_match=" + tc.mode
			got := h.ok(http.MethodGet, path, tc.session, nil)
			rows := rowsOf(got, "teams")
			wantRows := tc.count
			if wantRows > 1 {
				wantRows = 1
			}
			if int(firstFloat(got.json(), "total")) != tc.count || len(rows) != wantRows {
				t.Fatalf("过滤后总数或分页错误: %s", got.describe())
			}
			if tc.count == 2 {
				next := h.ok(http.MethodGet, path+"&page=2", tc.session, nil)
				nextRows := rowsOf(next, "teams")
				if len(nextRows) != 1 || firstString(rows[0], "team_id") == firstString(nextRows[0], "team_id") {
					t.Fatalf("搜索跨页重复或缺失: %s", next.describe())
				}
			}
		})
	}
	legacy := h.ok(http.MethodGet, "/team/list?search=Alpha", admin, nil)
	if len(rowsOf(legacy, "teams", "data")) != 1 {
		t.Fatalf("旧列表搜索不一致: %s", legacy.describe())
	}
	for _, path := range []string{"/team/list", "/v2/team/list"} {
		got := h.do(http.MethodGet, path+"?search_team_id_match=invalid", admin, nil)
		if got.status != 400 {
			t.Fatalf("非法模式应返回400: %s", got.describe())
		}
		got = h.do(http.MethodGet, path+"?search=Alpha", "", nil)
		if got.status != 401 {
			t.Fatalf("匿名搜索应拒绝: %s", got.describe())
		}
	}
	h.ok(http.MethodPost, "/team/delete", admin, map[string]any{"team_id": b.teamID})
	remaining := h.ok(http.MethodGet, "/v2/team/list?search="+url.QueryEscape("研发"), admin, nil)
	if firstFloat(remaining.json(), "total") != 1 {
		t.Fatalf("删除后搜索总数未更新: %s", remaining.describe())
	}
}
