package gateway

import (
	"net/http"

	"github.com/sunqirui1987/xhub/internal/util"
)

// authorizeFlow 保留旧本地连接查询URL，但不把测试fixture当作真实授权会话。
// w/r为HTTP响应与请求，由GET /authorize/flow调用；始终明确501，不读取或创建会话，也不调用外部IdP。
func (s *Server) authorizeFlow(w http.ResponseWriter, r *http.Request) {
	writeUnsupportedAuthorization(w)
}

// authorizeComplete 拒绝没有执行器的本地授权提交，不能把任意正文标为已完成。
// w/r为HTTP响应与请求，由POST /authorize/complete调用；返回501，无授权、重定向、存储或网络副作用。
func (s *Server) authorizeComplete(w http.ResponseWriter, r *http.Request) {
	writeUnsupportedAuthorization(w)
}

// mcpOAuthToken 保留MCP令牌交换URL，明确当前未实现授权码交换，而非签发无效随机token。
// w/r为HTTP响应与请求，由专用路由调用；任何身份和正文均返回501，不解析或回显授权码、PKCE及客户端秘密。
// 企业SSO使用独立/auth/enterprise/*处理器，不经过本函数；这不是一个可用的OAuth授权服务器。
func (s *Server) mcpOAuthToken(w http.ResponseWriter, r *http.Request) {
	writeUnsupportedAuthorization(w)
}

// writeUnsupportedAuthorization 向旧授权入口写统一的不支持响应；w是HTTP输出，无返回值。
// 仅供本文件三个占位入口调用；禁止缓存，不签发token或cookie，不把未执行能力包装成OAuth成功包络。
func writeUnsupportedAuthorization(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	util.WriteError(w, http.StatusNotImplemented, "not_implemented", "local authorization and MCP OAuth token exchange are not implemented")
}
