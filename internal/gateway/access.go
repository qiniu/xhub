// access.go 保留客户列表兼容入口，不进入推理或计费。缓存管理已移至cache_management.go及cacheadmin。

package gateway

import (
	"net/http"

	"github.com/sunqirui1987/xhub/internal/gateway/family"
	"github.com/sunqirui1987/xhub/internal/util"
)

// The SSO code generator and its exchange are gone. The pair minted an
// administrator session from an unauthenticated request: ssoGenerate handed out
// a code to anyone and loginExchange turned it into user "admin" with role
// "proxy_admin" without consulting any account. Authentication is now
// username-plus-password against the users table and nothing else.

// customerList lists customers. With no data it returns an empty list instead of 404.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 access.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *Server) customerList(w http.ResponseWriter, r *http.Request) {
	if s.requireManage(w, r) == nil {
		return
	}
	var list []map[string]any
	for _, kind := range []string{"customer", "end_user"} {
		rows, _ := s.Store.ListKV(kind)
		list = append(list, rows...)
	}
	out := []map[string]any{}
	for _, row := range list {
		family.Freeze("customer", row)
		out = append(out, map[string]any{
			"user_id":    row["user_id"],
			"spend":      row["spend"],
			"blocked":    row["blocked"],
			"max_budget": row["max_budget"],
		})
	}
	util.WriteJSON(w, 200, map[string]any{"object": "list", "data": out, "customers": out, "end_users": out})
}
