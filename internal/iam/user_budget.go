package iam

import (
	"context"
	"time"
	"xorm.io/xorm"
)

// userBudgetLocation 固定个人预算的自然月边界为北京时间，不受服务器时区影响。
var userBudgetLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// userBudgetBoundary 返回指定月份的刷新时刻；day 已由调用方校验为1至31，短月取最后一天，结果为UTC。
func userBudgetBoundary(year int, month time.Month, day int) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, userBudgetLocation).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, userBudgetLocation).UTC()
}

// userBudgetWindow 按当前时间和合法刷新日返回本周期起点及下一次刷新；用于配置和停机补执行，无副作用。
func userBudgetWindow(now time.Time, day int) (time.Time, time.Time) {
	local := now.In(userBudgetLocation)
	start := userBudgetBoundary(local.Year(), local.Month(), day)
	if now.Before(start) {
		return userBudgetBoundary(local.Year(), local.Month()-1, day), start
	}
	return start, userBudgetBoundary(local.Year(), local.Month()+1, day)
}

// configureUserBudget 设置个人刷新日；0取消、1至31为每月日期，非法值返回ErrInvalid。
// 创建、单人和批量编辑调用；仅周期变化才安排未来刷新，保留消费及旧周期边界，不因保存恢复额度。
func configureUserBudget(u *User, day int, now time.Time) error {
	if day < 0 || day > 31 {
		return ErrInvalid
	}
	if u.BudgetResetDay == day {
		return nil
	}
	u.BudgetResetDay = day
	u.BudgetResetAt = nil
	if day > 0 {
		_, next := userBudgetWindow(now, day)
		u.BudgetResetAt = &next
	}
	return nil
}

// ResetUserBudgets 检查并补执行到期个人预算；ctx控制取消，now为检查时刻，返回存储错误。
// 准入、读取和后台维护调用；无到期项不加写锁，重置及审计在统一事务中执行。
func (db *DB) ResetUserBudgets(ctx context.Context, now time.Time) error {
	s := db.session(ctx)
	defer s.Close()
	due, err := s.Where("budget_reset_day > 0 AND budget_reset_at <= ?", now.UTC()).Exist(&User{})
	if err != nil || !due {
		return err
	}
	return db.tx(ctx, func(s *xorm.Session) error { return resetUserBudgets(s, now) })
}

// resetUserBudgets 在已持有全局事务锁时补执行月度刷新；返回查询或审计错误，失败整体回滚。
// 停机多月直接推进到当前月，不累积额度；历史账单和其他层级保持原值，重复执行不会再次清零。
func resetUserBudgets(s *xorm.Session, now time.Time) error {
	var rows []User
	if err := s.Where("budget_reset_day > 0 AND budget_reset_at <= ?", now.UTC()).Find(&rows); err != nil {
		return err
	}
	for _, u := range rows {
		if u.BudgetResetDay > 31 {
			return ErrInvalid
		}
		start, next := userBudgetWindow(now, u.BudgetResetDay)
		previous, spent := u.BudgetResetAt, u.Spend
		u.Spend, u.BudgetPeriodStart, u.BudgetResetAt = 0, &start, &next
		if _, err := s.ID(u.ID).Cols("spend", "budget_period_start", "budget_reset_at").Update(&u); err != nil {
			return err
		}
		if err := writeAudit(s, Actor{Kind: "system"}, Audit{Action: "user.budget_reset", ObjectType: "user", ObjectID: u.ID, Detail: map[string]any{"previous_spend": spent, "previous_reset_at": previous, "period_start": start, "next_reset_at": next, "budget_reset_day": u.BudgetResetDay}}); err != nil {
			return err
		}
	}
	return nil
}

// ResetDueBudgets 补执行个人及组织到期预算；ctx和now指定检查范围，返回首个存储错误。
// 后台维护及额度读取调用；各层周期独立，失败不会清理账单，下次检查可安全重试。
func (db *DB) ResetDueBudgets(ctx context.Context, now time.Time) error {
	if err := db.ResetUserBudgets(ctx, now); err != nil {
		return err
	}
	return db.ResetOrganizationBudgets(ctx, now)
}
