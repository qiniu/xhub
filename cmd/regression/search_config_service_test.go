package regression

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSearchConfigServiceLifecycle 验证管理服务迁移后的真实 HTTP/PG 行为及搜索出站凭据轮换。
// 前置隔离 schema、本地严格鉴权上游；覆盖所有 CRUD 权限、默认值、扩展/null/零、upsert、掩码、401 和恢复。
// 通过真实接口删除配置，harness 清理账号/schema，httptest 关闭监听，不访问商业供应商。
func TestSearchConfigServiceLifecycle(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "search-service")
	const id = "service-search"
	const path = "/search_tools/" + id
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer service-search-key" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Service result","url":"https://example.com/service","content":"Search config used"}]}`))
	}))
	defer upstream.Close()
	for _, op := range []struct{ method, path string }{{"GET", "/search_tools/list"}, {"GET", path}, {"POST", "/search_tools"}, {"PUT", path}, {"DELETE", path}} {
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {owner.session, 403}} {
			got := h.do(op.method, op.path, actor.token, map[string]any{"search_tool_id": id})
			if got.status != actor.status {
				t.Fatalf("管理权限改变 %s %s: %s", op.method, op.path, got.describe())
			}
		}
	}
	created := h.ok("POST", "/search_tools", admin, map[string]any{"search_tool_id": id, "name": "Service search", "zero": 0, "note": nil, "enabled": false, "password": "ignored", "search_tool": map[string]any{"extension": "legacy"}, "litellm_params": map[string]any{"search_provider": "tavily", "api_key": "service-search-key", "api_base": upstream.URL}})
	obj := created.json()
	if obj["search_tool_alias"] != "Service search" || obj["search_tool_name"] != "" || obj["extension"] != "legacy" || obj["zero"] != float64(0) || obj["enabled"] != false || obj["note"] != nil || obj["password"] != nil || strings.Contains(created.text(), "service-search-key") {
		t.Fatalf("创建兼容字段改变: %s", created.describe())
	}
	read := h.ok("GET", path, admin, nil)
	h.ok("PUT", path, admin, read.json())
	// 每次按保存 ID 探测，不传临时配置；真实上游鉴权证明服务保存结果已进入执行链。
	probe := func(status int) {
		t.Helper()
		before := calls.Load()
		got := h.do("POST", "/search_tools/test_connection", admin, map[string]any{"search_tool_id": id})
		if got.status != status || calls.Load() != before+1 {
			t.Fatalf("真实搜索调用错误: %s calls=%d", got.describe(), calls.Load())
		}
		if status == 200 && got.json()["results_count"] != float64(1) {
			t.Fatal("未收到真实搜索结果")
		}
	}
	probe(200)
	h.ok("PUT", path, admin, map[string]any{"litellm_params": map[string]any{"api_key": "invalid-key"}})
	probe(401)
	h.ok("PUT", path, admin, map[string]any{"litellm_params": map[string]any{"api_key": "service-search-key"}})
	probe(200)
	for _, raw := range []string{`{"name":`, `{"jsonrpc":"2.0","method":"tools/list"}`} {
		got := catalogRawRequest(t, h, "PUT", path, admin, raw)
		if got.status != 400 {
			t.Fatalf("非法正文应拒绝: %s", got.describe())
		}
	}
	probe(200)
	h.ok("DELETE", path, admin, nil)
	if got := h.do("GET", path, admin, nil); got.status != 404 {
		t.Fatal("删除后仍存在", got.describe())
	}
	upsert := h.ok("PUT", path, admin, map[string]any{"name": "upsert", "note": nil, "zero": 0})
	if upsert.json()["created_at"] == nil || upsert.json()["search_tool_id"] != id {
		t.Fatal("缺失更新未保留upsert")
	}
	h.ok("POST", "/search_tools", admin, map[string]any{"search_tool_id": id, "name": "replacement"})
	if got := h.ok("GET", path, admin, nil).json(); got["zero"] != nil || got["name"] != "replacement" {
		t.Fatal("同名创建未替换")
	}
	h.ok("DELETE", path, admin, nil)
	if got := h.ok("DELETE", path, admin, nil).json()["deleted"]; got != float64(0) {
		t.Fatal("重复删除不幂等")
	}
	if rows := rowsOf(h.ok("GET", "/search_tools/list", admin, nil), "search_tools"); len(rows) != 0 {
		t.Fatal("测试配置未清理")
	}
}
