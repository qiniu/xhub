package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// rejectServerRuntime 拒绝缺少真实执行器或个人隔离存储的六个旧server操作；w/r为响应和请求。
// 在ServeMgmt完成原鉴权与JSON解析后、writeCatalogPersist取存储之前调用；返回true表示已输出501。
// 不触碰配置、秘密或外部服务；普通server CRUD与独立OAuth/SSO/技能处理器不在此边界内。
func rejectServerRuntime(w http.ResponseWriter, r *http.Request) bool {
	if !isServerRuntimeOperation(r.Method, r.URL.Path) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "legacy server health and per-user configuration are not implemented")
	return true
}

// isServerRuntimeOperation 按method和path精确识别旧健康/个人凭据/环境变量操作；尾斜杠等价，无副作用。
// 普通资源可以名为user-credential或user-env-vars；禁止只按后缀匹配导致误伤正常配置读写。
func isServerRuntimeOperation(method, path string) bool {
	path = strings.TrimSuffix(path, "/")
	if path == "/server/health" {
		return method == http.MethodGet
	}
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "server" || parts[2] == "" {
		return false
	}
	switch parts[3] {
	case "user-credential":
		return method == http.MethodPost || method == http.MethodDelete
	case "user-env-vars":
		return method == http.MethodGet || method == http.MethodPost || method == http.MethodDelete
	default:
		return false
	}
}
