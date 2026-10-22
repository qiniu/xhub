package iam

import (
	"context"
	"errors"
	"math"
	"time"

	"xorm.io/xorm"
)

// ErrBudgetUnavailable 表示本次预占超过某层可用余额；网关软预算准入返回 403，不调用上游。
var ErrBudgetUnavailable = errors.New("budget reservation exceeds available balance")

// BudgetReservation 保存每次上游尝试的计费路径和价格快照，不设自动过期。
// RequestID 是尝试 ID，CallID 是对外请求 ID；未知已付费的占用须核对后恢复，不能按 TTL 猜测退款。
type BudgetReservation struct {
	RequestID string    `xorm:"pk 'request_id'" json:"reservation_id"`
	CallID    string    `xorm:"'call_id'" json:"call_id"`
	Kind      string    `xorm:"'kind'" json:"kind"`
	NodeID    string    `xorm:"'node_id'" json:"node_id"`
	KeyID     string    `xorm:"'key_id'" json:"key_id"`
	UserID    string    `xorm:"'user_id'" json:"user_id"`
	TeamID    string    `xorm:"'team_id'" json:"team_id"`
	OrgID     string    `xorm:"'org_id'" json:"org_id"`
	ProjectID string    `xorm:"'project_id'" json:"project_id"`
	Amount    float64   `xorm:"'amount'" json:"amount"`
	Snapshot  string    `xorm:"'snapshot'" json:"snapshot"`
	State     string    `xorm:"'state'" json:"state"`
	Reason    string    `xorm:"'reason'" json:"reason"`
	StartedAt time.Time `xorm:"'started_at'" json:"started_at"`
	CreatedAt time.Time `xorm:"created 'created_at'" json:"created_at"`
}

// TableName 返回预占表名；无参数和副作用，供 xorm 查询使用。
func (BudgetReservation) TableName() string { return "budget_reservations" }

// BudgetReservationOptions 为实际出站尝试补充可核对的请求 ID 与冻结计价快照。
// 项目和五层身份必须由数据库推导，不能由调用者通过此配置覆盖。
type BudgetReservationOptions struct {
	CallID, Snapshot string
	// StartedAt 与最终 UsageRecord.TS 一致，跨周期重试仍归属于原请求周期。
	StartedAt time.Time
}

// applyBudgetHolds 在同一事务把已有预占加到额度树消费；用于管理分配和新请求准入。
// 参数 s/nodes 为当前事务和节点；返回数据库错误，不修改持久消费，不会提前释放未结算费用。
func applyBudgetHolds(s *xorm.Session, nodes map[string]*quotaNode) error {
	var holds []BudgetReservation
	if err := s.Find(&holds); err != nil {
		return err
	}
	for _, h := range holds {
		for kind, id := range map[string]string{"key": h.KeyID, "user": h.UserID, "team": h.TeamID, "org": h.OrgID} {
			if n := nodes[kind+":"+id]; n != nil {
				// 组织按请求开始时间归属周期；旧周期占用仍保留在其他层级与核对列表。
				if (kind == "org" || kind == "user") && n.PeriodStart != nil && h.StartedAt.Before(*n.PeriodStart) {
					continue
				}
				n.Spend += h.Amount
			}
		}
	}
	return nil
}

// reservationProject 从 Key 行读取项目归属；会话没有项目，返回 nil。
// 参数 s/kind/id 为同一事务和身份；项目缺失返回错误，防止跳过已声明的预算限制。
func reservationProject(s *xorm.Session, kind, id string) (*Project, error) {
	if kind != "key" {
		return nil, nil
	}
	var key Key
	found, err := s.ID(id).Get(&key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	if key.ProjectID == nil || *key.ProjectID == "" {
		return nil, nil
	}
	var project Project
	found, err = s.ID(*key.ProjectID).Get(&project)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	return &project, nil
}

// RequiresBudget 判断本次身份或任一祖先/项目是否有限额，供网关决定是否必须构造严格上界。
// 参数为数据库身份，返回是否需要预占或读取错误；实际余额检查仍须由 ReserveBudget 在事务锁内完成。
func (db *DB) RequiresBudget(ctx context.Context, kind, id string) (bool, error) {
	s := db.session(ctx)
	defer s.Close()
	nodes, err := quotaTreeFor(s, kind, id)
	if err != nil {
		return false, err
	}
	n := nodes[kind+":"+id]
	if n == nil {
		return false, ErrNotFound
	}
	for n != nil {
		if n.Limit != nil {
			return true, nil
		}
		if n.Parent == "ambiguous" {
			return false, ErrMultipleTeams
		}
		if n.Parent != "" && nodes[n.Parent] == nil {
			return false, ErrInvalid
		}
		n = nodes[n.Parent]
	}
	project, err := reservationProject(s, kind, id)
	return project != nil && project.MaxBudget != nil, err
}

// ReserveBudget 在 PostgreSQL 事务级跨实例锁内预占每次尝试的费用上界，重试必须使用独立尝试 ID。
// 参数 ctx/requestID/kind/id/amount 为上下文、尝试标识、身份和上界；可选配置保存请求及价格快照。
// 返回持久身份快照或余额/存储错误；同 ID 只允许完全相同的金额身份重放；五层和项目全部成功才写入。
// 结算由 RecordUsage 原子释放；仅确认未发送的请求可以主动 ReleaseBudget，绝不按超时自动释放。
func (db *DB) ReserveBudget(ctx context.Context, requestID, kind, id string, amount float64, options ...BudgetReservationOptions) (*BudgetReservation, error) {
	if requestID == "" || (kind != "key" && kind != "user") || id == "" || amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || len(options) > 1 {
		return nil, ErrInvalid
	}
	opt := BudgetReservationOptions{CallID: requestID}
	if len(options) == 1 {
		opt = options[0]
		if opt.CallID == "" {
			return nil, ErrInvalid
		}
	}
	// PostgreSQL 时间精度为微秒，幂等重放比较必须使用同一精度。
	if !opt.StartedAt.IsZero() {
		opt.StartedAt = opt.StartedAt.UTC().Truncate(time.Microsecond)
	}
	var hold BudgetReservation
	err := db.tx(ctx, func(s *xorm.Session) error {
		found, err := s.ID(requestID).Get(&hold)
		if err != nil {
			return err
		}
		if found {
			if hold.Kind != kind || hold.NodeID != id || hold.Amount != amount || hold.CallID != opt.CallID || hold.Snapshot != opt.Snapshot || (!opt.StartedAt.IsZero() && !hold.StartedAt.Equal(opt.StartedAt)) {
				return ErrConflict
			}
			return nil
		}
		settled, err := s.Where("request_id = ?", opt.CallID).Exist(new(UsageEvent))
		if err != nil {
			return err
		}
		if settled {
			return ErrConflict
		}
		nodes, err := quotaTreeFor(s, kind, id)
		if err != nil {
			return err
		}
		n := nodes[kind+":"+id]
		if n == nil {
			return ErrNotFound
		}
		if err := applyBudgetHolds(s, nodes); err != nil {
			return err
		}
		hold = BudgetReservation{RequestID: requestID, CallID: opt.CallID, Kind: kind, NodeID: id, Amount: amount, Snapshot: opt.Snapshot, State: "reserved", StartedAt: opt.StartedAt}
		if hold.StartedAt.IsZero() {
			hold.StartedAt = time.Now().UTC()
		}
		var child *quotaNode
		credit := 0.0
		for n != nil {
			switch n.Kind {
			case "key":
				hold.KeyID = n.ID
			case "user":
				hold.UserID = n.ID
			case "team":
				hold.TeamID = n.ID
			case "org":
				hold.OrgID = n.ID
			}
			if n.Parent == "ambiguous" {
				return ErrMultipleTeams
			}
			if n.Limit != nil && !((n.Kind == "org" || n.Kind == "user") && n.PeriodStart != nil && hold.StartedAt.Before(*n.PeriodStart)) {
				reserved := 0.0
				for _, c := range n.Children {
					value := quotaReserved(c)
					if c == child {
						value = math.Max(0, value-credit)
					}
					reserved += value
				}
				// 不使用正 epsilon 放宽边界；上界已向上取整，余额不足必须拒绝。
				if n.Spend+reserved+amount > *n.Limit {
					return ErrBudgetUnavailable
				}
				credit = math.Max(0, *n.Limit-n.Spend)
			}
			child = n
			if n.Parent != "" && nodes[n.Parent] == nil {
				return ErrInvalid
			}
			n = nodes[n.Parent]
		}
		project, err := reservationProject(s, kind, id)
		if err != nil {
			return err
		}
		if project != nil {
			hold.ProjectID = project.ID
			if project.MaxBudget != nil {
				held, err := s.Where("project_id = ?", project.ID).Sum(new(BudgetReservation), "amount")
				if err != nil {
					return err
				}
				if project.Spend+held+amount > *project.MaxBudget {
					return ErrBudgetUnavailable
				}
			}
		}
		_, err = s.Insert(&hold)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &hold, nil
}

// ReleaseBudget 释放已确认未发送的尝试；参数为上下文与尝试 ID，返回存储错误。
// 重复调用无害，数据库不可用则保留占用；调用者不得用它释放提交上游后费用未知的请求。
func (db *DB) ReleaseBudget(ctx context.Context, requestID string) error {
	if requestID == "" {
		return ErrInvalid
	}
	return db.tx(ctx, func(s *xorm.Session) error { _, err := s.ID(requestID).Delete(new(BudgetReservation)); return err })
}

// SetBudgetReservationState 为核对保留占用记录生命周期；参数为尝试 ID 和受支持状态。
// 无退款副作用；执行器提交前写 submitted，成功排队写 settling，未知费用写 uncertain。
func (db *DB) SetBudgetReservationState(ctx context.Context, id, state string, reasons ...string) error {
	if len(reasons) > 1 || id == "" || (state != "submitted" && state != "settling" && state != "uncertain") {
		return ErrInvalid
	}
	reason := ""
	if len(reasons) == 1 {
		reason = reasons[0]
	}
	return db.tx(ctx, func(s *xorm.Session) error {
		_, err := s.ID(id).Cols("state", "reason").Update(&BudgetReservation{State: state, Reason: reason})
		return err
	})
}

// PendingKeyBudget 返回指定 Key 的未结算占用，供已鉴权的管理详情展示和人工核对。
// 参数为 Key 行 ID，不接受哈希；调用方负责权限。按创建时间排序，只读，无副作用。
func (db *DB) PendingKeyBudget(ctx context.Context, id string) ([]BudgetReservation, error) {
	if id == "" {
		return nil, ErrInvalid
	}
	s := db.session(ctx)
	defer s.Close()
	var rows []BudgetReservation
	err := s.Where("key_id = ?", id).Asc("created_at").Find(&rows)
	return rows, err
}

// settleBudgetHold 校验尝试和账单归属、价格上界并在入账事务内删除占用。
// 参数 s/r 为事务和记录；未知或超出声明用量不得退款；所有检查失败均回滚账单和释放。
func settleBudgetHold(s *xorm.Session, r UsageRecord) error {
	id := r.BudgetReservationID
	if id == "" {
		id = r.RequestID
	}
	var h BudgetReservation
	found, err := s.ID(id).Get(&h)
	if err != nil || !found {
		return err
	}
	if h.CallID != "" && h.CallID != r.RequestID {
		return ErrConflict
	}
	if h.KeyID != r.KeyID || h.UserID != r.UserID || h.TeamID != r.TeamID || h.OrgID != r.OrganizationID || h.ProjectID != r.ProjectID {
		return ErrConflict
	}
	if r.Cost > h.Amount {
		return ErrBudgetUnavailable
	}
	// 同call可能已有失败零账单或另一attempt的费用。先删hold再依靠事件去重会吞掉本次费用；
	// 人工核对有独立增量事务，普通晚到结算在此保留原占用和队列证据，不猜测如何覆盖历史。
	duplicate, err := s.Where("request_id = ?", r.RequestID).Exist(new(UsageEvent))
	if err != nil {
		return err
	}
	if duplicate {
		return ErrConflict
	}
	_, err = s.ID(id).Delete(new(BudgetReservation))
	return err
}

// CheckSoftBudgetBalance 只读检查软预算准入余额，计入现有预占和待结算费用；参数为身份及热费用读取函数，返回不足或存储错误。
// 所有模型出站前调用；不新增预占、不释放历史uncertain，无法保证检查后并发调用的最终消费不超额。
func (db *DB) CheckSoftBudgetBalance(ctx context.Context, kind, id string, hot func(string, string, ...time.Time) float64) error {
	s := db.session(ctx)
	defer s.Close()
	nodes, err := quotaTreeFor(s, kind, id)
	if err != nil {
		return err
	}
	if err = applyBudgetHolds(s, nodes); err != nil {
		return err
	}
	_, _, scope, err := quotaPath(nodes, kind+":"+id, hot)
	if err != nil {
		return err
	}
	if scope != "" {
		return ErrBudgetUnavailable
	}
	project, err := reservationProject(s, kind, id)
	if err != nil {
		return err
	}
	if project != nil && project.MaxBudget != nil {
		held, err := s.Where("project_id = ?", project.ID).Sum(new(BudgetReservation), "amount")
		if err != nil {
			return err
		}
		spent := project.Spend + held
		if hot != nil {
			spent += hot("project", project.ID)
		}
		if math.IsNaN(spent) || math.IsInf(spent, 0) {
			return ErrQuotaUnavailable
		}
		if spent >= *project.MaxBudget {
			return ErrBudgetUnavailable
		}
	}
	return nil
}
