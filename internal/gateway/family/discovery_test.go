package family

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDiscoveryBoundary 验证精确九条目录不依赖存储，错误/近似方法路径不会被误接管。
// 前置纯HTTP记录器和空store，涵盖正常/尾斜杠/失败输入；无需外部数据清理。
func TestDiscoveryBoundary(t *testing.T) {
	for _, path := range []string{"/", "/end_user/daily/activity", "/network/client-ip", "/openapi-registry", "/registry.json", "/routes", "/user/available_roles", "/utils/available_routes", "/search_tools/ui/available_providers"} {
		suffixes := []string{"", "/"}
		if path == "/" {
			suffixes = []string{""}
		}
		for _, suffix := range suffixes {
			w := httptest.NewRecorder()
			writeCatalogStore(nil, w, httptest.NewRequest("GET", path+suffix, nil), map[string]any{})
			want := 501
			if path == "/search_tools/ui/available_providers" {
				want = 200
			}
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("错误目录契约或依赖存储", path, w)
			}
			if want == 501 && !strings.Contains(w.Body.String(), "unsupported_discovery_operation") {
				t.Fatal("未明确拒绝", w)
			}
		}
	}
	for _, tc := range []struct{ method, path string }{{"POST", "/routes"}, {"HEAD", "/routes"}, {"GET", "//"}, {"GET", "/routes/item"}, {"GET", "/v1/mcp/registry.json"}, {"GET", "/user/available_roles/extra"}, {"GET", "/search_tools/provider"}, {"GET", "/routes//"}} {
		w := httptest.NewRecorder()
		if writeDiscovery(w, httptest.NewRequest(tc.method, tc.path, nil)) || w.Body.Len() != 0 {
			t.Fatal("错误接管邻居入口", tc)
		}
	}
}

// TestDiscoveryJSONValidation 验证原管理JSON/RPC校验仍优先，不能因静态目录跳过错误输入；无数据库需清理。
func TestDiscoveryJSONValidation(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `null`, `{"jsonrpc":"2.0"}`} {
		w := httptest.NewRecorder()
		ServeMgmt(catalogPersistenceHost{}, w, httptest.NewRequest("GET", "/routes", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal("改变原JSON校验", body, w)
		}
	}
}
