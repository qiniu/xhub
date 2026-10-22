package identity

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"

	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

// readBulkUserBudgetRequest 严格读取批量预算JSON；参数为HTTP正文，返回完整命令或非法请求错误。
// 读取/解码失败（含浮点溢出）绝不能继续使用部分map，否则解析留下的nil会被误当显式null取消上限。
// 请求限制1MiB以约束1000目标之外的异常负载；调用方完成鉴权后使用，本函数不写数据库。
func readBulkUserBudgetRequest(body io.Reader) (iam.BulkUserBudgetInput, error) {
	const maxBody = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return iam.BulkUserBudgetInput{}, iam.ErrInvalid
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return iam.BulkUserBudgetInput{}, iam.ErrInvalid
	}
	return bulkUserBudgetFields(object)
}

// bulkUserBudgetFields 严格解析批量预算请求；参数为JSON对象，返回唯一目标模式或非法请求错误。
// 支持独立修改max_budget和budget_reset_day；缺省保持原值，额度null取消上限，周期0取消刷新。
// 两种契约为users:[{user_id,max_budget}]，或all_users:true加user_updates:{max_budget}，不访问存储。
func bulkUserBudgetFields(body map[string]any) (iam.BulkUserBudgetInput, error) {
	var in iam.BulkUserBudgetInput
	for key := range body {
		if key != "all_users" && key != "users" && key != "user_updates" {
			return in, iam.ErrInvalid
		}
	}
	if raw, exists := body["all_users"]; exists {
		v, ok := raw.(bool)
		if !ok {
			return in, iam.ErrInvalid
		}
		in.AllUsers = v
	}
	if in.AllUsers {
		if _, exists := body["users"]; exists {
			return in, iam.ErrInvalid
		}
		m, ok := body["user_updates"].(map[string]any)
		if !ok {
			return in, iam.ErrInvalid
		}
		if _, exists := m["user_id"]; exists {
			return in, iam.ErrInvalid
		}
		v, err := bulkUserChanges(m)
		in.MaxBudget, in.KeepBudget, in.BudgetResetDay = v.MaxBudget, v.KeepBudget, v.BudgetResetDay
		return in, err
	}
	if _, exists := body["user_updates"]; exists {
		return in, iam.ErrInvalid
	}
	list, ok := body["users"].([]any)
	if !ok || len(list) == 0 {
		return in, iam.ErrInvalid
	}
	if len(list) > iam.MaxBulkUserBudgets {
		return in, iam.ErrBulkUserBudgetTooLarge
	}
	seen := map[string]bool{}
	for _, value := range list {
		m, ok := value.(map[string]any)
		if !ok {
			return in, iam.ErrInvalid
		}
		id, ok := m["user_id"].(string)
		if !ok || id == "" || seen[id] {
			return in, iam.ErrInvalid
		}
		seen[id] = true
		for key := range m {
			if key != "user_id" && key != "max_budget" && key != "budget_reset_day" {
				return in, iam.ErrInvalid
			}
		}
		update, err := bulkUserChanges(m)
		if err != nil {
			return in, err
		}
		update.UserID = id
		in.Users = append(in.Users, update)
	}
	return in, nil
}

// bulkBudgetValue 读取明确提交的预算；参数为单项变更，返回有限非负数字或显式null，缺省/字符串/布尔值错误。
// 批量契约有意不沿用旧接口的空字符串清空语义，以免空表单误取消所有用户额度，无持久化副作用。
func bulkBudgetValue(m map[string]any) (*float64, error) {
	value, ok := m["max_budget"]
	if !ok {
		return nil, iam.ErrInvalid
	}
	if value == nil {
		return nil, nil
	}
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return nil, iam.ErrInvalid
	}
	return &n, nil
}

// UserBulkBudgetUpdate 处理平台管理员批量设置预算的真实接口；参数为鉴权宿主与HTTP请求/响应，无返回值。
// RequireManage在读取目标前拒绝普通成员、组织/团队管理员和推理Key；存储成功后返回全部真实快照，任何失败不报告部分成功。
func UserBulkBudgetUpdate(g Gate, w http.ResponseWriter, r *http.Request) {
	util.SetCallID(w, util.CallID())
	p := g.RequireManage(w, r)
	if p == nil {
		return
	}
	in, err := readBulkUserBudgetRequest(r.Body)
	if err == nil {
		var result iam.BulkUserBudgetResult
		result, err = g.Identity().AdminBulkUserBudgets(r.Context(), actorOf(p), in)
		if err == nil {
			results := make([]map[string]any, 0, len(result.Users))
			for i := range result.Users {
				u := &result.Users[i]
				results = append(results, map[string]any{"user_id": u.ID, "user_email": u.Email, "success": true, "updated_user": userPublic(u, nil)})
			}
			util.WriteJSON(w, 200, map[string]any{"atomic": true, "batch_id": result.BatchID, "results": results, "total_requested": len(results), "successful_updates": len(results), "failed_updates": 0})
			return
		}
	}
	switch {
	case errors.Is(err, iam.ErrInvalid):
		util.WriteError(w, 400, "bulk_budget_invalid", "Submit max_budget (nonnegative number or null) and/or budget_reset_day (0 to 31) for unique users")
	case errors.Is(err, iam.ErrBulkUserBudgetTooLarge):
		util.WriteError(w, 400, "bulk_budget_too_large", "An atomic batch supports at most 1000 users; select smaller batches")
	case errors.Is(err, iam.ErrNotFound):
		util.WriteError(w, 404, "bulk_budget_not_found", "At least one selected user no longer exists; no user was updated")
	case errors.Is(err, iam.ErrQuotaAllocation), errors.Is(err, iam.ErrMultipleTeams):
		util.WriteError(w, 400, "bulk_budget_allocation", "The batch violates current spending, reserved requests or parent and child budget allocations; no user was updated")
	default:
		g.WriteIAMError(w, r, err)
	}
}

// userResetDay 解析可选刷新日；缺省保持原值，0不刷新，1至31为日期，拒绝null、字符串及小数。
// 单人创建、更新及批量入口共用，不修改请求或持久化数据。
func userResetDay(body map[string]any) (*int, error) {
	raw, exists := body["budget_reset_day"]
	if !exists {
		return nil, nil
	}
	n, ok := raw.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 31 || n != math.Trunc(n) {
		return nil, iam.ErrInvalid
	}
	day := int(n)
	return &day, nil
}

// bulkUserChanges 读取预算与周期独立变更；至少显式提交一项，缺省预算保持，返回严格命令或错误。
// 批量解析器调用；不接受其他字段，以免忽略拼写错误造成看似保存成功。
func bulkUserChanges(m map[string]any) (iam.UserBudgetUpdate, error) {
	u := iam.UserBudgetUpdate{KeepBudget: true}
	for k := range m {
		if k != "user_id" && k != "max_budget" && k != "budget_reset_day" {
			return u, iam.ErrInvalid
		}
	}
	var err error
	if _, ok := m["max_budget"]; ok {
		u.KeepBudget = false
		u.MaxBudget, err = bulkBudgetValue(m)
		if err != nil {
			return u, err
		}
	}
	u.BudgetResetDay, err = userResetDay(m)
	if err != nil || (u.KeepBudget && u.BudgetResetDay == nil) {
		return u, iam.ErrInvalid
	}
	return u, nil
}
