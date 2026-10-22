package iam

import (
	"context"
	"time"

	"xorm.io/xorm"
)

// OrganizationBudgetInterval 校验组织预算周期；value 为协议标识，返回固定 UTC 时长或 ErrInvalid。
// 创建、更新和重置共用；空串表示不自动重置，30d 是固定 30 天而非自然月，无副作用。
func OrganizationBudgetInterval(value string) (time.Duration, error) {
	switch value {
	case "":
		return 0, nil
	case "24h":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	default:
		return 0, ErrInvalid
	}
}

// configureOrganizationBudget 保存或取消周期；参数为事务中组织、显式周期和当前时间，返回校验错误。
// 创建和更新调用；修改周期保留当前消费，只有周期改变才重新安排下一次重置，不能重复保存来恢复额度。
// 取消只取消未来重置，保留当前计数和最近周期起点，以免迟到旧账单重新占用当前额度。
func configureOrganizationBudget(o *Organization, value string, now time.Time) error {
	interval, err := OrganizationBudgetInterval(value)
	if err != nil {
		return err
	}
	if o.BudgetDuration == value {
		return nil
	}
	o.BudgetDuration = value
	o.BudgetResetAt = nil
	if interval > 0 {
		next := now.UTC().Add(interval)
		o.BudgetResetAt = &next
	}
	return nil
}

// ResetOrganizationBudgets 补执行到期组织预算；ctx 控制取消，now 为 UTC 检查时刻，返回存储错误。
// 后台循环、组织读取及准入调用；无到期项只查询，有到期项通过管理/记账共用事务锁串行重置并写审计。
// 停机跨多个周期直接推进到当前周期，不累积充值；本组织重置不改写历史、团队和Key计数，事务另行检查独立的个人到期周期。
func (db *DB) ResetOrganizationBudgets(ctx context.Context, now time.Time) error {
	s := db.session(ctx)
	defer s.Close()
	due, err := s.Where("budget_duration <> '' AND budget_reset_at <= ?", now.UTC()).Exist(&Organization{})
	if err != nil || !due {
		return err
	}
	return db.tx(ctx, func(s *xorm.Session) error { return resetOrganizationBudgets(s, now) })
}

// resetOrganizationBudgets 在调用方已持有全局写事务锁时重置到期组织；参数为会话和时刻，返回任意查询/审计错误。
// db.tx 和显式重置调用；状态与审计同事务，失败全部回滚，并发或重放不会再次重置同一周期。
func resetOrganizationBudgets(s *xorm.Session, now time.Time) error {
	var rows []Organization
	if err := s.Where("budget_duration <> '' AND budget_reset_at <= ?", now.UTC()).Find(&rows); err != nil {
		return err
	}
	for _, o := range rows {
		period, err := OrganizationBudgetInterval(o.BudgetDuration)
		if err != nil || period <= 0 {
			return ErrInvalid
		}
		previous := *o.BudgetResetAt
		cycles := int64(now.Sub(previous)/period) + 1
		start := previous.Add(time.Duration(cycles-1) * period)
		next := start.Add(period)
		spent := o.Spend
		o.Spend = 0
		o.BudgetPeriodStart = &start
		o.BudgetResetAt = &next
		if _, err := s.ID(o.ID).Cols("spend", "budget_period_start", "budget_reset_at").Update(&o); err != nil {
			return err
		}
		if err := writeAudit(s, Actor{Kind: "system"}, Audit{Action: "org.budget_reset", ObjectType: "organization", ObjectID: o.ID,
			Detail: map[string]any{"previous_spend": spent, "previous_reset_at": previous, "period_start": start, "next_reset_at": next, "elapsed_periods": cycles}}); err != nil {
			return err
		}
	}
	return nil
}
