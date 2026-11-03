package regression

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// settingsOperationCases 固化C3的15条设置入口；与生产匹配器独立维护，避免同错同过。
var settingsOperationCases = []string{
	"GET /config/field/info",
	"GET /config/yaml",
	"GET /get/default_team_settings",
	"GET /get/internal_user_settings",
	"GET /get/ui_settings",
	"GET /get/user_banner",
	"GET /get_favicon",
	"GET /get_image",
	"GET /get_logo_url",
	"GET /settings",
	"GET /sso/get/ui_settings",
	"PATCH /update/default_team_settings",
	"PATCH /update/internal_user_settings",
	"PATCH /update/ui_settings",
	"PATCH /update/user_banner",
}

// TestSettingsOperationBoundary 使用真实HTTP和隔离PostgreSQL验证未实现设置不能读写公共KV或发上游请求。
// 管理员、普通用户、匿名及JSON错误逐项校验；harness清理用户、配置与schema，不修改线上数据。
func TestSettingsOperationBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("settings-chat"))
	admin := h.adminSession()
	user := h.openScope(t, admin, "settings-ops")
	for _, kind := range []string{"get", "update", "sso", "config", "settings", "get_image", "get_favicon", "get_logo_url"} {
		for _, id := range []string{"fixture", "default_team_settings", "internal_user_settings", "ui_settings", "user_banner", "yaml", "info"} {
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
	for _, route := range settingsOperationCases {
		t.Run(strings.ReplaceAll(route, "/", "|"), func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			path = strings.ReplaceAll(path, "{name}", "fixture")
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {user.session, 403}, {admin, 501}} {
				r := h.do(method, path, actor.token, map[string]any{"id": "fixture", "api_key": "private-input-fixture", "enabled": true})
				if r.status != actor.status {
					t.Fatalf("设置操作鉴权或能力边界错误: %s %s", route, r.describe())
				}
				if actor.status == 501 {
					e, ok := r.json()["error"].(map[string]any)
					if !ok || e["type"] != "unsupported_settings_operation" || r.header("Cache-Control") != "no-store" {
						t.Fatal("缺少明确失败契约", r.describe())
					}
					if strings.Contains(r.text(), "private-input-fixture") || strings.Contains(r.text(), "private-config-fixture") {
						t.Fatal("设置失败不应回显凭据或存量配置", r.describe())
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
				t.Fatal("旧设置操作不应修改公共KV", route)
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("旧设置操作不应调用上游", route)
			}
		})
	}
	h.ok("GET", "/auth/me", admin, nil)
	h.ok("GET", "/auth/me", user.session, nil)
	// 相邻真实主题服务必须继续读写同一配置；不能把旧品牌别名的拒绝扩展到真实服务。
	h.ok("PATCH", "/update/ui_theme_settings", admin, map[string]any{"primary_color": "#112233"})
	h.ok("POST", "/upload/logo", admin, map[string]any{"logo_url": "https://example.invalid/logo.svg"})
	theme := h.ok("GET", "/get/ui_theme_settings", admin, nil).json()
	if theme["primary_color"] != "#112233" || theme["logo_url"] != "https://example.invalid/logo.svg" {
		t.Fatal("真实主题未读回", theme)
	}
	request := map[string]any{"model": "settings-chat", "messages": []any{map[string]any{"role": "user", "content": "Verify settings isolation."}}}
	if r := h.do("POST", "/v1/chat/completions", user.key, request); r.status != 200 || len(h.upstreamCalls()) != 1 {
		t.Fatal("正常对话被设置边界破坏", r.describe())
	}
	blocked := h.openScope(t, admin, "settings-zero-budget")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if r := h.do("POST", "/v1/chat/completions", blocked.key, request); r.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算规则改变", r.describe())
	}

}
