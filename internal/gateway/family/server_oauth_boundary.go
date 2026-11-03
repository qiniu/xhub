package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// rejectServerOAuth 在原管理鉴权之后拒绝未实现的旧server授权流程；w/r为响应和请求。
// 返回true表示已写入501；ServeMgmt据此停止解析和存储，避免授权正文被当作普通配置保存或删除。
// 仅匹配已注册的方法/路径，不影响普通server CRUD、企业SSO或已独立处理的授权入口。
func rejectServerOAuth(w http.ResponseWriter, r *http.Request) bool {
	if !isServerOAuthOperation(r.Method, r.URL.Path) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "legacy server OAuth is not implemented")
	return true
}

// isServerOAuthOperation 判断method/path是否属于七个保留的OAuth操作；允许末尾斜杠，无副作用。
// 按完整路径结构匹配，不能按token/oauth后缀拦截，以免误伤以这些词命名的普通server。
func isServerOAuthOperation(method, path string) bool {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(parts) < 4 || parts[0] != "" || parts[1] != "server" {
		return false
	}
	if len(parts) == 4 {
		if parts[2] == "oauth" && parts[3] == "session" {
			return method == http.MethodPost
		}
		return parts[2] != "" && parts[3] == "oauth-user-credential" && (method == http.MethodPost || method == http.MethodDelete)
	}
	if len(parts) != 5 || parts[3] == "" {
		return false
	}
	if parts[2] == "oauth" {
		switch parts[4] {
		case "authorize":
			return method == http.MethodGet
		case "register", "token":
			return method == http.MethodPost
		}
	}
	return parts[2] != "" && parts[3] == "oauth-user-credential" && parts[4] == "status" && method == http.MethodGet
}
