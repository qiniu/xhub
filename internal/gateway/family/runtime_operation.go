package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// unsupportedRuntimeOperations 精确列出无执行器的旧运维操作；不是禁用整个资源前缀或已有数据面能力。
var unsupportedRuntimeOperations = map[string]struct{}{
	"DELETE /schedule/anthropic_beta_headers_reload":     {},
	"DELETE /vantage/delete":                             {},
	"GET /schedule/anthropic_beta_headers_reload/status": {},
	"GET /vantage/settings":                              {},
	"POST /api/event_logging/batch":                      {},
	"POST /reload/anthropic_beta_headers":                {},
	"POST /schedule/anthropic_beta_headers_reload":       {},
	"POST /vantage/dry-run":                              {},
	"POST /vantage/export":                               {},
	"POST /vantage/init":                                 {},
	"PUT /vantage/settings":                              {},
}

// rejectRuntimeOperation 在通用KV读写之前拒绝无执行器的调度、预热、事件和Vantage操作。
// w/r为已完成鉴权及JSON校验的HTTP请求；命中返回true并写不缓存的501，不读写配置或访问外部服务。
// 仅允许精确方法/路径和单段预热名称（可带单个尾斜杠），其他入口返回false继续原有处理。
func rejectRuntimeOperation(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimSuffix(r.URL.Path, "/")
	_, matched := unsupportedRuntimeOperations[r.Method+" "+path]
	// 预热路由只接受单段名称，不能用前缀匹配吞掉未知子路径。
	if name, ok := strings.CutPrefix(path, "/lazy/warm/"); r.Method == http.MethodPost && ok && name != "" && !strings.Contains(name, "/") {
		matched = true
	}
	if !matched {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "This legacy runtime operation has no executor")
	return true
}
