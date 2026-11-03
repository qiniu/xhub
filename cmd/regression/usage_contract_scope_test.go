package regression

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/sunqirui1987/xhub/contracts/usage"
)

// TestUsageContractScope 验证两个真实组织/团队普通用户的日报和同名会话隔离；真实HTTP/PG/本地上游产生不同账单。
// 断言合法范围、混合范围、范围外拒绝、匿名拒绝、分页内容和契约；不修改权限/预算实现，harness清理隔离schema。
func TestUsageContractScope(t *testing.T) {
	h := newHarness(t, chatDeployment("usage-scope-chat"))
	admin := h.adminSession()
	a, b := h.provision(t, admin, "scope-a"), h.provision(t, admin, "scope-b")
	v, err := usage.NewValidator(usage.Document)
	if err != nil {
		t.Fatal(err)
	}
	// 两个账号故意复用相同session_id；必须按实际身份过滤而不是按字符串直接共享历史。
	ids := map[string]string{}
	for _, tn := range []provisionedTenant{a, b} {
		body := chatRequest("usage-scope-chat", "Verify isolated history.")
		body["session_id"] = "shared-contract-session"
		r := h.ok("POST", "/v1/chat/completions", tn.key, body)
		ids[tn.userID] = r.headers.Get("x-litellm-call-id")
		if ids[tn.userID] == "" {
			t.Fatal("真实调用缺少日志ID")
		}
	}
	h.flushSpend()
	// call 检查真实返回与OpenAPI，不能仅检查200；调用者随后断言数据归属，harness负责清理。
	call := func(path, token string, status int) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if err := v.Request(req); err != nil {
			t.Fatalf("请求契约 %s: %v", path, err)
		}
		r := h.do("GET", path, token, nil)
		if r.status != status {
			t.Fatalf("%s want=%d: %s", path, status, r.describe())
		}
		if err := v.Response(req, r.status, r.headers, r.body); err != nil {
			t.Fatalf("响应契约 %s: %v", path, err)
		}
		return r.json()
	}
	for _, tn := range []provisionedTenant{a, b} {
		other := b
		if tn.userID == b.userID {
			other = a
		}
		for _, route := range []struct{ path, param, own, foreign string }{
			{"/user/daily/activity", "user_id", tn.userID, other.userID},
			{"/user/daily/activity/aggregated", "user_id", tn.userID, other.userID},
			{"/team/daily/activity", "team_ids", tn.teamID, other.teamID},
			{"/team/daily/activity/aggregated", "team_ids", tn.teamID, other.teamID},
			{"/organization/daily/activity", "organization_ids", tn.orgID, other.orgID},
		} {
			own := route.path + "?" + route.param + "=" + url.QueryEscape(route.own)
			result := call(own, tn.session, 200)
			if result["metadata"].(map[string]any)["total_api_requests"] != float64(1) {
				t.Fatalf("%s混入其他账号用量: %+v", own, result)
			}
			// user_id在现有查询中与授权范围取交集，范围外返回200空统计；团队/组织列表则明确404。
			// 锁定实际规则，不为了测试一致而修改生产权限或预算。
			foreign := route.path + "?" + route.param + "=" + url.QueryEscape(route.foreign)
			if route.param == "user_id" {
				empty := call(foreign, tn.session, 200)
				if empty["metadata"].(map[string]any)["total_api_requests"] != float64(0) {
					t.Fatal("范围外用户泄露用量")
				}
			} else {
				call(foreign, tn.session, 404)
			}
			call(own, "", 401)
			if route.param != "user_id" {
				mixed := call(route.path+"?"+route.param+"="+url.QueryEscape(route.own+","+route.foreign), tn.session, 200)
				if mixed["metadata"].(map[string]any)["total_api_requests"] != float64(1) {
					t.Fatalf("混合范围扩大权限: %s", route.path)
				}
			}
		}
		for _, path := range []string{"/spend/logs/ui", "/spend/logs/v2", "/spend/logs/session/ui?session_id=shared-contract-session"} {
			result := call(path, tn.session, 200)
			rows := result["data"].([]any)
			if len(rows) != 1 {
				t.Fatalf("%s返回%d条，预期自己的1条", path, len(rows))
			}
			row := rows[0].(map[string]any)
			if row["user"] != tn.userID || row["request_id"] != ids[tn.userID] {
				t.Fatalf("日志串号: %s %+v", path, row)
			}
		}
		call("/spend/logs/ui/"+ids[other.userID], tn.session, 404)
	}
	// 非空会话必须鉴权；管理员能看到两条并且分页无重复，不以空session_id快捷返回冒充成功。
	call("/spend/logs/session/ui?session_id=shared-contract-session", "", 401)
	seen := map[string]bool{}
	for page := 1; page <= 2; page++ {
		result := call(fmt.Sprintf("/spend/logs/session/ui?session_id=shared-contract-session&page_size=1&page=%d", page), admin, 200)
		rows := result["data"].([]any)
		if result["total"] != float64(2) || len(rows) != 1 {
			t.Fatalf("会话分页不正确: %+v", result)
		}
		id := rows[0].(map[string]any)["request_id"].(string)
		if seen[id] {
			t.Fatal("分页重复日志")
		}
		seen[id] = true
	}
	if !seen[ids[a.userID]] || !seen[ids[b.userID]] {
		t.Fatal("管理员分页丢失实际请求")
	}
}
