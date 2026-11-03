package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// rejectServerSubmission 拒绝没有执行器的旧server导入、提交和审批操作；w/r为已鉴权且JSON已解析的响应与请求。
// 由管理目录入口调用，命中返回true并写501，否则返回false且不修改响应；必须在存储之前执行，防止动作名误作资源ID。
func rejectServerSubmission(w http.ResponseWriter, r *http.Request) bool {
	if !isServerSubmissionOperation(r.Method, r.URL.Path) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "legacy server import and submission approval are not implemented")
	return true
}

// isServerSubmissionOperation 按方法和完整路径匹配五个目录操作，返回是否属于未实现的审批流程。
// 参数来自已注册路由；允许单个尾斜杠，不把同名普通资源、未知方法、其他前缀或已退役的/v1/mcp入口纳入此边界。
func isServerSubmissionOperation(method, path string) bool {
	path = strings.TrimSuffix(path, "/")
	if method == http.MethodPost && (path == "/server/import" || path == "/server/register") {
		return true
	}
	if method == http.MethodGet && path == "/server/submissions" {
		return true
	}
	parts := strings.Split(path, "/")
	return method == http.MethodPut && len(parts) == 4 && parts[1] == "server" && parts[2] != "" && (parts[3] == "approve" || parts[3] == "reject")
}
