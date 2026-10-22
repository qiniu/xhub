package iam

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestUserBudgetCalendar 验证北京时间、年界、闰年和月末；纯内存输入，无持久资源需清理。
func TestUserBudgetCalendar(t *testing.T) {
	for _, tc := range []struct {
		now         string
		day         int
		start, next string
	}{
		{"2026-01-31T16:00:00Z", 1, "2026-01-31T16:00:00Z", "2026-02-28T16:00:00Z"},
		{"2026-02-27T15:59:59Z", 31, "2026-01-30T16:00:00Z", "2026-02-27T16:00:00Z"},
		{"2026-02-27T16:00:00Z", 31, "2026-02-27T16:00:00Z", "2026-03-30T16:00:00Z"},
		{"2028-02-28T16:00:00Z", 30, "2028-02-28T16:00:00Z", "2028-03-29T16:00:00Z"},
		{"2026-12-31T16:00:00Z", 15, "2026-12-14T16:00:00Z", "2027-01-14T16:00:00Z"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		start, next := userBudgetWindow(now, tc.day)
		if start.Format(time.RFC3339) != tc.start || next.Format(time.RFC3339) != tc.next {
			t.Fatalf("自然月边界错误 %+v: %v %v", tc, start, next)
		}
	}
	now := time.Now()
	u := User{Spend: 12}
	for _, day := range []int{-1, 32} {
		if !errors.Is(configureUserBudget(&u, day, now), ErrInvalid) {
			t.Fatal("非法日期被接受")
		}
	}
	if err := configureUserBudget(&u, 31, now); err != nil {
		t.Fatal(err)
	}
	next := *u.BudgetResetAt
	if err := configureUserBudget(&u, 31, now.AddDate(0, 1, 0)); err != nil || !u.BudgetResetAt.Equal(next) || u.Spend != 12 {
		t.Fatal("重复保存改变消费或推迟刷新")
	}
	u.BudgetPeriodStart = &now
	if err := configureUserBudget(&u, 0, now); err != nil || u.BudgetResetAt != nil || u.Spend != 12 || u.BudgetPeriodStart == nil {
		t.Fatal("取消刷新改变已有周期或消费")
	}
}

// TestUserBudgetResetPersistence 验证私有PG中并发补执行、审计去重、迟到消费、批量日期独立保存及回滚。
// testDB清理专属schema，无真实用户、外部模型或生产数据依赖。
func TestUserBudgetResetPersistence(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	day := 31
	u, err := db.CreateUser(ctx, Actor{}, UserInput{Email: "monthly@example.invalid", Password: "test-password", MaxBudget: quotaLimit(10), BudgetResetDay: &day})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().AddDate(0, 3, 0).UTC()
	start, next := userBudgetWindow(now, day)
	if _, err = db.Engine.Exec("UPDATE users SET spend=12,budget_reset_at=? WHERE id=?", now.AddDate(0, -2, 0), u.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.ResetUserBudgets(ctx, now); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := db.GetUser(ctx, u.ID)
	if err != nil || got.Spend != 0 || got.BudgetPeriodStart == nil || !got.BudgetPeriodStart.Equal(start) || !got.BudgetResetAt.Equal(next) {
		t.Fatalf("补执行失败: %+v %v", got, err)
	}
	count, err := db.Engine.Where("action=? AND object_id=?", "user.budget_reset", u.ID).Count(&AuditEntry{})
	if err != nil || count != 1 {
		t.Fatalf("并发重复刷新: %d %v", count, err)
	}
	for i, ts := range []time.Time{start.Add(-time.Second), start} {
		id := "old"
		if i == 1 {
			id = "current"
		}
		r := usageRecord(id, "", 2)
		r.UserID = u.ID
		r.TS = ts
		for range 2 {
			if err := db.RecordUsage(ctx, []UsageRecord{r}); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, _ = db.GetUser(ctx, u.ID)
	if got.Spend != 2 {
		t.Fatalf("迟到/重放污染新周期: %v", got.Spend)
	}
	day = 15
	result, err := db.AdminBulkUserBudgets(ctx, Actor{}, BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: u.ID, KeepBudget: true, BudgetResetDay: &day}}})
	if err != nil || result.Users[0].Spend != 2 || *result.Users[0].MaxBudget != 10 || result.Users[0].BudgetResetDay != 15 {
		t.Fatalf("周期独立更新失败: %+v %v", result, err)
	}
	day = 0
	_, err = db.AdminBulkUserBudgets(ctx, Actor{}, BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: u.ID, KeepBudget: true, BudgetResetDay: &day}, {UserID: "missing", KeepBudget: true, BudgetResetDay: &day}}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	got, _ = db.GetUser(ctx, u.ID)
	if got.BudgetResetDay != 15 {
		t.Fatal("失败批次没有回滚")
	}
	if _, err = db.AdminUpdateUser(ctx, Actor{}, u.ID, UserUpdate{BudgetResetDay: &day}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetUser(ctx, u.ID)
	if got.Spend != 2 || got.BudgetResetAt != nil || got.BudgetPeriodStart == nil {
		t.Fatal("取消周期清零消费或边界")
	}
}

// TestUserBudgetUpgrade 验证旧库补列默认不刷新且重复迁移保留消费；私有schema由testDB回收。
func TestUserBudgetUpgrade(t *testing.T) {
	db := testDB(t)
	u, err := db.CreateUser(t.Context(), Actor{}, UserInput{Email: "legacy@example.invalid", Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Engine.Exec("UPDATE users SET spend=4 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Engine.Exec("ALTER TABLE users DROP COLUMN budget_reset_day, DROP COLUMN budget_reset_at, DROP COLUMN budget_period_start"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = db.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.GetUser(t.Context(), u.ID)
	if err != nil || got.Spend != 4 || got.BudgetResetDay != 0 || got.BudgetResetAt != nil || got.BudgetPeriodStart != nil {
		t.Fatalf("升级破坏已有用户: %+v %v", got, err)
	}
}

// TestUserBudgetResetRollback 验证审计写入失败时消费和周期全部回滚；隔离PG中临时约束由schema清理。
func TestUserBudgetResetRollback(t *testing.T) {
	db := testDB(t)
	day := 1
	u, err := db.CreateUser(t.Context(), Actor{}, UserInput{Email: "rollback@example.invalid", Password: "test-password", BudgetResetDay: &day})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().AddDate(0, 2, 0)
	due := time.Now().AddDate(0, 1, 0)
	if _, err = db.Engine.Exec("UPDATE users SET spend=7,budget_reset_at=? WHERE id=?", due, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Engine.Exec("ALTER TABLE audit_logs ADD CONSTRAINT reject_user_reset CHECK (action <> 'user.budget_reset')"); err != nil {
		t.Fatal(err)
	}
	if err = db.ResetUserBudgets(t.Context(), now); err == nil {
		t.Fatal("审计失败应回滚刷新")
	}
	got, err := db.GetUser(t.Context(), u.ID)
	if err != nil || got.Spend != 7 || got.BudgetPeriodStart != nil {
		t.Fatalf("刷新未回滚: %+v %v", got, err)
	}
}

// TestUserBudgetLegacyHolds 验证历史预占按个人周期隔离且不丢弃未知账单；隔离PG中模拟边界，testDB清理全部记录。
func TestUserBudgetLegacyHolds(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	u, err := db.CreateUser(ctx, Actor{}, UserInput{Email: "holds@example.invalid", Password: "test-password", MaxBudget: quotaLimit(10)})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Microsecond)
	old, err := db.ReserveBudget(ctx, "old-user-hold", "user", u.ID, 10, BudgetReservationOptions{CallID: "old", StartedAt: start})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetBudgetReservationState(ctx, old.RequestID, "uncertain"); err != nil {
		t.Fatal(err)
	}
	boundary := start.Add(time.Second)
	if _, err = db.Engine.Exec("UPDATE users SET budget_period_start=? WHERE id=?", boundary, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ReserveBudget(ctx, "new-user-hold", "user", u.ID, 10, BudgetReservationOptions{CallID: "new", StartedAt: boundary}); err != nil {
		t.Fatalf("旧周期占用封锁新个人周期: %v", err)
	}
	if _, err = db.ReserveBudget(ctx, "over-user-hold", "user", u.ID, 1, BudgetReservationOptions{CallID: "over", StartedAt: boundary}); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("新周期预占未计入: %v", err)
	}
}
