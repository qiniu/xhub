package family

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSettingsOperationRejects 用独立清单验证15条设置入口及尾斜杠的失败契约，nil存储证明拒绝先于存储访问；仅内存无需清理。
func TestSettingsOperationRejects(t *testing.T) {
	for _, route := range []string{
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
	} {
		for _, trailing := range []bool{false, true} {
			label := strings.ReplaceAll(route, "/", "|")
			suffix := ""
			if trailing {
				suffix = "/"
				label += "-trailing"
			}
			t.Run(label, func(t *testing.T) {
				method, path, _ := strings.Cut(route, " ")
				path = strings.ReplaceAll(path, "{name}", "fixture")
				w := httptest.NewRecorder()
				writeCatalogStore(nil, w, httptest.NewRequest(method, path+suffix, nil), map[string]any{"api_key": "private-fixture"})
				if w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"type":"unsupported_settings_operation"`) || strings.Contains(w.Body.String(), "private-fixture") {
					t.Fatalf("无消费者设置应明确失败: %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

// TestSettingsJSONRPCPriority 验证设置入口不会吞掉RPC信封，只有内存对象无需清理。
func TestSettingsJSONRPCPriority(t *testing.T) {
	w := httptest.NewRecorder()
	writeCatalogStore(nil, w, httptest.NewRequest("PATCH", "/update/ui_settings", nil), map[string]any{"jsonrpc": "2.0"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_protocol") {
		t.Fatal("RPC错误顺序改变", w.Body.String())
	}
}

// TestSettingsOperationBoundaries 验证真实主题、配置更新、错误方法与相似路径仍交给原服务；只有内存无需清理。
func TestSettingsOperationBoundaries(t *testing.T) {
	for _, route := range []string{"GET /get/ui_theme_settings", "POST /update/ui_theme_settings", "POST /upload/logo", "POST /config/update", "GET /config/list", "POST /get/ui_settings", "GET /get/ui_settings/extra", "GET /get/ui_settings//", "PATCH /update/ui_theme_settings", "GET /settings/new"} {
		method, path, _ := strings.Cut(route, " ")
		w := httptest.NewRecorder()
		if rejectSettingsOperation(w, httptest.NewRequest(method, path, nil)) || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "" {
			t.Fatal("误拦截真实服务或扩大范围", route)
		}
	}
}
