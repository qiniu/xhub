package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/gateway/searchconfig"
	"github.com/sunqirui1987/xhub/internal/util"
)

// writeDiscovery 处理C7九条精确GET目录；w/r来自既有管理鉴权和JSON校验后的请求。
// 返回是否已写响应；搜索目录委派真实适配器定义，其余八条未实现别名明确501，不接触公共KV或上游。
// 根路径仅匹配单斜杠，其他路径允许一个尾斜杠；不接管同名详情、其他方法或标准管理模块。
func writeDiscovery(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path := r.URL.Path
	if path != "/" {
		path = strings.TrimSuffix(path, "/")
		if path == "/" {
			return false
		}
	}
	if path == "/search_tools/ui/available_providers" {
		w.Header().Set("Cache-Control", "no-store")
		util.WriteJSON(w, http.StatusOK, map[string]any{"providers": searchconfig.Providers()})
		return true
	}
	switch path {
	case "/", "/end_user/daily/activity", "/network/client-ip", "/openapi-registry", "/registry.json", "/routes", "/user/available_roles", "/utils/available_routes":
		w.Header().Set("Cache-Control", "no-store")
		util.WriteError(w, http.StatusNotImplemented, "unsupported_discovery_operation", "this legacy endpoint has no discovery or activity implementation")
		return true
	default:
		return false
	}
}
