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
	TokenOnly bool
}

// PrepareBudgetAttempt 为所有模型协议执行软预算准入，不再按输入窗口预占或改写输出上限。
// 参数为已鉴权身份、最终出站正文及部署；返回原始正文和是否放行，余额耗尽返回403，存储不可读返回503。
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
		if err == nil && state.Pending > 0 {
			err = s.FlushLocalUsage(r.Context())
		}
		if err != nil {
			util.WriteTypedError(w, r.URL.Path, 503, "billing_storage_pending", "pending token charges must settle before another request")
			return nil, false
		}
	}
	// 无Redis时失败账单可能只保存在PG；先恢复，失败才拒绝。
	// 并发结算会短暂产生正常pending，恢复成功后不能因新出现的在途记录误拒503。
	pending, err := s.IAM.Engine.Context(r.Context()).Table("usage_pending").Count()
	if err == nil && pending > 0 {
		err = s.IAM.FlushPendingUsage(r.Context(), 100)
	}
	if err != nil {
		util.WriteTypedError(w, r.URL.Path, 503, "billing_storage_pending", "pending token charges must settle before another request")
		return nil, false
	}
	kind, id := "user", p.UserID
	if p.Key != nil {
		kind, id = "key", p.KeyID
	}
	if err := s.IAM.CheckSoftBudgetBalance(r.Context(), kind, id, s.hotSpendRef); err != nil {
		if errors.Is(err, iam.ErrBudgetUnavailable) {
			util.WriteTypedError(w, r.URL.Path, http.StatusForbidden, "budget_exceeded", "available budget has been exhausted")
		} else {
			budgetRefusal(w, r.URL.Path, iam.ErrQuotaUnavailable)
		}
		return nil, false
	}
	s.mu.Lock()
	if s.budgetCalls == nil {
		s.budgetCalls = map[string]*budgetAttempt{}
	}
	tokenOnly := billing.SoftTokenOnly(dep, protocol)
	s.budgetCalls[callID] = &budgetAttempt{SoftPrice: billing.NewSoftPricePlan(dep, start, tokenOnly), TokenOnly: tokenOnly}
	s.mu.Unlock()
	w.Header().Set("X-XHub-Budget-State", "token_only")
	w.Header().Set("X-XHub-Billing-Scope", "tokens_only")
	w.Header().Set("X-XHub-Budget-Guarantee", "soft")
	w.Header().Set("X-XHub-Tool-Costs", "excluded")
	if !tokenOnly {
		w.Header().Set("X-XHub-Budget-State", "soft")
		w.Header().Set("X-XHub-Billing-Scope", "model_usage")
		w.Header().Del("X-XHub-Tool-Costs")
	}
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
	return attempt != nil && attempt.TokenOnly
}

// softBudgetCharge 使用出站时冻结价格计算费用；参数为请求ID和实测usage，返回账单、尝试存在性、有效性和安全原因码。
// 供recordSpend调用；失败保留未定价原因，不编造费用或冻结额度，亦不公开内部配置/原始错误。
func (s *Server) softBudgetCharge(callID string, usage map[string]any) (catalog.Charge, bool, bool, string) {
	s.mu.Lock()
	attempt := s.budgetCalls[callID]
	s.mu.Unlock()
	if attempt == nil {
		return catalog.Charge{}, false, false, ""
	}
	charge, err := attempt.SoftPrice.Charge(usage)
	return charge, true, err == nil, pricingFailureCode(err)
}

// pricingFailureCode 将本地结算错误转换成稳定日志原因码；参数err可为空，返回空串或有限枚举。
// 仅供费用观察使用，不改变结算结果；未知错误不直接输出，避免把内部值带入日志接口。
func pricingFailureCode(err error) string {
	if err == nil {
		return ""
	}
	switch err.Error() {
	case "measured cache reads have no reserved price":
		return "cache_read_price_missing"
	case "measured cache writes have no reserved price":
		return "cache_write_price_missing"
	default:
		return "pricing_unavailable"
	}
}
