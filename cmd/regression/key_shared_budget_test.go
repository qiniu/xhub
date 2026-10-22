package regression

import (
	"net/http"
	"testing"
)

// TestSharedPersonalKeyBudgetHTTP 真实路由验证固定和空预算 Key、会话共用个人余额且创建不消费。
// 前置独立租户、PostgreSQL 和本地上游；覆盖字段契约、非法值回滚、0/个人耗尽拒绝，harness 自动回收 schema。
func TestSharedPersonalKeyBudgetHTTP(t *testing.T) {
	h := newHarness(t, chatDeployment("shared-budget"))
	admin := h.adminSession()
	u := h.provision(t, admin, "shared-budget")
	h.setUserBudget(t, admin, u.userID, 500)
	fixed := h.ok(http.MethodPost, "/key/generate", u.session, map[string]any{"key_alias": "fixed", "max_budget": 500}).json()
	blank := h.ok(http.MethodPost, "/key/generate", u.session, map[string]any{"key_alias": "blank", "max_budget": nil}).json()
	omitted := h.ok(http.MethodPost, "/key/generate", u.session, map[string]any{"key_alias": "omitted"}).json()
	for _, key := range []map[string]any{blank, omitted} {
		if key["max_budget"] != nil {
			t.Fatalf("空预算必须持久化为 null: %v", key["max_budget"])
		}
	}
	info := h.ok(http.MethodGet, "/v2/user/info", u.session, nil).json()["user_info"].(map[string]any)
	if info["max_budget"] != float64(500) || info["spend"] != float64(0) {
		t.Fatalf("创建 Key 改变个人额度: %v", info)
	}
	for _, token := range []string{firstString(fixed, "key"), firstString(blank, "key"), firstString(omitted, "key"), u.session} {
		h.ok(http.MethodPost, "/v1/chat/completions", token, chatRequest("shared-budget", "Verify shared personal budget"))
	}
	h.flushSpend()
	account, err := h.gw.Identity().GetUser(t.Context(), u.userID)
	if err != nil || account.Spend <= 0 || account.Spend >= 500 {
		t.Fatalf("消费未汇总到个人: %+v %v", account, err)
	}
	zero := h.ok(http.MethodPost, "/key/generate", u.session, map[string]any{"key_alias": "zero", "max_budget": 0}).json()
	response := h.do(http.MethodPost, "/v1/chat/completions", firstString(zero, "key"), chatRequest("shared-budget", "Blocked key"))
	if response.status != 403 {
		t.Fatalf("零额度 Key 应拒绝: %s", response.describe())
	}
	h.ok(http.MethodPost, "/v1/chat/completions", firstString(blank, "key"), chatRequest("shared-budget", "Other key remains available"))
	if response = h.do(http.MethodPost, "/key/generate", u.session, map[string]any{"max_budget": -1}); response.status != 400 {
		t.Fatalf("负额度必须拒绝: %s", response.describe())
	}
	h.flushSpend()
	if _, err = h.gw.Identity().Engine.Exec("UPDATE users SET spend=501 WHERE id=?", u.userID); err != nil {
		t.Fatal(err)
	}
	// 余额耗尽仍允许管理空预算 Key，但新凭据和会话都不能绕过个人上限。
	exhausted := h.ok(http.MethodPost, "/key/generate", u.session, map[string]any{"key_alias": "after-exhaustion"}).json()
	for _, token := range []string{firstString(exhausted, "key"), firstString(blank, "key"), firstString(fixed, "key"), u.session} {
		response = h.do(http.MethodPost, "/v1/chat/completions", token, chatRequest("shared-budget", "Personal budget exhausted"))
		if response.status != 403 {
			t.Fatalf("个人超支应拦截所有入口: %s", response.describe())
		}
	}
}
