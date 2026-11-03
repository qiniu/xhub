package usage

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

// logOptionPairs 从配置快照取当前允许且启用的公开模型/供应商对；allowed 为授权集合，nil 表示不限。
// 返回去重组合，仅供日志候选查询；跳过供应商配置壳及不完整身份，不访问密钥、不改授权或配置。
func logOptionPairs(entries []config.ModelEntry, allowed []string) []iam.LogOptionDeployment {
	grants := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		grants[name] = true
	}
	seen := make(map[iam.LogOptionDeployment]bool)
	result := make([]iam.LogOptionDeployment, 0)
	for _, entry := range entries {
		if entry.Disabled() || entry.ModelInfo["role"] == "provider" || strings.TrimSpace(entry.ModelName) == "" || (allowed != nil && !grants[entry.ModelName]) {
			continue
		}
		supplier, _ := resolvedMonitorIdentity(entry)
		pair := iam.LogOptionDeployment{Model: entry.ModelName, Supplier: supplier}
		if !seen[pair] {
			seen[pair] = true
			result = append(result, pair)
		}
	}
	return result
}

// LogFilterOptions 处理授权日志筛选候选；s 为已鉴权宿主，w/r 为 HTTP 响应和请求。
// 返回非空候选页与 has_more，要求明确日期、合法字段和有限分页；认证、存储或授权失败返回错误，绝不返回空的假成功。
// 日志筛选调用；模型及供应商只基于当前授权启用部署，历史不存在于候选时仍可手动查询，仅读取且限制查询时长。
func LogFilterOptions(s Host, w http.ResponseWriter, r *http.Request) {
	// 候选可能包含用户密钥指纹，成功和错误均禁止浏览器与共享缓存保存。
	w.Header().Set("Cache-Control", "no-store")
	p := s.RequireUser(w, r)
	if p == nil {
		return
	}
	scope, err := s.LogsScope(r, p)
	if err != nil {
		s.WriteAuthz(w, r, err)
		return
	}
	raw := r.URL.Query()
	field := raw.Get("field")
	search := strings.TrimSpace(raw.Get("q"))
	fields := map[string]bool{"model": true, "key_alias": true, "key_hash": true, "supplier": true, "inbound_endpoint": true}
	parsePositive := func(name string, defaultValue, max int) (int, bool) {
		value := raw.Get(name)
		if value == "" {
			return defaultValue, true
		}
		n, err := strconv.Atoi(value)
		return n, err == nil && n > 0 && n <= max
	}
	page, okPage := parsePositive("page", 1, 100)
	size, okSize := parsePositive("page_size", 50, 50)
	if !fields[field] || !okPage || !okSize || utf8.RuneCountInString(search) > 128 {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid filter option parameters")
		return
	}
	q, err := logQuery(r, scope)
	if err != nil || q.From.IsZero() || q.To.IsZero() || q.To.Sub(q.From) > 366*24*time.Hour {
		util.WriteError(w, http.StatusBadRequest, "invalid_request", "a valid date range of at most 366 days is required")
		return
	}
	db := s.Identity()
	if db == nil {
		util.WriteError(w, http.StatusServiceUnavailable, "unavailable", "usage storage is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var pairs []iam.LogOptionDeployment
	var deployments []iam.MonitorDeployment
	var allowed []string
	if field == "model" || field == "supplier" {
		var e error
		allowed, e = modelMonitorAllowedModels(ctx, db, p, "")
		if e != nil {
			s.WriteIAMError(w, r, e)
			return
		}
		list := s.ModelList()
		pairs = logOptionPairs(list, allowed)
		deployments = modelMonitorDeployments(list, allowed)
	}
	rows, more, err := db.ListLogFilterOptions(ctx, q, field, search, page, size, pairs, deployments, allowed)
	if err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"data": rows, "page": page, "page_size": size, "has_more": more})
}
