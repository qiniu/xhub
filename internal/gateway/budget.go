package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/gateway/billing"
	"github.com/sunqirui1987/xhub/internal/iam"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	"github.com/sunqirui1987/xhub/internal/util"
)

var _ dpcontract.BudgetHost = (*Server)(nil)

// budgetAttempt 保存活跃请求最新尝试；金额及身份已在 PostgreSQL，内存只存本次冻结费率和清理事实。
type budgetAttempt struct {
	Hold                 *iam.BudgetReservation
	Plan                 billing.BudgetPlan
	Submitted, Finalized bool
	Reason               string
}

// initializeBudgetQueue 在当前进程首次启用硬额度前清算旧版本未预占的待结算账单。
// 参数 ctx 控制读取；返回队列不能排空的错误。仅在全部实例升级/停止旧节点后使用此保证，旧 writer 不得继续写入。
// 已初始化后新费用均被 PostgreSQL hold 覆盖，不在每次准入扫描 Redis；失败不标记初始化完成。
func (s *Server) initializeBudgetQueue(ctx context.Context) error {
	s.budgetInitMu.Lock()
	defer s.budgetInitMu.Unlock()
	if s.budgetQueueReady {
		return nil
	}
	s.FlushSpend()
	count, err := s.Live.PendingSpendCount()
	if err != nil || count != 0 {
		return iam.ErrQuotaUnavailable
	}
	count, err = s.IAM.Engine.Context(ctx).Table("usage_pending").Count()
	if err != nil || count != 0 {
		return iam.ErrQuotaUnavailable
	}
	s.budgetQueueReady = true
	return nil
}

// PrepareBudgetAttempt 为每次实际出站冻结上限、费率并在数据库中原子预占五层和项目额度。
// 参数是执行器已完成协议转换的最终正文及真实部署；返回实际出站正文、是否继续；失败已经写明 400/429/503。
// 有限额才要求可信上界；未支持能力明确拒绝。重试单独预占，旧尝试已提交但未知费用时保留。
func (s *Server) PrepareBudgetAttempt(w http.ResponseWriter, r *http.Request, p *auth.Principal, callID string, dep config.ModelEntry, protocol string, body []byte, start time.Time) ([]byte, bool) {
	s.finishBudgetCall(callID)
	kind, id := "user", p.UserID
	if p.Key != nil {
		kind, id = "key", p.KeyID
	}
	required, err := s.IAM.RequiresBudget(r.Context(), kind, id)
	if err != nil {
		budgetRefusal(w, r.URL.Path, iam.ErrQuotaUnavailable)
		return nil, false
	}
	if !required {
		return body, true
	}
	plan, encoded, err := billing.StrictBudgetPlan(dep, body, protocol, start)
	if err != nil {
		code := "unsupported_budget"
		var detail *billing.BudgetPlanError
		if errors.As(err, &detail) {
			code = detail.Code
		}
		util.WriteTypedError(w, r.URL.Path, 400, code, err.Error())
		return nil, false
	}
	if err := s.initializeBudgetQueue(r.Context()); err != nil {
		budgetRefusal(w, r.URL.Path, iam.ErrQuotaUnavailable)
		return nil, false
	}
	hold, err := s.IAM.ReserveBudget(r.Context(), util.CallID(), kind, id, plan.Amount, iam.BudgetReservationOptions{CallID: callID, Snapshot: plan.PriceSnapshot, StartedAt: start})
	if err != nil {
		if errors.Is(err, iam.ErrBudgetUnavailable) {
			util.WriteTypedError(w, r.URL.Path, 429, "budget_reservation_exceeded", "available budget is insufficient for the declared input window and output limit")
		} else {
			budgetRefusal(w, r.URL.Path, iam.ErrQuotaUnavailable)
		}
		return nil, false
	}
	// 计费归属以事务内读取的预占快照为准，避免成员转移和准入之间产生账单身份分叉。
	p.UserID, p.BillingTeamID, p.BillingOrgID = hold.UserID, hold.TeamID, hold.OrgID
	if p.Key != nil {
		p.Key.UserID = &hold.UserID
		if hold.ProjectID != "" {
			p.Key.ProjectID = &hold.ProjectID
		} else {
			p.Key.ProjectID = nil
		}
	}
	s.mu.Lock()
	if s.budgetCalls == nil {
		s.budgetCalls = map[string]*budgetAttempt{}
	}
	s.budgetCalls[callID] = &budgetAttempt{Hold: hold, Plan: plan}
	s.mu.Unlock()
	if plan.SearchCalls > 0 {
		w.Header().Set("X-XHub-Max-Tool-Calls", strconv.Itoa(plan.SearchCalls))
	}
	w.Header().Set("X-XHub-Max-Output-Tokens", strconv.Itoa(plan.OutputCap))
	w.Header().Set("X-XHub-Budget-Input-Bound", strconv.Itoa(plan.InputBound))
	w.Header().Set("X-XHub-Budget-Reserved", catalog.Format(plan.Amount))
	w.Header().Set("X-XHub-Budget-Reservation-Id", hold.RequestID)
	w.Header().Set("X-XHub-Budget-State", "reserved")
	util.Info("budget reserved call_id=%q reservation_id=%q amount=%s input_bound=%d output_cap=%d defaulted=%t", callID, hold.RequestID, catalog.Format(plan.Amount), plan.InputBound, plan.OutputCap, plan.Defaulted)
	return encoded, true
}

// MarkBudgetAttemptSubmitted 在 HTTP 发送前持久化已提交事实；失败或客户端已取消时拒绝外发。
// 参数为执行器响应/请求和调用 ID；返回是否可继续。标记成功后不能按网络错误自动退款。
func (s *Server) MarkBudgetAttemptSubmitted(w http.ResponseWriter, r *http.Request, callID string) bool {
	s.mu.Lock()
	attempt := s.budgetCalls[callID]
	s.mu.Unlock()
	if attempt == nil {
		return true
	}
	if r.Context().Err() != nil {
		util.WriteTypedError(w, r.URL.Path, 499, "request_cancelled", "request cancelled before upstream submission")
		return false
	}
	if err := s.IAM.SetBudgetReservationState(r.Context(), attempt.Hold.RequestID, "submitted"); err != nil {
		budgetRefusal(w, r.URL.Path, iam.ErrQuotaUnavailable)
		return false
	}
	s.mu.Lock()
	attempt.Submitted = true
	s.mu.Unlock()
	w.Header().Set("X-XHub-Budget-State", "submitted")
	return true
}

// finishBudgetCall 清理一次尝试的内存状态；只有确认尚未发送才退款，未知费用保留为 uncertain。
// 参数是调用 ID，供重试与 defer 清理；存储失败记录错误且保留占用，不能用进程结束等同上游免费。
func (s *Server) finishBudgetCall(callID string) {
	if s == nil || callID == "" {
		return
	}
	s.mu.Lock()
	attempt := s.budgetCalls[callID]
	delete(s.budgetCalls, callID)
	s.mu.Unlock()
	if attempt == nil || attempt.Finalized || s.IAM == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	if attempt.Submitted {
		if attempt.Reason == "" {
			attempt.Reason = "upstream outcome requires reconciliation"
		}
		err = s.IAM.SetBudgetReservationState(ctx, attempt.Hold.RequestID, "uncertain", attempt.Reason)
		util.Error("budget pending reconciliation call_id=%q reservation_id=%q amount=%s", callID, attempt.Hold.RequestID, catalog.Format(attempt.Hold.Amount))
	} else {
		err = s.IAM.ReleaseBudget(ctx, attempt.Hold.RequestID)
	}
	if err != nil {
		util.Error("budget cleanup failed call_id=%q reservation_id=%q err=%v", callID, attempt.Hold.RequestID, err)
	}
}

// budgetCharge 仅用冻结价格和完整实测用量结算当前成功尝试；失败/截断/超出声明保持占用等待核对。
// 参数为请求 ID、归一化用量、HTTP 状态、缓存事实；返回账单、是否启用硬额度、有效性、尝试 ID 和观测快照。
// 有效时标记 settling，直到真正 RecordUsage 事务成功才退款；不能在异步入队时释放。
func (s *Server) budgetCharge(callID string, rawUsage map[string]any, status int, cached bool) (catalog.Charge, bool, bool, string, map[string]any) {
	s.mu.Lock()
	attempt := s.budgetCalls[callID]
	s.mu.Unlock()
	if attempt == nil {
		return catalog.Charge{}, false, false, "", nil
	}
	metadata := map[string]any{"reservation_id": attempt.Hold.RequestID, "reserved": attempt.Plan.Amount, "input_bound": attempt.Plan.InputBound, "output_cap": attempt.Plan.OutputCap, "state": "uncertain"}
	var charge catalog.Charge
	var err error
	if status >= 400 || cached {
		err = errors.New("upstream billing outcome is uncertain")
	} else {
		if err = billing.ValidateStrictBudgetUsage(rawUsage, attempt.Plan); err == nil {
			measured := catalog.NormalizeUsage(llmdialogue.NormalizeDialogueUsage(rawUsage))
			if attempt.Plan.SearchCalls > 0 {
				// 搜索按已验证终态的search动作计费；不改变供应商原始usage或无契约通道的计量语义。
				measured.Searches = catalog.NormalizeUsage(map[string]any{"searches": rawUsage["xhub_web_search_searches"]}).Searches
			}
			charge, err = attempt.Plan.Charge(measured)
		}
	}
	if err != nil {
		metadata["reason"] = err.Error()
		s.mu.Lock()
		attempt.Reason = err.Error()
		s.mu.Unlock()
		util.Error("budget usage requires reconciliation call_id=%q reservation_id=%q err=%v", callID, attempt.Hold.RequestID, err)
		return catalog.Charge{}, true, false, "", metadata
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.IAM.SetBudgetReservationState(ctx, attempt.Hold.RequestID, "settling"); err != nil {
		util.Error("budget settling state update failed call_id=%q err=%v", callID, err)
	}
	s.mu.Lock()
	attempt.Finalized = true
	s.mu.Unlock()
	metadata["state"] = "settling"
	return charge, true, true, attempt.Hold.RequestID, metadata
}

// budgetPriceSnapshot 把预占观测附加到现有价格 JSON；参数是计价快照与可空预算元数据。
// 返回兼容现有日志查看器的 JSON，无外部副作用；原快照不可解析时保留预算元数据，避免隐去未知占用。
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
