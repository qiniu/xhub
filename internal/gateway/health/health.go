// health.go serves liveness, readiness, and the startup config the dashboard
// reads. These handlers do not change models and do not record spend.

package health

import (
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/sunqirui1987/xhub/internal/util"
)

// LocalGatewayOrigin is the API clients and the console use when no public base is configured.
const LocalGatewayOrigin = "http://localhost:4000"

// PublicOrigin is the OpenAI-compatible API origin. XHUB_PUBLIC_ORIGIN overrides the local default. The value is an origin only: the console is not mounted under it.
// 参数：无。
// 返回 string（string）：OpenAI 兼容接口的源。XHUB_PUBLIC_ORIGIN 优先，否则 http://localhost:4000。
// 调用：仅在 health.go 内使用
// 测试：无直接单测
func PublicOrigin() string {
	if v := strings.TrimSpace(os.Getenv("XHUB_PUBLIC_ORIGIN")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return LocalGatewayOrigin
}

var logTraceOnceHealth sync.Once

// healthLive answers the liveness probe. It does not touch the database and returns status ok.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 health.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *handler) healthLive(w http.ResponseWriter, r *http.Request) {
	logTraceOnceHealth.Do(func() { util.Trace("enter gateway.healthLive") })
	util.Debug("process %s %s step=health check=liveliness", r.Method, r.URL.Path)
	util.SetCallID(w, util.CallID())
	util.WriteJSON(w, 200, map[string]any{"status": "ok"})
}

// healthReady answers the readiness probe. A failed database ping returns 503.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 health.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *handler) healthReady(w http.ResponseWriter, r *http.Request) {
	util.SetCallID(w, util.CallID())
	if err := s.ping(); err != nil {
		util.Error("process %s %s step=health check=readiness ok=false", r.Method, r.URL.Path)
		util.WriteError(w, 503, "not_ready", err.Error())
		return
	}
	util.Debug("process %s %s step=health check=readiness ok=true", r.Method, r.URL.Path)
	util.WriteJSON(w, 200, map[string]any{"status": "ready"})
}

// healthDetails 提供包含构建版本的真实就绪详情；w/r为响应与请求，由公开就绪详情入口调用。
// 成功保持旧响应形状；数据库探测失败返回503和脱敏错误，不把底层连接信息发给客户端。
// 每次调用实际执行ping，不缓存健康状态，不改变独立的存活检查和账务。
func (s *handler) healthDetails(w http.ResponseWriter, r *http.Request) {
	util.SetCallID(w, util.CallID())
	if err := s.ping(); err != nil {
		util.Error("readiness details failed error_type=%T", err)
		util.WriteError(w, http.StatusServiceUnavailable, "not_ready", "database is not ready")
		return
	}
	util.WriteJSON(w, 200, map[string]any{
		"status":          "ready",
		"litellm_version": s.version,
		"healthy_count":   1,
		"unhealthy_count": 0,
		"details":         []any{},
	})
}

// uiConfig returns the startup config the dashboard needs, such as the proxy base URL. It does not include secrets.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 health.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *handler) uiConfig(w http.ResponseWriter, r *http.Request) {
	util.SetCallID(w, util.CallID())
	util.WriteJSON(w, 200, map[string]any{
		"admin_ui_disabled":             false,
		"auto_redirect_to_sso":          false,
		"sso_configured":                s.sso != nil && s.sso(r),
		"hide_default_credentials_hint": false,
		"is_control_plane":              false,
		"proxy_base_url":                PublicOrigin(),
		"server_root_path":              "/",
	})
}

// Pinger 提供就绪检查的连接探测，Ping 返回错误，不写入数据。
type Pinger interface{ Ping() error }

// handler 仅持有探测函数和版本，不依赖网关 Server。
type handler struct {
	ping    func() error
	version string
	sso     func(*http.Request) bool
}

// Module serves liveness, readiness, and the dashboard startup config.
// 参数 db：数据库探测接口；version：公开构建版本，不依赖 Server。
// 返回 util.Module（util.Module）：可挂到网关上的模块。
// 调用：仅在 routes.go 内使用
// 测试：无直接单测
func Module(db Pinger, version string, sso ...func(*http.Request) bool) util.Module {
	s := &handler{ping: db.Ping, version: version}
	if len(sso) > 0 {
		s.sso = sso[0]
	}
	return util.Bind("health", func(reg util.Registrar) {
		reg.Handle("GET /health/liveliness", s.healthLive)
		reg.Handle("GET /health/liveness", s.healthLive)
		reg.Handle("GET /health/readiness", s.healthReady)
		reg.Handle("GET /health/readiness/details", s.healthDetails)
		reg.Handle("GET /health", s.healthReady)
		reg.Handle("GET /.well-known/litellm-ui-config", s.uiConfig)
		reg.Handle("GET /litellm/.well-known/litellm-ui-config", s.uiConfig)
	})
}
