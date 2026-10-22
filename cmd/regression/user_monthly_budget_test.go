package regression

import (
	"github.com/sunqirui1987/xhub/internal/iam"
	"net/http"
	"testing"
	"time"
)

// TestUserMonthlyBudgetContract 验证真实HTTP创建、日期单独批量保存、错误回滚及鉴权，月度刷新后Key恢复调用。
// 前置私有PG与可控上游；不接生产，harness清理全部用户、Key、审计和合成账单。
func TestUserMonthlyBudgetContract(t *testing.T) {
	h := newHarness(t, chatDeployment("monthly-chat"))
	admin := h.adminSession()
	a := h.provision(t, admin, "monthly")
	read := func() map[string]any {
		t.Helper()
		return h.ok(http.MethodGet, "/user/info?user_id="+a.userID, admin, nil).json()["user_info"].(map[string]any)
	}
	h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": a.userID, "max_budget": 10, "budget_reset_day": 31})
	first := read()
	if first["budget_reset_day"] != float64(31) || first["budget_reset_at"] == nil {
		t.Fatal("周期未持久化", first)
	}
	for _, bad := range []any{-1, 32, 1.5, "1", nil} {
		r := h.do(http.MethodPost, "/user/bulk_update", admin, map[string]any{"users": []any{map[string]any{"user_id": a.userID, "max_budget": 20, "budget_reset_day": bad}}})
		if r.status != 400 || read()["max_budget"] != float64(10) {
			t.Fatalf("非法周期部分保存: %s", r.describe())
		}
	}
	// 通用夹具默认授予组织管理权，先撤销再验证普通成员不可自行调整周期。
	if err := h.db.SetOrgAdmin(t.Context(), iam.Actor{Kind: "master"}, a.orgID, a.userID, false); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{a.session, a.key} {
		r := h.do(http.MethodPost, "/user/update", token, map[string]any{"user_id": a.userID, "budget_reset_day": 0})
		if r.status != 401 && r.status != 403 {
			t.Fatalf("个人周期越权: %s", r.describe())
		}
	}
	// 已消费完时仍允许管理员只改周期，但不提前恢复额度。
	if _, err := h.db.Engine.Exec("UPDATE users SET spend=10 WHERE id=?", a.userID); err != nil {
		t.Fatal(err)
	}
	h.ok(http.MethodPost, "/user/bulk_update", admin, map[string]any{"users": []any{map[string]any{"user_id": a.userID, "budget_reset_day": 1}}})
	if got := read(); got["spend"] != float64(10) || got["max_budget"] != float64(10) || got["budget_reset_day"] != float64(1) {
		t.Fatal("周期独立保存改变额度", got)
	}
	chat := map[string]any{"model": "monthly-chat", "messages": []any{map[string]any{"role": "user", "content": "Check monthly allowance."}}, "max_tokens": 8}
	if r := h.do(http.MethodPost, "/v1/chat/completions", a.key, chat); r.status != 429 {
		t.Fatalf("刷新前余额为零应拒绝: %s", r.describe())
	}
	// 仅调整本测试用户的到期时间，真实准入触发刷新，不能以mock绕开数据面。
	if _, err := h.db.Engine.Exec("UPDATE users SET budget_reset_at=? WHERE id=?", time.Now().AddDate(0, -1, 0), a.userID); err != nil {
		t.Fatal(err)
	}
	h.ok(http.MethodPost, "/v1/chat/completions", a.key, chat)
	h.flushSpend()
	if got := read(); got["spend"].(float64) <= 0 || got["spend"].(float64) >= 10 {
		t.Fatal("刷新后实际费用未正确记录", got)
	}
	before := read()["spend"]
	h.ok(http.MethodPost, "/user/bulk_update", admin, map[string]any{"all_users": true, "user_updates": map[string]any{"budget_reset_day": 0}})
	if got := read(); got["budget_reset_day"] != float64(0) || got["budget_reset_at"] != nil || got["spend"] != before {
		t.Fatal("取消刷新修改消费", got)
	}
	created := h.ok(http.MethodPost, "/user/new", admin, map[string]any{"user_email": "monthly-import@example.invalid", "password": "monthly-password", "budget_reset_day": 15}).json()
	u := h.ok(http.MethodGet, "/user/info?user_id="+created["user_id"].(string), admin, nil).json()["user_info"].(map[string]any)
	if u["budget_reset_day"] != float64(15) {
		t.Fatal("导入共用创建接口遗漏周期")
	}
	h.ok(http.MethodPost, "/user/delete", admin, map[string]any{"user_id": created["user_id"]})
}
