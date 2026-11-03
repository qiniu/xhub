package regression

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// runtimeOperationCases 固化C3的12条运维入口；与生产匹配器独立维护，避免同错同过。
var runtimeOperationCases = []string{
	"DELETE /schedule/anthropic_beta_headers_reload",
	"DELETE /vantage/delete",
	"GET /schedule/anthropic_beta_headers_reload/status",
	"GET /vantage/settings",
	"POST /api/event_logging/batch",
	"POST /lazy/warm/{name}",
	"POST /reload/anthropic_beta_headers",
	"POST /schedule/anthropic_beta_headers_reload",
	"POST /vantage/dry-run",
	"POST /vantage/export",
	"POST /vantage/init",
	"PUT /vantage/settings",
}

// TestRuntimeOperationBoundary 使用真实HTTP和隔离PostgreSQL验证未实现操作不能读写公共KV或发上游请求。
// 管理员、普通用户、匿名及JSON错误逐项校验；harness清理用户、配置与schema，不修改线上数据。
func TestRuntimeOperationBoundary(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	user := h.openScope(t, admin, "runtime-ops")
	for _, kind := range []string{"schedule", "vantage", "lazy", "reload", "event_logging"} {
		for _, id := range []string{"fixture", "anthropic_beta_headers_reload", "status", "settings", "export", "batch", "warm", "anthropic_beta_headers"} {
			raw, _ := json.Marshal(map[string]any{"id": id, "marker": "private-config-fixture"})
			if err := h.store.PutKV(kind, id, string(raw)); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range runtimeOperationCases {
		t.Run(strings.ReplaceAll(route, "/", "|"), func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			path = strings.ReplaceAll(path, "{name}", "fixture")
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {user.session, 403}, {admin, 501}} {
				r := h.do(method, path, actor.token, map[string]any{"id": "fixture", "api_key": "private-input-fixture", "enabled": true})
				if r.status != actor.status {
					t.Fatalf("运维操作鉴权或能力边界错误: %s %s", route, r.describe())
				}
				if actor.status == 501 {
					e, ok := r.json()["error"].(map[string]any)
					if !ok || e["type"] != "unsupported_operation" || r.header("Cache-Control") != "no-store" {
						t.Fatal("缺少明确失败契约", r.describe())
					}
					if strings.Contains(r.text(), "private-input-fixture") || strings.Contains(r.text(), "private-config-fixture") {
						t.Fatal("运维失败不应回显凭据或存量配置", r.describe())
					}
				}
			}
			if method != "GET" {
				if r := catalogRawRequest(t, h, method, path, admin, `{"id":`); r.status != 400 {
					t.Fatal("无效JSON顺序改变", r.describe())
				}
				if r := h.do(method, path, admin, map[string]any{"jsonrpc": "2.0"}); r.status != 400 {
					t.Fatal("REST入口误接受RPC", r.describe())
				}
			}
			after, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("旧运维操作不应修改公共KV", route)
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("旧运维操作不应调用上游", route)
			}
		})
	}
	h.ok("GET", "/auth/me", admin, nil)
	h.ok("GET", "/auth/me", user.session, nil)
}
