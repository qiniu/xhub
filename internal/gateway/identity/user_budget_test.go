package identity

import "testing"

// TestUserResetDayContract 验证日期严格整数契约和独立批量变更；纯解析无持久资源。
func TestUserResetDayContract(t *testing.T) {
	for _, v := range []any{nil, "1", true, float64(-1), float64(32), 1.5} {
		if _, err := userResetDay(map[string]any{"budget_reset_day": v}); err == nil {
			t.Fatalf("非法日期被接受: %v", v)
		}
	}
	for _, v := range []float64{0, 1, 31} {
		d, err := userResetDay(map[string]any{"budget_reset_day": v})
		if err != nil || d == nil || *d != int(v) {
			t.Fatal("合法日期被拒绝", v)
		}
		u, err := bulkUserChanges(map[string]any{"budget_reset_day": v})
		if err != nil || !u.KeepBudget || u.BudgetResetDay == nil {
			t.Fatal("日期独立更新清空预算")
		}
	}
	if d, err := userResetDay(map[string]any{}); err != nil || d != nil {
		t.Fatal("缺省未保持")
	}
	if _, err := bulkUserChanges(map[string]any{}); err == nil {
		t.Fatal("空更新被接受")
	}
	if _, err := bulkUserBudgetFields(map[string]any{"all_users": true, "user_updates": map[string]any{"user_id": "wrong", "budget_reset_day": float64(1)}}); err == nil {
		t.Fatal("全部模式接受目标混用")
	}
}
