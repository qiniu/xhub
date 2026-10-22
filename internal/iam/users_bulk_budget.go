package iam

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"xorm.io/xorm"
)

// MaxBulkUserBudgets 限制一次原子预算管理事务的规模；超过限制须分批明确选择，全部模式不会截断成部分成功。
const MaxBulkUserBudgets = 1000

// ErrBulkUserBudgetTooLarge 表示显式选择或全部用户快照超过单次限制，接口应提示分批选择。
var ErrBulkUserBudgetTooLarge = errors.New("bulk user budget operation exceeds 1000 users")

// UserBudgetUpdate 独立设置个人额度及月度日期；KeepBudget保持额度，否则nil取消上限、零禁止消费。
// BudgetResetDay缺省保持、0取消周期、1至31指定日期；设置时均不立即重置消费。
type UserBudgetUpdate struct {
	KeepBudget     bool
	BudgetResetDay *int
	UserID         string
	MaxBudget      *float64
}

// BulkUserBudgetInput 区分全部用户统一设置与指定用户逐项设置；调用方只提交一种目标模式。
// AllUsers为真时使用顶层变更，否则使用Users；KeepBudget表示不改额度，BudgetResetDay缺省表示不改周期。
type BulkUserBudgetInput struct {
	KeepBudget     bool
	BudgetResetDay *int
	AllUsers       bool
	MaxBudget      *float64
	Users          []UserBudgetUpdate
}

// BulkUserBudgetResult 返回同一提交的用户快照与批次号；仅事务成功时可作为成功响应，失败不返回局部结果。
type BulkUserBudgetResult struct {
	BatchID string
	Users   []User
}

// normalizeBulkUserBudgets 校验命令并复制/排序目标；参数为管理命令，返回独立命令或空、重复、非法额度错误。
// 用于HTTP和存储双重边界校验；不修改原请求，不读取数据库，取消上限仍须在事务内校验上级和下级分配。
func normalizeBulkUserBudgets(in BulkUserBudgetInput) (BulkUserBudgetInput, error) {
	valid := func(n *float64) bool { return n == nil || !math.IsNaN(*n) && !math.IsInf(*n, 0) && *n >= 0 }
	copyBudget := func(n *float64) *float64 {
		if n == nil {
			return nil
		}
		v := *n
		return &v
	}
	validDay := func(day *int) bool { return day == nil || *day >= 0 && *day <= 31 }
	if in.AllUsers {
		if len(in.Users) > 0 || !valid(in.MaxBudget) || !validDay(in.BudgetResetDay) || (in.KeepBudget && in.BudgetResetDay == nil) {
			return BulkUserBudgetInput{}, ErrInvalid
		}
		in.MaxBudget = copyBudget(in.MaxBudget)
		return in, nil
	}
	if len(in.Users) == 0 || in.MaxBudget != nil || in.KeepBudget || in.BudgetResetDay != nil {
		return BulkUserBudgetInput{}, ErrInvalid
	}
	if len(in.Users) > MaxBulkUserBudgets {
		return BulkUserBudgetInput{}, ErrBulkUserBudgetTooLarge
	}
	out := BulkUserBudgetInput{Users: make([]UserBudgetUpdate, len(in.Users))}
	seen := map[string]bool{}
	for i, u := range in.Users {
		if u.UserID == "" || u.UserID != strings.TrimSpace(u.UserID) || seen[u.UserID] || !valid(u.MaxBudget) || !validDay(u.BudgetResetDay) || (u.KeepBudget && u.BudgetResetDay == nil) {
			return BulkUserBudgetInput{}, ErrInvalid
		}
		seen[u.UserID] = true
		out.Users[i] = UserBudgetUpdate{UserID: u.UserID, MaxBudget: copyBudget(u.MaxBudget), KeepBudget: u.KeepBudget, BudgetResetDay: u.BudgetResetDay}
	}
	sort.Slice(out.Users, func(i, j int) bool { return out.Users[i].UserID < out.Users[j].UserID })
	return out, nil
}

// AdminBulkUserBudgets 在现有全局写事务锁内原子设置个人预算、验证最终额度树并记录每位用户的审计。
// 参数ctx/by/in为上下文、已通过平台管理鉴权的操作者和显式目标命令；返回已提交快照或错误，失败整体回滚。
// 全部模式在事务内读取至多1001个当前账号，包含管理员和停用账号；超限不会部分更新。历史消费和角色均保留；周期只能通过显式字段变更。
// 通过applyBudgetHolds复用硬额度预占约束，与ReserveBudget/结算共用锁，不能把在途金额重新分配给其他用户。
func (db *DB) AdminBulkUserBudgets(ctx context.Context, by Actor, in BulkUserBudgetInput) (BulkUserBudgetResult, error) {
	in, err := normalizeBulkUserBudgets(in)
	if err != nil {
		return BulkUserBudgetResult{}, err
	}
	out := BulkUserBudgetResult{BatchID: newID()}
	err = db.tx(ctx, func(s *xorm.Session) error {
		var users []User
		if in.AllUsers {
			if err := s.Asc("id").Limit(MaxBulkUserBudgets + 1).Find(&users); err != nil {
				return err
			}
			if len(users) > MaxBulkUserBudgets {
				return ErrBulkUserBudgetTooLarge
			}
			if len(users) == 0 {
				return ErrInvalid
			}
		} else {
			ids := make([]string, len(in.Users))
			for i, u := range in.Users {
				ids[i] = u.UserID
			}
			if err := s.In("id", ids).Asc("id").Find(&users); err != nil {
				return err
			}
			if len(users) != len(ids) {
				return ErrNotFound
			}
		}
		updates := map[string]UserBudgetUpdate{}
		for _, u := range in.Users {
			updates[u.UserID] = u
		}
		previous := map[string]*float64{}
		previousDay := map[string]int{}
		ids := make([]string, 0, len(users))
		for i := range users {
			u := &users[i]
			previous[u.ID] = u.MaxBudget
			previousDay[u.ID] = u.BudgetResetDay
			update := UserBudgetUpdate{MaxBudget: in.MaxBudget, KeepBudget: in.KeepBudget, BudgetResetDay: in.BudgetResetDay}
			if !in.AllUsers {
				update = updates[u.ID]
			}
			if !update.KeepBudget {
				u.MaxBudget = update.MaxBudget
				ids = append(ids, u.ID)
			}
			if update.BudgetResetDay != nil {
				if err := configureUserBudget(u, *update.BudgetResetDay, time.Now()); err != nil {
					return err
				}
			}
			if _, err := s.ID(u.ID).Cols("max_budget", "budget_reset_day", "budget_reset_at").Update(u); err != nil {
				return err
			}
		}
		// 所有目标先写进同一未提交树，再统一校验，避免“先增加后释放”造成顺序相关的假失败。
		nodes, err := quotaTree(s)
		if err != nil {
			return err
		}
		if err = applyBudgetHolds(s, nodes); err != nil {
			return err
		}
		if err = validateBulkUserBudgetTree(nodes, ids); err != nil {
			return err
		}
		for _, u := range users {
			if err := writeAudit(s, by, Audit{Action: "user.bulk_budget", ObjectType: "user", ObjectID: u.ID, Detail: map[string]any{"batch_id": out.BatchID, "all_users": in.AllUsers, "previous_max_budget": previous[u.ID], "max_budget": u.MaxBudget, "spend": u.Spend, "previous_budget_reset_day": previousDay[u.ID], "budget_reset_day": u.BudgetResetDay}}); err != nil {
				return err
			}
		}
		out.Users = users
		return nil
	})
	if err != nil {
		return BulkUserBudgetResult{}, err
	}
	return out, nil
}

// validateBulkUserBudgetTree 校验已含全部最终修改和预占的额度树；参数为事务快照和目标ID，返回目标/归属/分配错误。
// 每条受影响路径检查一次；组织团队的检查递归包括固定下级保留额度，纯函数不修改节点或数据库。
func validateBulkUserBudgetTree(nodes map[string]*quotaNode, ids []string) error {
	checked := map[string]bool{}
	for _, id := range ids {
		n := nodes["user:"+id]
		if n == nil {
			return ErrNotFound
		}
		for n != nil {
			key := n.Kind + ":" + n.ID
			if checked[key] {
				break
			}
			checked[key] = true
			if n.Parent == "ambiguous" {
				return ErrMultipleTeams
			}
			if _, err := quotaUnused(n); err != nil {
				return err
			}
			if n.Parent != "" && nodes[n.Parent] == nil {
				return ErrInvalid
			}
			n = nodes[n.Parent]
		}
	}
	return nil
}
