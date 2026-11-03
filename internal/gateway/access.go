// access.go 保留客户列表兼容入口，不进入推理或计费。缓存管理已移至cache_management.go及cacheadmin。

package gateway

import (
	"net/http"

	"github.com/sunqirui1987/xhub/internal/gateway/metadataconfig"
	"github.com/sunqirui1987/xhub/internal/util"
)

// The SSO code generator and its exchange are gone. The pair minted an
// administrator session from an unauthenticated request: ssoGenerate handed out
// a code to anyone and loginExchange turned it into user "admin" with role
// "proxy_admin" without consulting any account. Authentication is now
// username-plus-password against the users table and nothing else.

// customerList 为两个旧客户列表入口提供相同合并投影；w/r为HTTP响应和请求。
// 先沿用管理鉴权，再委派元数据服务；任一空间读取失败返回脱敏500而非空/部分成功，不改变存储或预算。
func (s *Server) customerList(w http.ResponseWriter, r *http.Request) {
	if s.requireManage(w, r) == nil {
		return
	}
	if s.Store == nil {
		util.WriteError(w, 503, "unavailable", "catalog store unavailable")
		return
	}
	out, err := metadataconfig.CustomerList(s.Store)
	if err != nil {
		util.Error("customer catalog read failed error_type=%T", err)
		util.WriteError(w, 500, "internal", "catalog store failure")
		return
	}
	util.WriteJSON(w, 200, map[string]any{"object": "list", "data": out, "customers": out, "end_users": out})
}
