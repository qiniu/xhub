// Package cacheadmin 管理实际进程内响应缓存，不把历史配置存储当成运行时执行器。
package cacheadmin

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// Serve 处理已通过管理员鉴权的缓存入口；cache为本进程缓存，version为构建版本，w/r为HTTP响应请求。
// gateway适配器调用；设置及探测不访问数据库或Redis。仅flushall实际清理进程响应缓存，不改预算及用量。
// 未实现的配置、连接测试和按键删除返回501且不解析/回显凭据；未初始化缓存返回503，不冒充健康。
func Serve(cache *util.DualCache, version string, w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch r.Method + " " + path {
	case "POST /cache/settings", "POST /cache/settings/test", "POST /delete":
		util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "runtime cache configuration, connection testing and per-key deletion are not implemented")
		return
	case "GET /cache/settings", "GET /cache/ping", "GET /ping", "POST /flushall":
		// 进程内缓存没有外部连接；就绪意味着实际LRU实例已初始化，不声称Redis可达。
		if !cache.Ready() {
			util.WriteError(w, http.StatusServiceUnavailable, "cache_unavailable", "process response cache is unavailable")
			return
		}
	default:
		util.WriteError(w, http.StatusNotFound, "not_found", "Not Found")
		return
	}
	switch path {
	case "/cache/settings":
		util.WriteJSON(w, 200, map[string]any{"enabled": true, "cache_type": "memory", "scope": "process", "configurable": false})
	case "/flushall":
		cache.Flush()
		util.WriteJSON(w, 200, map[string]any{"status": "ok", "message": "cache flushed"})
	default:
		util.WriteJSON(w, 200, map[string]any{"status": "healthy", "healthy": true, "cache_type": "memory", "ping": "pong", "host": nil, "port": nil, "version": version})
	}
}
