package regression

import (
	"net/http"
	"testing"
)

// TestConsoleDeleteContract 验证控制台单值删除契约、空ID及越权拒绝和实际持久化结果。
// 前置独立数据库和合成管理员/普通用户；只操作本例资源，harness在结束后回收整个schema。
func TestConsoleDeleteContract(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	org := h.ok(http.MethodPost, "/organization/new", admin, map[string]any{"organization_alias": "delete-contract"}).json()["organization_id"].(string)
	team := h.ok(http.MethodPost, "/team/new", admin, map[string]any{"organization_id": org, "team_alias": "delete-contract"}).json()["team_id"].(string)
	user := h.ok(http.MethodPost, "/user/new", admin, map[string]any{"user_email": "delete-contract@example.invalid", "user_role": "user", "password": "delete-contract-password"}).json()["user_id"].(string)
	h.ok(http.MethodPost, "/user/new", admin, map[string]any{"user_email": "delete-actor@example.invalid", "user_role": "user", "password": "delete-contract-password"})
	session := h.login("delete-actor@example.invalid", "delete-contract-password")
	for _, tc := range []struct{ kind, id string }{{"user", user}, {"team", team}} {
		r := h.do(http.MethodPost, "/"+tc.kind+"/delete", admin, map[string]any{tc.kind + "_id": ""})
		if r.status != 400 {
			t.Fatalf("空%s ID应拒绝: %s", tc.kind, r.describe())
		}
		r = h.do(http.MethodPost, "/"+tc.kind+"/delete", "", map[string]any{tc.kind + "_id": tc.id})
		if r.status != 401 {
			t.Fatalf("未登录删除%s应拒绝: %s", tc.kind, r.describe())
		}
		r = h.do(http.MethodPost, "/"+tc.kind+"/delete", session, map[string]any{tc.kind + "_id": tc.id})
		if r.status != 403 {
			t.Fatalf("普通用户删除%s应拒绝: %s", tc.kind, r.describe())
		}
		h.ok(http.MethodGet, "/"+tc.kind+"/info?"+tc.kind+"_id="+tc.id, admin, nil)
		h.ok(http.MethodPost, "/"+tc.kind+"/delete", admin, map[string]any{tc.kind + "_id": tc.id})
		r = h.do(http.MethodGet, "/"+tc.kind+"/info?"+tc.kind+"_id="+tc.id, admin, nil)
		if r.status != 404 {
			t.Fatalf("删除%s后不应仍能查询: %s", tc.kind, r.describe())
		}
	}
}
