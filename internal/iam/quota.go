package iam

import (
	"context"
	"errors"
	"math"
	"time"

	"xorm.io/xorm"

	"github.com/sunqirui1987/xhub/internal/live"
)

// ErrQuotaAllocation 表示已消费与下级保留额度超过父级；管理接口返回 400 并回滚配置。
var ErrQuotaAllocation = errors.New("quota allocation exceeds available parent budget")

// ErrMultipleTeams 表示账号存在多个团队，必须先解除多余归属，不能猜测计费路径。
var ErrMultipleTeams = errors.New("a person may belong to only one team")

// quotaNode 是额度树节点；null 共享父级，固定额度保留未消费部分，历史消费留在父级。
type quotaNode struct {
	ID, Parent, Kind string
	HotID            string
	RPM, TPM         *int
	Limit            *float64
	Spend            float64
	Children         []*quotaNode
}

// quotaUnused 计算节点仍需由父级保留的金额。参数 n 是树节点；返回未消费保留金额或非法分配错误。
// 调用：管理写入及消费检查；共享节点递归传递下级保留金额，固定节点不得低于消费与下级保留总和。
func quotaUnused(n *quotaNode) (float64, error) {
	unused := 0.0
	for _, c := range n.Children {
		amount, err := quotaUnused(c)
		if err != nil {
			return 0, err
		}
		unused += amount
	}
	if n.Limit == nil {
		return unused, nil
	}
	if math.IsNaN(*n.Limit) || math.IsInf(*n.Limit, 0) || *n.Limit < 0 || n.Spend+unused > *n.Limit+1e-9 {
		return 0, ErrQuotaAllocation
	}
	return math.Max(0, *n.Limit-n.Spend), nil
}

// quotaTree 读取当前事务的额度归属；参数 s 是会话，返回节点索引或数据库错误。
// 调用：分配校验、运行时检查；所有密钥只能沿用户唯一团队消费，项目是额外上限。
func quotaTree(s *xorm.Session) (map[string]*quotaNode, error) {
	return quotaTreeFor(s, "", "")
}

// quotaTreeFor 读取管理校验的完整树，或运行时主体所属组织的额度树。
// 参数 s 为数据库会话，kind/id 为空时供管理事务校验，否则仅支持 user/key；返回已链接节点或归属错误。
// 保留同组织兄弟及后代的固定额度，团队外个人只读取自己；不缓存跨请求配置，不读取无关组织热消费。
func quotaTreeFor(s *xorm.Session, kind, id string) (map[string]*quotaNode, error) {
	nodes := map[string]*quotaNode{}
	var orgs []Organization
	var teams []Team
	var users []User
	var keys []Key
	var members []TeamMembership
	userID, orgID := "", ""
	if kind != "" {
		userID = id
		if kind == "key" {
			var key Key
			ok, err := s.ID(id).Get(&key)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, ErrNotFound
			}
			if key.UserID == nil || *key.UserID == "" {
				return nil, ErrInvalid
			}
			userID = *key.UserID
		} else if kind != "user" {
			return nil, ErrInvalid
		}
		if err := s.Where("user_id = ?", userID).Find(&members); err != nil {
			return nil, err
		}
		if len(members) > 1 {
			return nil, ErrMultipleTeams
		}
		if len(members) == 1 {
			var team Team
			ok, err := s.ID(members[0].TeamID).Get(&team)
			if err != nil {
				return nil, err
			}
			if !ok || team.OrganizationID == "" {
				return nil, ErrInvalid
			}
			orgID = team.OrganizationID
		}
	}
	if kind != "" {
		s.Where("id = ?", orgID)
	}
	if err := s.Find(&orgs); err != nil {
		return nil, err
	}
	if kind != "" {
		s.Where("organization_id = ?", orgID)
	}
	if err := s.Find(&teams); err != nil {
		return nil, err
	}
	// 成员查询按组织界定用户，随后读取这些用户的全部归属，以拒绝跨组织的多团队脏数据。
	userFilter := "id = ?"
	userArg := userID
	if orgID != "" {
		userFilter = "id IN (SELECT user_id FROM team_members WHERE team_id IN (SELECT id FROM teams WHERE organization_id = ?))"
		userArg = orgID
	}
	if kind != "" {
		s.Where(userFilter, userArg)
	}
	if err := s.Find(&users); err != nil {
		return nil, err
	}
	if kind != "" {
		s.Where("user_id IN (SELECT id FROM users WHERE "+userFilter+")", userArg)
	}
	if err := s.Find(&keys); err != nil {
		return nil, err
	}
	members = nil
	if kind != "" {
		s.Where("user_id IN (SELECT id FROM users WHERE "+userFilter+")", userArg)
	}
	if err := s.Find(&members); err != nil {
		return nil, err
	}
	parents := map[string]string{}
	for _, m := range members {
		if parents[m.UserID] != "" {
			parents[m.UserID] = "ambiguous"
		} else {
			parents[m.UserID] = "team:" + m.TeamID
		}
	}
	for _, o := range orgs {
		nodes["org:"+o.ID] = &quotaNode{ID: o.ID, Kind: "org", Limit: o.MaxBudget, Spend: o.Spend, RPM: o.RPMLimit, TPM: o.TPMLimit}
	}
	for _, t := range teams {
		nodes["team:"+t.ID] = &quotaNode{ID: t.ID, Kind: "team", Parent: "org:" + t.OrganizationID, Limit: t.MaxBudget, Spend: t.Spend, RPM: t.RPMLimit, TPM: t.TPMLimit}
	}
	for _, u := range users {
		nodes["user:"+u.ID] = &quotaNode{ID: u.ID, Kind: "user", Parent: parents[u.ID], Limit: u.MaxBudget, Spend: u.Spend, RPM: u.RPMLimit, TPM: u.TPMLimit}
	}
	for _, k := range keys {
		uid := ""
		if k.UserID != nil {
			uid = *k.UserID
		}
		parent := ""
		if uid != "" {
			parent = "user:" + uid
		} else {
			return nil, ErrInvalid
		}
		nodes["key:"+k.ID] = &quotaNode{ID: k.ID, Kind: "key", HotID: k.TokenHash, Parent: parent, Limit: k.MaxBudget, Spend: k.Spend, RPM: k.RPMLimit, TPM: k.TPMLimit}
	}
	for _, n := range nodes {
		if p := nodes[n.Parent]; p != nil {
			p.Children = append(p.Children, n)
		}
	}
	return nodes, nil
}

// validateQuota 校验指定节点及祖先。参数 kind/id 标识修改对象；返回分配错误，事务调用方负责回滚。
// 只验证受影响路径，减少分配可释放未使用容量，历史消费不回退，不阻断无关资料修改。
func validateQuota(s *xorm.Session, kind, id string) error {
	nodes, err := quotaTree(s)
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
	for n != nil {
		if n.Parent == "ambiguous" {
			return ErrMultipleTeams
		}
		if _, err := rateAllocated(n, false); err != nil {
			return err
		}
		if _, err := rateAllocated(n, true); err != nil {
			return err
		}
		if _, err := quotaUnused(n); err != nil {
			return err
		}
		if n.Parent != "" && nodes[n.Parent] == nil {
			return ErrInvalid
		}
		n = nodes[n.Parent]
	}
	return nil
}

// singleTeamMember 拒绝第二个团队归属。参数 s/userID/teamID 为事务与身份；返回归属或数据库错误。
// 调用：创建团队、添加成员；已有本团队关系由原唯一约束处理，不自动移动消费历史。
func singleTeamMember(s *xorm.Session, userID, teamID string) error {
	var members []TeamMembership
	if err := s.Where("user_id = ?", userID).Find(&members); err != nil {
		return err
	}
	for _, m := range members {
		if m.TeamID != teamID {
			return ErrMultipleTeams
		}
	}
	return nil
}

// QuotaPath 检查调用节点及祖先的共享余额。参数 ctx/kind/id/hot 为上下文、主体与未落库消费读取器。
// 返回消费团队、组织、耗尽层级及错误；固定孩子保留额度，仅共享孩子受父级共享池限制。
// 调用：会话与密钥检查；检查前补执行到期组织周期，失败拒绝准入；无法约束供应商最终计价超过最后余额的情况。
func (db *DB) QuotaPath(ctx context.Context, kind, id string, hot func(string, string) float64) (string, string, string, error) {
	if err := db.ResetOrganizationBudgets(ctx, time.Now()); err != nil {
		return "", "", "", err
	}
	s := db.session(ctx)
	defer s.Close()
	nodes, err := quotaTreeFor(s, kind, id)
	if err != nil {
		return "", "", "", err
	}
	return quotaPath(nodes, kind+":"+id, hot)
}

// QuotaAndRates 为首次准入共用一次归属树读取，避免预算和分钟计划分别扫描同一组织。
// 参数 ctx/kind/id/hot 与 QuotaPath 相同；返回计费团队、组织、耗尽层级、速率计划及读取错误。
// 网关首次准入调用；先补执行到期组织周期，再读取预算及热消费，不占分钟容量，后续复核仍读取最新预算。
func (db *DB) QuotaAndRates(ctx context.Context, kind, id string, hot func(string, string) float64) (string, string, string, live.RatePlan, error) {
	if err := db.ResetOrganizationBudgets(ctx, time.Now()); err != nil {
		return "", "", "", live.RatePlan{}, err
	}
	s := db.session(ctx)
	defer s.Close()
	nodes, err := quotaTreeFor(s, kind, id)
	if err != nil {
		return "", "", "", live.RatePlan{}, err
	}
	team, org, scope, err := quotaPath(nodes, kind+":"+id, hot)
	if err != nil || scope != "" {
		return team, org, scope, live.RatePlan{}, err
	}
	plan, err := buildRatePlan(nodes, kind+":"+id)
	return team, org, scope, plan, err
}

// quotaPath 检查已读取的归属树及热消费，参数 target 为层级前缀 ID，hot 可空。
// 返回计费归属及耗尽层级；供 QuotaPath/QuotaAndRates 共用，不更新数据库或速率计数。
func quotaPath(nodes map[string]*quotaNode, target string, hot func(string, string) float64) (string, string, string, error) {
	n := nodes[target]
	if n == nil {
		return "", "", "", ErrNotFound
	}
	// 热账单同时影响父级消费和子级未用保留额，避免重复占用。
	if hot != nil {
		for _, node := range nodes {
			ref := node.ID
			if node.Kind == "key" {
				ref = node.HotID
			}
			node.Spend += hot(node.Kind, ref)
		}
	}
	team, org, scope := "", "", ""
	var child *quotaNode
	credit := 0.0
	for n != nil {
		if n.Parent == "ambiguous" {
			return "", "", "", ErrMultipleTeams
		}
		if n.Kind == "team" {
			team = n.ID
		}
		if n.Kind == "org" {
			org = n.ID
		}
		spent := n.Spend
		if n.Limit != nil {
			reserved := 0.0
			for _, c := range n.Children {
				amount := quotaReserved(c)
				// 固定后代的保留权穿过共享层，不能被自己的保留额再次拦截。
				if c == child {
					amount = math.Max(0, amount-credit)
				}
				reserved += amount
			}
			if spent+reserved >= *n.Limit-1e-12 && scope == "" {
				scope = n.Kind
			}
			credit = math.Max(0, *n.Limit-spent)
		}
		if n.Parent != "" && nodes[n.Parent] == nil {
			return team, org, "", ErrInvalid
		}
		child = n
		n = nodes[n.Parent]
	}
	return team, org, scope, nil
}

// quotaReserved 计算运行时未使用的保留金额；参数 n 含热消费，返回非负金额。
// 调用：QuotaPath；固定额度耗尽后的保留额为零，消费本身仍在祖先账单中。
func quotaReserved(n *quotaNode) float64 {
	if n.Limit != nil {
		return math.Max(0, *n.Limit-n.Spend)
	}
	amount := 0.0
	for _, c := range n.Children {
		amount += quotaReserved(c)
	}
	return amount
}

// rateAllocated 计算固定 RPM 或 TPM 分配；参数 n 为节点、tokens 选择维度，返回向父级占用的容量或超配错误。
// 调用：所有额度树写入；共享层透传下级分配，固定层保留自身容量，整数相加不允许超过父级。
func rateAllocated(n *quotaNode, tokens bool) (int64, error) {
	allocated := int64(0)
	for _, c := range n.Children {
		value, err := rateAllocated(c, tokens)
		if err != nil {
			return 0, err
		}
		allocated += value
	}
	limit := n.RPM
	if tokens {
		limit = n.TPM
	}
	if limit == nil {
		return allocated, nil
	}
	if err := ValidateRates(limit); err != nil {
		return 0, err
	}
	if allocated > int64(*limit) {
		return 0, ErrRateAllocation
	}
	return int64(*limit), nil
}
