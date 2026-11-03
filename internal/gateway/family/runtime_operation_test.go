package family

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRuntimeOperationRejects 用独立清单验证12条操作及尾斜杠的失败契约，nil存储证明拒绝先于存储访问；仅内存无需清理。
func TestRuntimeOperationRejects(t *testing.T) {
	for _, route := range []string{
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
				if w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"type":"unsupported_operation"`) || strings.Contains(w.Body.String(), "private-fixture") {
					t.Fatalf("无执行器操作应明确失败: %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

// TestRuntimeOperationBoundaries 验证正常配置、模型入口、错误方法及多层名称不被误匹配，RPC仍优先报400；无外部资源。
func TestRuntimeOperationBoundaries(t *testing.T) {
	for _, route := range []string{"POST /v1/chat/completions", "POST /config/update", "GET /get/ui_settings", "GET /model/available", "POST /policies", "POST /credentials", "GET /lazy/warm/model", "POST /lazy/warm/", "POST /lazy/warm/a/b", "POST /lazy/warm/a//", "POST /vantage/export/extra", "PATCH /vantage/settings", "GET /schedule/anthropic_beta_headers_reload/status/extra", "POST /api/event_logging/batches"} {
		t.Run(strings.ReplaceAll(route, "/", "|"), func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			w := httptest.NewRecorder()
			if rejectRuntimeOperation(w, httptest.NewRequest(method, path, nil)) || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "" {
				t.Fatal("运维匹配不能扩大拒绝范围", route)
			}
		})
	}
	w := httptest.NewRecorder()
	writeCatalogStore(nil, w, httptest.NewRequest("POST", "/vantage/export", nil), map[string]any{"jsonrpc": "2.0"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_protocol") {
		t.Fatal("RPC错误顺序改变", w.Body.String())
	}
}
