package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/gateway/billing"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

var _ dpcontract.BudgetHost = (*Server)(nil)

// budgetAttempt 保存本次出站冻结的计价源；软预算不创建持久预占或跟踪冻结状态。
type budgetAttempt struct {
	SoftPrice billing.SoftPricePlan
}

// PrepareBudgetAttempt 为所有模型协议执行软预算准入，不再按输入窗口预占或改写输出上限。
// 参数为已鉴权身份、最终出站正文及部署；返回原始正文和是否放行，余额耗尽返回429，存储不可读返回503。
// 每次重试重新核对余额；放行后允许该请求完成并按实际token结算，并发调用可能使余额变负。
func (s *Server) PrepareBudgetAttempt(w http.ResponseWriter, r *http.Request, p *auth.Principal, callID string, dep config.ModelEntry, protocol string, body []byte, start time.Time) ([]byte, bool) {
	s.finishBudgetCall(callID)
	if !s.admitUsageSpool(w, r, callID) {
		return nil, false
	}
	for _, name := range []string{"X-XHub-Budget-State", "X-XHub-Billing-Scope", "X-XHub-Budget-Guarantee", "X-XHub-Tool-Costs", "X-XHub-Max-Output-Tokens", "X-XHub-Max-Tool-Calls", "X-XHub-Budget-Input-Bound", "X-XHub-Budget-Reserved", "X-XHub-Budget-Reservation-Id"} {
		w.Header().Del(name)
	}
	// 尚未持久化的账单不能被当成余额；沿用磁盘队列故障保护，不影响已经放行的调用。
	if s.usageSpool != nil {
		state, err := s.usageSpool.Status()
		if err != nil || state.Pending > 0 {
			util.WriteTypedError(w, r.URL.Path, 503, "billing_storage_pending", "pending token charges must settle before another request")
			return nil, false
		}
	}
	// 无Redis时失败账单可能只保存在PG；先恢复再检查，不能把未结算消费当成零。
	pending, err := s.IAM.Engine.Context(r.Context()).Table("usage_pending").Count()
	if err == nil && pending > 0 {
		err = s.IAM.FlushPendingUsage(r.Context(), 100)
		if err == nil {
			pending, err = s.IAM.Engine.Context(r.Context()).Table("usage_pending").Count()
		}
	}
	if err != nil || pending > 0 {
		util.WriteTypedError(w, r.URL.Path, 503, "billing_storage_pending", "pending token charges must settle before another request")
		return nil, false
	}
	kind, id := "user", p.UserID
	if p.Key != nil {
		kind, id = "key", p.KeyID
	}
	if err := s.IAM.CheckSoftBudgetBalance(r.Context(), kind, id, s.hotSpendRef); err != nil {
		if errors.Is(err, iam.ErrBudgetUnavailable) {
			util.WriteTypedError(w, r.URL.Path, 429, "budget_exceeded", "available budget has been exhausted")
		} else {
			budgetRefusal(w, r.URL.Path, iam.ErrQuotaUnavailable)
		}
		return nil, false
	}
	s.mu.Lock()
	if s.budgetCalls == nil {
		s.budgetCalls = map[string]*budgetAttempt{}
	}
	s.budgetCalls[callID] = &budgetAttempt{SoftPrice: billing.NewSoftPricePlan(dep, start)}
	s.mu.Unlock()
	w.Header().Set("X-XHub-Budget-State", "token_only")
	w.Header().Set("X-XHub-Billing-Scope", "tokens_only")
	w.Header().Set("X-XHub-Budget-Guarantee", "soft")
	w.Header().Set("X-XHub-Tool-Costs", "excluded")
	return body, true
}

// MarkBudgetAttemptSubmitted 在实际发送前检查取消；参数为请求及兼容执行器的调用ID，返回是否继续。
// 软预算不写预占状态，取消返回499；不能因为移除预占而放行已取消请求。
func (s *Server) MarkBudgetAttemptSubmitted(w http.ResponseWriter, r *http.Request, callID string) bool {
	if r.Context().Err() != nil {
		util.WriteTypedError(w, r.URL.Path, 499, "request_cancelled", "request cancelled before upstream submission")
		return false
	}
	return true
}

// finishBudgetCall 删除本次尝试价格快照并释放磁盘队列准入槽；参数为调用ID，可重复调用，无账单退款副作用。
func (s *Server) finishBudgetCall(callID string) {
	if s == nil || callID == "" {
		return
	}
	if s.usageSpool != nil {
		s.usageSpool.Release(callID)
	}
	s.mu.Lock()
	delete(s.budgetCalls, callID)
	s.mu.Unlock()
}

// budgetPriceSnapshot 把预算计费观测附加到现有价格 JSON；参数是计价快照与可空预算元数据。
// 返回兼容现有日志查看器的 JSON，无外部副作用；原快照不可解析时保留预算元数据，避免隐去未定价状态。
func budgetPriceSnapshot(snapshot string, budget map[string]any) string {
	if budget == nil {
		return snapshot
	}
	fields := map[string]any{}
	_ = json.Unmarshal([]byte(snapshot), &fields)
	if fields == nil {
		fields = map[string]any{}
	}
	fields["budget"] = budget
	raw, _ := json.Marshal(fields)
	return string(raw)
}

// tokenOnlyBudget 读取当前尝试的软预算标志；参数为请求ID，返回是否仅计 token，供结算使用，受互斥锁保护。
func (s *Server) tokenOnlyBudget(callID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt := s.budgetCalls[callID]
	return attempt != nil
}

// softBudgetCharge 使用出站时冻结价格计算实际token费用；返回账单、是否存在软预算尝试及计价有效性。
// 供recordSpend调用；失败只标记未定价，不编造费用或冻结额度。
func (s *Server) softBudgetCharge(callID string, usage map[string]any) (catalog.Charge, bool, bool) {
	s.mu.Lock()
	attempt := s.budgetCalls[callID]
	s.mu.Unlock()
	if attempt == nil {
		return catalog.Charge{}, false, false
	}
	charge, err := attempt.SoftPrice.Charge(usage)
	return charge, true, err == nil
}
