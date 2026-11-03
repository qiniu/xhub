package cacheadmin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/util"
)

// TestServe 验证已鉴权缓存边界的真实LRU读写、只读状态、明确拒绝与无缓存失败。
// 前置内存缓存，不依赖网络或数据库；每个案例独占实例，函数退出即清理。
func TestServe(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
		flush        bool
	}{
		{"GET", "/cache/settings", 200, false}, {"GET", "/cache/ping", 200, false}, {"GET", "/ping/", 200, false},
		{"POST", "/flushall", 200, true}, {"POST", "/cache/settings", 501, false}, {"POST", "/cache/settings/test", 501, false},
		{"POST", "/delete", 501, false}, {"POST", "/cache/flushall", 404, false}, {"DELETE", "/cache/settings", 404, false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			cache := util.NewCache()
			cache.Set("entry", []byte("original"))
			w := httptest.NewRecorder()
			Serve(cache, "test-version", w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("invalid-json-secret")))
			if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") {
				t.Fatal("状态或凭据泄露", w.Code, w.Body.String())
			}
			if _, exists := cache.Get("entry"); exists == tc.flush {
				t.Fatal("缓存清理范围错误")
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tc.status == 501 {
				if e := body["error"].(map[string]any); e["type"] != "unsupported_operation" {
					t.Fatal(body)
				}
			}
			if tc.path == "/cache/settings" && tc.method == "GET" && (body["scope"] != "process" || body["configurable"] != false || body["enabled"] != true) {
				t.Fatal(body)
			}
			if tc.path == "/cache/ping" && (body["healthy"] != true || body["version"] != "test-version") {
				t.Fatal(body)
			}
			if tc.flush && (body["status"] != "ok" || body["message"] != "cache flushed") {
				t.Fatal(body)
			}
		})
	}
	for _, cache := range []*util.DualCache{nil, {}} {
		for _, op := range []struct{ method, path string }{{"GET", "/cache/settings"}, {"GET", "/cache/ping"}, {"POST", "/flushall"}} {
			w := httptest.NewRecorder()
			Serve(cache, "test", w, httptest.NewRequest(op.method, op.path, nil))
			if w.Code != 503 || !strings.Contains(w.Body.String(), "cache_unavailable") {
				t.Fatal("空缓存不能报告健康", w.Code, w.Body.String())
			}
		}
	}
}
