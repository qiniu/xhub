package usage

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

// ModelMonitor 接收登录用户的模型监控查询；参数为宿主和 HTTP 请求响应，无返回值。
// 默认最近七个本地日，最多九十天；日期、时区非法返回 400，无数据库返回 503。
// usage.Module 调用；沿用 UsageScope，仅汇总调用方可见的账单，无持久化副作用。
func ModelMonitor(s Host, w http.ResponseWriter, r *http.Request) {
	p := s.RequireUser(w, r)
	if p == nil {
		return
	}
	sc, err := s.UsageScope(r, p, r.URL.Query().Get("team_id"))
	if err != nil {
		s.WriteAuthz(w, r, err)
		return
	}
	q, tz, err := modelMonitorQuery(r, time.Now())
	if err != nil {
		util.WriteJSON(w, 400, map[string]any{"error": map[string]any{"type": "invalid_monitor_query", "message": err.Error()}})
		return
	}
	q.Cond = sc.Cond
	db := s.Identity()
	if db == nil {
		util.WriteJSON(w, 503, map[string]any{"error": map[string]any{"type": "monitor_unavailable", "message": "Usage storage is unavailable"}})
		return
	}
	// 配置目录独立于统计窗口；省略团队筛选也必须按当前账号实时授权收窄。
	// 显式团队已由 UsageScope 校验可见性；目录权限与账单统计范围分别计算，不能互相替代。
	allowed, err := modelMonitorAllowedModels(r.Context(), db, p, q.TeamID)
	if err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	options := modelMonitorOptions(s.ModelList(), allowed)
	rows, err := db.ModelMonitor(r.Context(), q, tz)
	if err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	summary := iam.ModelMonitorRow{Kind: "summary"}
	models := []iam.ModelMonitorRow{}
	trend := []iam.ModelMonitorRow{}
	for _, row := range rows {
		switch row.Kind {
		case "summary":
			summary = row
		case "model":
			models = append(models, row)
		case "trend":
			trend = append(trend, row)
		}
	}
	util.WriteJSON(w, 200, map[string]any{"options": options, "summary": summary, "models": models, "trend": trend, "start_date": q.From.Add(time.Duration(tz) * time.Minute).Format("2006-01-02"), "end_date": q.To.Add(time.Duration(tz)*time.Minute - time.Nanosecond).Format("2006-01-02")})
}

// modelMonitorAccess 复用 IAM 的模型交集查询；监控只读取授权，不创建独立权限规则。
type modelMonitorAccess interface {
	// AllowedModelsForKey 读取密钥完整归属链的模型交集；输入上下文与密钥，返回模型或查询错误，无写入。
	AllowedModelsForKey(context.Context, *iam.Key) ([]string, error)
	// AllowedModelsForTeam 读取已授权团队与组织的模型交集；输入上下文与团队 ID，返回模型或查询错误，无写入。
	AllowedModelsForTeam(context.Context, string) ([]string, error)
	// AllowedModelsForUser 实时读取账号所属团队及组织的模型交集；输入上下文与账号 ID，返回模型或查询错误，无写入。
	AllowedModelsForUser(context.Context, string) ([]string, error)
}

// modelMonitorAllowedModels 为监控目录选择实时授权来源；ctx/db/p 为上下文、IAM 及已认证主体。
// teamID 是已经过 UsageScope 校验的可选团队；平台管理员保留全局目录，Key 始终应用自身限制。
// 返回 nil 表示授权链未限制，非 nil 空列表拒绝全部；读取失败原样返回，调用方必须拒绝响应。
// ModelMonitor 调用；省略 teamID 的普通会话与模型发现一样查询本人当前团队及组织，不信任会话缓存。
func modelMonitorAllowedModels(ctx context.Context, db modelMonitorAccess, p *auth.Principal, teamID string) ([]string, error) {
	if p == nil || db == nil {
		return nil, iam.ErrInvalid
	}
	if p.PlatformAdmin() || p.IsMaster() {
		return nil, nil
	}
	if p.Key != nil {
		return db.AllowedModelsForKey(ctx, p.Key)
	}
	if teamID != "" {
		return db.AllowedModelsForTeam(ctx, teamID)
	}
	return db.AllowedModelsForUser(ctx, p.UserID)
}

// modelMonitorOption 是不含凭据的供应商实际模型组合，供页面筛选使用。
type modelMonitorOption struct {
	Supplier string `json:"supplier"`
	Model    string `json:"model"`
}

// modelMonitorOptions 从配置 list 提取去重排序目录，handler 和单元测试调用。
// allowed 为许可别名，nil 不限制、空切片拒绝全部；供应商壳仅提供连接，不伪造模型。
// 返回非 nil 目录，空配置返回空数组；不访问账单、不修改配置。
func modelMonitorOptions(list []config.ModelEntry, allowed []string) []modelMonitorOption {
	grants := make(map[string]bool, len(allowed))
	for _, alias := range allowed {
		grants[alias] = true
	}
	seen := map[modelMonitorOption]bool{}
	out := []modelMonitorOption{}
	for _, entry := range list {
		if strings.TrimSpace(entry.ModelName) == "" || (allowed != nil && !grants[entry.ModelName]) {
			continue
		}
		supplier, model := entry.MonitorIdentity()
		if entry.ModelInfo["role"] == "provider" {
			model = ""
		}
		option := modelMonitorOption{supplier, model}
		if (supplier != "" || model != "") && !seen[option] {
			seen[option] = true
			out = append(out, option)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Supplier != out[j].Supplier {
			return out[i].Supplier < out[j].Supplier
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// modelMonitorQuery 校验监控日期及浏览器时区；参数 r 为查询、now 为默认窗口基准。
// 返回未附权限的条件、东向分钟偏移及错误；handler 和单元测试调用，无副作用。
func modelMonitorQuery(r *http.Request, now time.Time) (iam.ModelMonitorQuery, int, error) {
	raw := r.URL.Query()
	tz := 0
	if value := raw.Get("timezone"); value != "" {
		offset, err := strconv.Atoi(value)
		if err != nil || offset < -840 || offset > 840 || offset%15 != 0 {
			return iam.ModelMonitorQuery{}, 0, queryError("Invalid timezone")
		}
		tz = -offset
	}
	end := raw.Get("end_date")
	if end == "" {
		end = now.Add(time.Duration(tz) * time.Minute).Format("2006-01-02")
	}
	endDay, err := time.Parse("2006-01-02", end)
	if err != nil {
		return iam.ModelMonitorQuery{}, 0, queryError("Invalid end_date")
	}
	start := raw.Get("start_date")
	if start == "" {
		start = endDay.AddDate(0, 0, -6).Format("2006-01-02")
	}
	startDay, err := time.Parse("2006-01-02", start)
	if err != nil || startDay.After(endDay) || endDay.Sub(startDay) >= 90*24*time.Hour {
		return iam.ModelMonitorQuery{}, 0, queryError("Date range must contain 1 to 90 days")
	}
	return iam.ModelMonitorQuery{UsageQuery: iam.UsageQuery{From: startDay.Add(-time.Duration(tz) * time.Minute), To: endDay.AddDate(0, 0, 1).Add(-time.Duration(tz) * time.Minute), Model: raw.Get("model"), TeamID: raw.Get("team_id")}, Supplier: raw.Get("supplier")}, tz, nil
}

// queryError 为查询校验提供无额外依赖的错误类型；仅由 modelMonitorQuery 返回，无副作用。
type queryError string

// Error 返回协议错误文本；无参数，供 HTTP 错误序列化使用，不修改数据。
func (e queryError) Error() string { return string(e) }
