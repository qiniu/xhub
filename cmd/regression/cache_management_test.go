package regression

import (
	"strings"
	"testing"
)

// TestCacheManagementTruth 使用真实HTTP/PostgreSQL检查缓存管理承诺与数据面一致。
// 前置隔离租户、本地模型及历史无效配置；验证身份拒绝、不支持操作无写入、缓存命中/清理及费用。
// 所有用户、日志和历史配置由harness私有schema严格回收，不访问外部Redis或供应商。
func TestCacheManagementTruth(t *testing.T) {
	h := newHarness(t, chatDeployment("cache-management"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "cache-management")
	legacy := `{"type":"redis","enabled":false,"password":"legacy-cache-secret"}`
	if err := h.store.PutKV("cache", "settings", legacy); err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ method, path string }{
		{"GET", "/cache/settings"}, {"GET", "/cache/ping"}, {"GET", "/ping"}, {"POST", "/flushall"},
		{"POST", "/cache/settings"}, {"POST", "/cache/settings/test"}, {"POST", "/delete"},
	} {
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {owner.session, 403}} {
			if got := h.do(op.method, op.path, actor.token, map[string]any{}); got.status != actor.status {
				t.Errorf("缓存管理权限改变 %s %s: %s", op.method, op.path, got.describe())
			}
		}
	}
	settings := h.ok("GET", "/cache/settings", admin, nil)
	if body := settings.json(); body["enabled"] != true || body["cache_type"] != "memory" || body["scope"] != "process" || body["configurable"] != false || strings.Contains(settings.describe(), "legacy-cache-secret") {
		t.Error("配置查询必须返回实际进程缓存，不回显未生效配置或凭据", settings.describe())
	}
	for _, path := range []string{"/cache/settings", "/cache/settings/test", "/delete"} {
		got := h.do("POST", path, admin, map[string]any{"cache_settings": map[string]any{"type": "redis", "password": "incoming-cache-secret"}, "keys": []string{"unused"}})
		e, _ := got.json()["error"].(map[string]any)
		if got.status != 501 || e["type"] != "unsupported_operation" || strings.Contains(got.describe(), "incoming-cache-secret") {
			t.Errorf("未执行缓存操作却未明确拒绝 %s: %s", path, got.describe())
		}
	}
	rows, err := h.store.ListKV("cache")
	if err != nil || len(rows) != 1 || rows[0]["password"] != "legacy-cache-secret" {
		t.Error("不支持的操作改动历史配置", rows, err)
	}
	for _, path := range []string{"/cache/ping", "/ping"} {
		if got := h.ok("GET", path, admin, nil).json(); got["healthy"] != true || got["cache_type"] != "memory" {
			t.Error("进程缓存探测契约改变", got)
		}
	}
	request := chatRequest("cache-management", "Verify local response cache management.")
	h.ok("POST", "/v1/chat/completions", owner.key, request)
	if hit := h.ok("POST", "/v1/chat/completions", owner.key, request); !isCacheHit(hit) || parseFloatOrZero(hit.header("x-litellm-response-cost")) != 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("配置拒绝改变命中或费用", hit.describe())
	}
	h.ok("POST", "/flushall", admin, nil)
	if miss := h.ok("POST", "/v1/chat/completions", owner.key, request); isCacheHit(miss) || len(h.upstreamCalls()) != 2 || parseFloatOrZero(miss.header("x-litellm-response-cost")) <= 0 {
		t.Fatal("清理未实际生效或未恢复费用", miss.describe())
	}
	blocked := h.openScope(t, admin, "cache-management-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if got := h.do("POST", "/v1/chat/completions", blocked.key, request); got.status != 403 || len(h.upstreamCalls()) != 2 {
		t.Fatal("零预算不得通过缓存绕过", got.describe())
	}
}

// TestCacheUnavailableManagement 在真实已登录HTTP边界替换本测试缓存为nil，验证不会假健康或panic。
// 独占harness，结束前恢复缓存；用户及schema由harness清理，不影响其他进程。
func TestCacheUnavailableManagement(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	original := h.gw.Cache
	h.gw.Cache = nil
	defer func() { h.gw.Cache = original }()
	for _, op := range []struct{ method, path string }{{"GET", "/cache/settings"}, {"GET", "/cache/ping"}, {"GET", "/ping"}, {"POST", "/flushall"}} {
		got := h.do(op.method, op.path, admin, nil)
		e, _ := got.json()["error"].(map[string]any)
		if got.status != 503 || e["type"] != "cache_unavailable" {
			t.Error("未初始化缓存未明确失败", op, got.describe())
		}
	}
}
