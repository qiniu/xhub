package family

import (
	"github.com/sunqirui1987/xhub/internal/util"
	"net/http"
	"strings"
)

// unsupportedAnalyticsAliases 精确列举23条没有统计实现的旧入口；真实usage和management查询保持原路由。
var unsupportedAnalyticsAliases = map[string]struct{}{
	"GET /global/activity/exceptions":            {},
	"GET /global/activity/exceptions/deployment": {},
	"GET /global/all_end_users":                  {},
	"GET /global/spend":                          {},
	"GET /global/spend/report":                   {},
	"GET /key/spend/report":                      {},
	"GET /model/metrics":                         {},
	"GET /model/metrics/exceptions":              {},
	"GET /model/metrics/slow_responses":          {},
	"GET /model/streaming_metrics":               {},
	"GET /otel-spans":                            {},
	"GET /spend/logs":                            {},
	"GET /spend_logs/end_users":                  {},
	"GET /spend_logs/users":                      {},
	"GET /tag/dau":                               {},
	"GET /tag/distinct":                          {},
	"GET /tag/mau":                               {},
	"GET /tag/summary":                           {},
	"GET /tag/user-agent/per-user-analytics":     {},
	"GET /tag/wau":                               {},
	"GET /user/spend/report":                     {},
	"POST /global/spend/refresh":                 {},
	"POST /global/spend/reset":                   {},
}

// rejectAnalyticsAlias 为完成鉴权及JSON校验的旧统计入口写501；w/r为目录调度器请求，命中返回true。
// 仅接受精确方法/路径和单尾斜杠；不读取KV、不更改消费，不把相似名称推断为真实统计的兼容协议。
func rejectAnalyticsAlias(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := unsupportedAnalyticsAliases[r.Method+" "+strings.TrimSuffix(r.URL.Path, "/")]; !ok {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_analytics_operation", "This legacy analytics endpoint has no implementation; use the documented usage and log APIs")
	return true
}
