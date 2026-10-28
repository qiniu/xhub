package regression

import (
	"net/http"
	"testing"
)

// TestTagCatalogReadAfterWrite 通过真实HTTP/私有PostgreSQL复现标签写入后列表不可见，验证更新、删除和权限。
// 参数 t 为上下文；前置本地上游和普通用户，列表必须读同一tag命名空间，标签预算字段不改变推理准入；资源与schema均清理。
func TestTagCatalogReadAfterWrite(t *testing.T) {
	h := newHarness(t, chatDeployment("tag-model"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "tag-catalog")
	const name = "tag-catalog-中文"
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.session, 403}} {
		for _, req := range []struct{ method, path string }{{"GET", "/tag/list"}, {"POST", "/tag/new"}, {"POST", "/tag/update"}, {"POST", "/tag/delete"}} {
			if got := h.do(req.method, req.path, tc.token, map[string]any{"name": name}); got.status != tc.status {
				t.Fatalf("标签管理权限变化: %s %s %s", req.method, req.path, got.describe())
			}
		}
	}
	created := h.ok("POST", "/tag/new", admin, map[string]any{"name": name, "description": "原始说明", "models": []string{}, "max_budget": 0, "rpm_limit": 0})
	if created.json()["id"] != name {
		t.Fatalf("标签标识变化: %s", created.describe())
	}
	t.Cleanup(func() { h.ok("POST", "/tag/delete", admin, map[string]any{"name": name}) })
	list := h.ok("GET", "/tag/list?start_date=2000-01-01&end_date=2000-01-02", admin, nil)
	tag, ok := list.json()[name].(map[string]any)
	if !ok || tag["name"] != name || tag["description"] != "原始说明" || tag["max_budget"] != float64(0) {
		t.Fatalf("已持久化标签未被列表正确读取: %s", list.describe())
	}
	h.ok("POST", "/tag/update", admin, map[string]any{"name": name, "description": "Updated description"})
	updated := h.ok("GET", "/tag/list", admin, nil).json()[name].(map[string]any)
	if updated["description"] != "Updated description" {
		t.Fatalf("列表未反映更新: %v", updated)
	}
	result := h.ok(http.MethodPost, "/v1/chat/completions", owner.key, map[string]any{"model": "tag-model", "messages": []map[string]any{{"role": "user", "content": "Verify tag metadata does not change admission"}}, "metadata": map[string]any{"tags": []string{name}}})
	if result.json()["choices"] == nil || len(h.upstreamCalls()) != 1 {
		t.Fatalf("标签配置改变了数据面准入: %s", result.describe())
	}
	blockedOwner := h.openScope(t, admin, "tag-zero-budget")
	h.setUserBudget(t, admin, blockedOwner.userID, 0)
	blocked := h.do("POST", "/v1/chat/completions", blockedOwner.key, map[string]any{"model": "tag-model", "messages": []map[string]any{{"role": "user", "content": "Verify account budget still blocks"}}, "metadata": map[string]any{"tags": []string{name}}})
	if blocked.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatalf("标签绕过了账号预算: %s", blocked.describe())
	}

	deleted := h.ok("POST", "/tag/delete", admin, map[string]any{"name": name})
	if deleted.json()["deleted"] != float64(1) {
		t.Fatalf("清理计数错误: %s", deleted.describe())
	}
	if got := h.ok("GET", "/tag/list", admin, nil); len(got.json()) != 0 {
		t.Fatalf("删除后列表有残留: %s", got.describe())
	}
	if got := h.do("POST", "/tag/info", admin, map[string]any{"name": name}); got.status != 404 {
		t.Fatalf("删除后详情仍在: %s", got.describe())
	}
}

// TestTagCatalogReadFailure 用真实SQL视图仅破坏标签正文读取，验证500不伪装空列表且恢复后仍保留原配置。
// 参数 t 为上下文；前置私有schema，鉴权数据可读；恢复视图/表与标签清理失败都阻止通过。
func TestTagCatalogReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	h.ok("POST", "/tag/new", admin, map[string]any{"name": "tag-read-failure", "description": "original"})
	catalogSQL(t, h, `CREATE FUNCTION tag_read_failure() RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-tag-read-error'; END $$`)
	catalogSQL(t, h, `ALTER TABLE kv RENAME TO tag_offline_kv`)
	restored := false
	t.Cleanup(func() {
		if !restored {
			catalogSQL(t, h, `DROP VIEW IF EXISTS kv`)
			catalogSQL(t, h, `ALTER TABLE tag_offline_kv RENAME TO kv`)
		}
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS tag_read_failure()`)
		h.ok("POST", "/tag/delete", admin, map[string]any{"name": "tag-read-failure"})
	})
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind,id,CASE WHEN kind='tag' THEN tag_read_failure() ELSE body END AS body,created_at FROM tag_offline_kv`)
	assertCatalogStorageFailure(t, h.do("GET", "/tag/list", admin, nil))
	catalogSQL(t, h, `DROP VIEW kv`)
	catalogSQL(t, h, `ALTER TABLE tag_offline_kv RENAME TO kv`)
	restored = true
	got := h.ok("GET", "/tag/list", admin, nil).json()["tag-read-failure"]
	if row, ok := got.(map[string]any); !ok || row["description"] != "original" {
		t.Fatalf("读取故障污染原配置: %v", got)
	}
}
