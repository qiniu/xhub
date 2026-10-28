// access.go serves cache probes and the customer list. These routes do not
// enter the inference loop and do not record spend. Email events, the IP
// allow-list, and SCIM used to live here; those pages are gone.

package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/sunqirui1987/xhub/internal/gateway/metadataconfig"
	"github.com/sunqirui1987/xhub/internal/util"
)

// The SSO code generator and its exchange are gone. The pair minted an
// administrator session from an unauthenticated request: ssoGenerate handed out
// a code to anyone and loginExchange turned it into user "admin" with role
// "proxy_admin" without consulting any account. Authentication is now
// username-plus-password against the users table and nothing else.

// flushCache 在管理身份授权通过后清空当前进程的推理响应缓存；未授权请求不改变缓存。
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 access.go 内使用
// 测试：access_cache_test.go；regression/consistency_test.go。
// 返回：无。状态码和正文写进调用方的响应。
func (s *Server) flushCache(w http.ResponseWriter, r *http.Request) {
	if s.requireManage(w, r) == nil {
		util.Debug("access denied method=%s path=%s operation=cache_flush", r.Method, r.URL.Path)
		return
	}
	s.Cache.Flush()
	util.WriteJSON(w, 200, map[string]any{"status": "ok", "message": "cache flushed"})
}

// cacheSettings reads or stores cache settings. It does not return cached bodies.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 access.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *Server) cacheSettings(w http.ResponseWriter, r *http.Request) {
	if s.requireManage(w, r) == nil {
		return
	}
	if r.Method == http.MethodPost {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body == nil {
			body = map[string]any{}
		}
		raw, _ := json.Marshal(body)
		if err := s.Store.PutKV("cache", "settings", string(raw)); err != nil {
			util.WriteError(w, 500, "internal", err.Error())
			return
		}
		util.WriteJSON(w, 200, body)
		return
	}
	saved, err := s.Store.GetKV("cache", "settings")
	if err != nil {
		util.WriteJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	util.WriteJSON(w, 200, saved)
}

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

// cachePing checks whether the cache is usable.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 access.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *Server) cachePing(w http.ResponseWriter, r *http.Request) {
	if s.requireManage(w, r) == nil {
		return
	}
	util.WriteJSON(w, 200, map[string]any{
		"status":     "healthy",
		"healthy":    true,
		"cache_type": "memory",
		"ping":       "pong",
		"host":       nil,
		"port":       nil,
		"version":    Version,
	})
}
