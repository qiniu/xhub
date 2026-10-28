package regression

import (
	"net/http"
	"reflect"
	"testing"
)

// TestPluginFilterLifecycle 通过真实HTTP/PostgreSQL核对enabled_only，不把筛选配置当作插件加载。
// 前置隔离schema和本地上游；验证布尔值、旧缺省、权限、错误参数、无写入及推理预算；配置显式删除，harness回收账号/schema。
func TestPluginFilterLifecycle(t *testing.T) {
	h := newHarness(t, chatDeployment("plugin-filter"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "plugin-filter")
	for id, body := range map[string]map[string]any{
		"on": {"enabled": true}, "off": {"enabled": false}, "legacy": {}, "null": {"enabled": nil}, "invalid": {"enabled": "true"},
	} {
		body["name"] = id
		h.ok("PUT", "/claude-code/plugins/"+id, admin, body)
	}
	before, err := h.store.ListKV("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		count int
	}{{"", 5}, {"?enabled_only=false", 5}, {"?enabled_only=true", 3}} {
		t.Run(tc.query, func(t *testing.T) {
			out := h.ok("GET", "/claude-code/plugins"+tc.query, admin, nil)
			rows := rowsOf(out, "plugins")
			if len(rows) != tc.count || out.json()["count"] != float64(tc.count) {
				t.Fatalf("插件筛选数量错误: %s", out.describe())
			}
			if tc.count == 3 {
				for _, r := range rows {
					if r["enabled"] != true {
						t.Errorf("已启用列表包含停用或无效状态: %v", r)
					}
				}
			}
		})
	}
	for _, q := range []string{"?enabled_only=true;bad", "?enabled_only=%zz", "?enabled_only=unknown", "?enabled_only=", "?enabled_only=true&enabled_only=false"} {
		if out := h.do("GET", "/claude-code/plugins"+q, admin, nil); out.status != 400 {
			t.Errorf("无效筛选应拒绝: %s", out.describe())
		}
	}
	for _, actor := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.session, 403}} {
		if out := h.do("GET", "/claude-code/plugins?enabled_only=true", actor.token, nil); out.status != actor.status {
			t.Fatal("筛选不能放宽管理权限", out.describe())
		}
	}
	after, err := h.store.ListKV("claude-code")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("列表读取修改了原配置", err)
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("plugin-filter", "Verify plugin filtering leaves inference available."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("正常推理或费用发生变化")
	}
	blocked := h.openScope(t, admin, "plugin-filter-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("plugin-filter", "Verify zero budget remains blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算规则发生变化", out.describe())
	}
	for _, id := range []string{"on", "off", "legacy", "null", "invalid"} {
		h.ok("DELETE", "/claude-code/plugins/"+id, admin, nil)
	}
	if out := h.ok("GET", "/claude-code/plugins?enabled_only=true", admin, nil); len(rowsOf(out, "plugins")) != 0 || out.json()["count"] != float64(0) {
		t.Fatal("插件未清理", out.describe())
	}
}

// TestPluginFilterDatabaseReadFailure 用私有schema视图仅对claude-code正文注入真实SQL读取失败，保留会话鉴权可读。
// 参数 t 为测试上下文；验证列表、详情和更新均不能伪装成功；恢复表后用HTTP读回原值，清理错误阻止通过。
func TestPluginFilterDatabaseReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/claude-code/plugins/read-failure"
	h.ok(http.MethodPut, path, admin, map[string]any{"enabled": true, "note": "original"})
	catalogSQL(t, h, `CREATE FUNCTION integrity_read_failure() RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-read-error'; END $$`)
	catalogSQL(t, h, `ALTER TABLE kv RENAME TO integrity_offline_kv`)
	restored := false
	t.Cleanup(func() {
		if !restored {
			catalogSQL(t, h, `DROP VIEW IF EXISTS kv`)
			catalogSQL(t, h, `ALTER TABLE integrity_offline_kv RENAME TO kv`)
		}
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS integrity_read_failure()`)
	})
	// CASE只在请求真正读取claude-code行时失败，登录会话和其他kv种类仍使用原始正文。
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'claude-code' THEN integrity_read_failure() ELSE body END AS body, created_at FROM integrity_offline_kv`)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/claude-code/plugins?enabled_only=true", nil}, {http.MethodGet, path, nil},
		{http.MethodPut, path, map[string]any{"note": "changed"}},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) { assertCatalogStorageFailure(t, h.do(tc.method, tc.path, admin, tc.body)) })
	}
	catalogSQL(t, h, `DROP VIEW kv`)
	catalogSQL(t, h, `ALTER TABLE integrity_offline_kv RENAME TO kv`)
	restored = true
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("读失败修改了记录: %v", got)
	}
	h.ok(http.MethodDelete, path, admin, nil)
}
