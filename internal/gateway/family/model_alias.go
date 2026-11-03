package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// unsupportedModelAliases 仅列出旧目录入口，不包含现有模型专用服务；动态单段别名在函数中匹配。
var unsupportedModelAliases = map[string]struct{}{
	"GET /fallback/login":           {},
	"GET /model/deprecations":       {},
	"GET /model/info":               {},
	"GET /model/settings":           {},
	"GET /v1/model/deprecations":    {},
	"GET /v1/model/info":            {},
	"POST /fallback":                {},
	"POST /make_public":             {},
	"POST /model/block":             {},
	"POST /model/unblock":           {},
	"POST /model_group/make_public": {},
}

// rejectModelAlias 精确拒绝未接入模型服务的旧别名；w/r为已鉴权并校验JSON的目录请求。
// 命中返回true及不可缓存501，否则返回false；不读取遗留KV，也不改变模型授权、启停、预算或fallback。
// 名称相似不代表输入输出等价，不能将通用KV结果冒充真实模型，或把未知旧字段默默映射到新操作。
func rejectModelAlias(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimSuffix(r.URL.Path, "/")
	_, matched := unsupportedModelAliases[r.Method+" "+path]
	if id, ok := strings.CutPrefix(path, "/fallback/"); ok && id != "" && !strings.Contains(id, "/") && (r.Method == "GET" || r.Method == "DELETE") {
		matched = true
	}
	if id, ok := strings.CutPrefix(path, "/models/"); ok && id != "" && !strings.Contains(id, "/") && r.Method == "GET" {
		matched = true
	}
	if !matched {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_model_operation", "This legacy model endpoint has no implementation; use the supported model management API")
	return true
}
