package gateway

import (
	"net/http"

	"github.com/sunqirui1987/xhub/internal/gateway/cacheadmin"
)

// cacheManagement 保留缓存管理入口的原管理员鉴权，再交给独立缓存边界处理。
// w/r为HTTP请求响应；accessModule注册调用；拒绝身份不读取缓存、不写配置或计费。
func (s *Server) cacheManagement(w http.ResponseWriter, r *http.Request) {
	if s.requireManage(w, r) == nil {
		return
	}
	cacheadmin.Serve(s.Cache, Version, w, r)
}
