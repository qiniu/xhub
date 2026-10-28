package regression

import (
	"net/http"
	"testing"
)

// TestBudgetConfigServiceDatabaseWriteFailure 使用私有schema的真实PostgreSQL约束与触发器注入写失败。
// 参数 t 为测试上下文；验证创建/更新/删除返回500且没有伪造保存，并在故障撤销后成功；清理SQL失败同样使测试失败，harness最终清理schema。
func TestBudgetConfigServiceDatabaseWriteFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	body := map[string]any{"budget_id": "integrity-tool"}
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT integrity_write_failure CHECK (kind <> 'budget' OR body NOT LIKE '%reject-write%')`)
	t.Cleanup(func() { catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT IF EXISTS integrity_write_failure`) })
	create := h.do(http.MethodPost, "/budget/new", admin, map[string]any{"budget_id": "integrity-tool", "note": "reject-write"})
	assertCatalogStorageFailure(t, create)
	if list := rowsOf(h.ok(http.MethodGet, "/budget/list", admin, nil), "data"); len(list) != 0 {
		t.Fatalf("失败创建仍有记录: %v", list)
	}
	h.ok(http.MethodPost, "/budget/new", admin, map[string]any{"budget_id": "integrity-tool", "note": "original"})
	assertCatalogStorageFailure(t, h.do(http.MethodPost, "/budget/update", admin, map[string]any{"budget_id": "integrity-tool", "note": "reject-write"}))
	if got := h.ok(http.MethodPost, "/budget/info", admin, body).json()["note"]; got != "original" {
		t.Fatalf("失败更新污染原值: %v", got)
	}
	catalogSQL(t, h, `CREATE FUNCTION integrity_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'budget' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`)
	catalogSQL(t, h, `CREATE TRIGGER integrity_delete_failure BEFORE DELETE ON kv FOR EACH ROW EXECUTE FUNCTION integrity_reject_delete()`)
	t.Cleanup(func() {
		catalogSQL(t, h, `DROP TRIGGER IF EXISTS integrity_delete_failure ON kv`)
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS integrity_reject_delete()`)
	})
	assertCatalogStorageFailure(t, h.do(http.MethodPost, "/budget/delete", admin, body))
	if got := h.ok(http.MethodPost, "/budget/info", admin, body).json()["note"]; got != "original" {
		t.Fatalf("失败删除却丢失记录: %v", got)
	}
	catalogSQL(t, h, `DROP TRIGGER integrity_delete_failure ON kv`)
	h.ok(http.MethodPost, "/budget/update", admin, map[string]any{"budget_id": "integrity-tool", "note": "recovered"})
	if got := h.ok(http.MethodPost, "/budget/info", admin, body).json()["note"]; got != "recovered" {
		t.Fatalf("恢复后更新未保存: %v", got)
	}
	h.ok(http.MethodPost, "/budget/delete", admin, body)
	if list := rowsOf(h.ok(http.MethodGet, "/budget/list", admin, nil), "data"); len(list) != 0 {
		t.Fatalf("清理后仍有记录: %v", list)
	}
}

// TestBudgetConfigServiceDatabaseReadFailure 用私有schema视图仅对budget正文注入真实SQL读取失败，保留会话鉴权可读。
// 参数 t 为测试上下文；验证列表、详情和更新均不能伪装成功；恢复表后用HTTP读回原值，清理错误阻止通过。
func TestBudgetConfigServiceDatabaseReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	body := map[string]any{"budget_id": "read-failure"}
	h.ok(http.MethodPost, "/budget/new", admin, map[string]any{"budget_id": "read-failure", "note": "original"})
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
	// CASE只在请求真正读取budget行时失败，登录会话和其他kv种类仍使用原始正文。
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'budget' THEN integrity_read_failure() ELSE body END AS body, created_at FROM integrity_offline_kv`)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/budget/list", nil}, {http.MethodPost, "/budget/info", body},
		{http.MethodPost, "/budget/update", map[string]any{"budget_id": "read-failure", "note": "changed"}},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) { assertCatalogStorageFailure(t, h.do(tc.method, tc.path, admin, tc.body)) })
	}
	catalogSQL(t, h, `DROP VIEW kv`)
	catalogSQL(t, h, `ALTER TABLE integrity_offline_kv RENAME TO kv`)
	restored = true
	if got := h.ok(http.MethodPost, "/budget/info", admin, body).json()["note"]; got != "original" {
		t.Fatalf("读失败修改了记录: %v", got)
	}
	h.ok(http.MethodPost, "/budget/delete", admin, body)
}
