package family

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestModelAliasRejects 用独立清单验证14条模型旧入口及尾斜杠的失败契约，nil存储证明拒绝先于存储访问；仅内存无需清理。
func TestModelAliasRejects(t *testing.T) {
	for _, route := range []string{
		"DELETE /fallback/{model}",
		"GET /fallback/login",
		"GET /fallback/{model}",
		"GET /model/deprecations",
		"GET /model/info",
		"GET /model/settings",
		"GET /models/{model_id}",
		"GET /v1/model/deprecations",
		"GET /v1/model/info",
		"POST /fallback",
		"POST /make_public",
		"POST /model/block",
		"POST /model/unblock",
		"POST /model_group/make_public",
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
				path = strings.NewReplacer("{model}", "fixture", "{model_id}", "fixture").Replace(path)
				w := httptest.NewRecorder()
				writeCatalogStore(nil, w, httptest.NewRequest(method, path+suffix, nil), map[string]any{"api_key": "private-fixture"})
				if w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"type":"unsupported_model_operation"`) || strings.Contains(w.Body.String(), "private-fixture") {
					t.Fatalf("模型旧接口应明确失败: %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

// TestModelAliasBoundaries 验证相似路径、错误方法及全部相邻真实模型服务不被拦截；内存请求无需清理。
func TestModelAliasBoundaries(t *testing.T) {
	for _, route := range []string{"GET /v2/model/info", "GET /v1/models", "GET /v1/models/fixture", "GET /model/available", "GET /model/groups", "GET /model_group/info", "POST /model/new", "POST /model/update", "POST /model/delete", "POST /model/disable", "POST /model/enable", "PUT /model/fallback", "PUT /model/default", "PATCH /model/fixture/update", "GET /models/", "GET /models/a/b", "GET /models/a//", "POST /models/a", "POST /fallback/a", "GET /fallback/", "DELETE /fallback/a/b", "GET /fallback/a//", "GET /model/info/extra"} {
		method, path, _ := strings.Cut(route, " ")
		w := httptest.NewRecorder()
		if rejectModelAlias(w, httptest.NewRequest(method, path, nil)) || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "" {
			t.Fatal("误拦截真实服务或扩大范围", route)
		}
	}
	w := httptest.NewRecorder()
	writeCatalogStore(nil, w, httptest.NewRequest("POST", "/model/block", nil), map[string]any{"jsonrpc": "2.0"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_protocol") {
		t.Fatal("RPC错误优先级改变", w.Body.String())
	}
}
