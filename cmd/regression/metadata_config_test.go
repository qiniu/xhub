package regression

import (
	"fmt"
	"testing"
)

// TestMetadataConfigLifecycle 固定标签与终端客户旧HTTP契约，迁移前后运行同一测试。
// t为上下文；独立schema、本地上游和测试用户；覆盖字段、权限、更新/替换、隔离与清理，不把配置当授权能力。
func TestMetadataConfigLifecycle(t *testing.T) {
	h := newHarness(t, chatDeployment("metadata-model"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "metadata-owner")
	for _, kind := range []string{"tag", "customer", "end_user"} {
		t.Run(kind, func(t *testing.T) {
			field := "user_id"
			infoMethod := "GET"
			if kind == "tag" {
				field = "name"
				infoMethod = "POST"
			}
			id := "metadata-shared-id"
			body := map[string]any{field: id}
			info := "/" + kind + "/info"
			if infoMethod == "GET" {
				info += "?user_id=" + id
			}
			for _, op := range []struct{ method, path string }{{"POST", "/" + kind + "/new"}, {"POST", "/" + kind + "/update"}, {"POST", "/" + kind + "/delete"}, {"GET", "/" + kind + "/list"}, {infoMethod, info}} {
				for _, actor := range []struct {
					token  string
					status int
				}{{"", 401}, {owner.session, 403}} {
					if got := h.do(op.method, op.path, actor.token, body); got.status != actor.status {
						t.Fatalf("%s管理权限改变: %s", kind, got.describe())
					}
				}
			}
			create := map[string]any{field: id, "description": "原始客户说明", "spend": 0, "max_budget": nil, "blocked": false, "password": "discard", "skill": map[string]any{"extension": "kept"}}
			created := h.ok("POST", "/"+kind+"/new", admin, create).json()
			if created[field] != id || created["extension"] != "kept" || created["password"] != nil || created["spend"] != float64(0) {
				t.Fatal("创建字段变化", created)
			}
			h.ok("POST", "/"+kind+"/update", admin, map[string]any{field: id, "description": "更新原文", "max_budget": 0, "blocked": true, "extra": nil})
			read := h.ok(infoMethod, info, admin, body).json()
			if read["description"] != "更新原文" || read["max_budget"] != float64(0) || read["blocked"] != true {
				t.Fatal("更新未持久化", read)
			}
			list := h.ok("GET", "/"+kind+"/list", admin, nil)
			if kind == "tag" {
				if list.json()[id] == nil {
					t.Fatal("标签字典丢失", list.describe())
				}
			} else if len(rowsOf(list, "data")) != 1 {
				t.Fatal("客户包络变化", list.describe())
			}
			for _, raw := range []string{`[]`, `{"name":`, `{"jsonrpc":"2.0"}`, `{} {}`} {
				if got := catalogRawRequest(t, h, "POST", "/"+kind+"/update", admin, raw); got.status != 400 {
					t.Fatal("非法请求未拒绝", got.describe())
				}
			}
			if h.ok(infoMethod, info, admin, body).json()["description"] != "更新原文" {
				t.Fatal("非法更新污染记录")
			}
			// 元数据里的blocked/max_budget不是已有账户准入规则；真实调用仍由账号授权及预算管理。
			result := h.ok("POST", "/v1/chat/completions", owner.key, map[string]any{"model": "metadata-model", "user": id, "messages": []map[string]any{{"role": "user", "content": "Reply with a short greeting for " + kind + "."}}, "metadata": map[string]any{"tags": []string{id}}})
			h.flushSpend()
			if cost, ok := floatField(logDetail(t, h, admin, result.header("x-litellm-call-id")), "spend"); !ok || cost <= 0 {
				t.Fatal("元数据变更影响真实计费")
			}
			h.ok("POST", "/"+kind+"/new", admin, map[string]any{field: id, "description": "replacement"})
			if h.ok(infoMethod, info, admin, body).json()["blocked"] == true {
				t.Fatal("同ID创建未替换")
			}
			if h.ok("POST", "/"+kind+"/delete", admin, body).json()["deleted"] != float64(1) {
				t.Fatal("删除未生效")
			}
			if got := h.do(infoMethod, info, admin, body); got.status != 404 {
				t.Fatal("删除后残留", got.describe())
			}
			upsert := h.ok("POST", "/"+kind+"/update", admin, map[string]any{field: id, "description": "upsert"}).json()
			if upsert[field] != id || upsert["created_at"] == nil {
				t.Fatal("upsert变化", upsert)
			}
			h.ok("POST", "/"+kind+"/delete", admin, body)
			if h.ok("POST", "/"+kind+"/delete", admin, body).json()["deleted"] != float64(0) {
				t.Fatal("缺失删除变化")
			}
		})
	}
	// 三种空间可同时保存同一标识，不能静默合并；最终逐项删除并核对404。
	for _, kind := range []string{"customer", "end_user"} {
		h.ok("POST", "/"+kind+"/new", admin, map[string]any{"user_id": "same", "description": kind})
	}
	for _, kind := range []string{"customer", "end_user"} {
		if h.ok("GET", "/"+kind+"/info?user_id=same", admin, map[string]any{"user_id": "same"}).json()["description"] != kind {
			t.Fatal("命名空间串号")
		}
		h.ok("POST", "/"+kind+"/delete", admin, map[string]any{"user_id": "same"})
		if got := h.do("GET", "/"+kind+"/info?user_id=same", admin, map[string]any{"user_id": "same"}); got.status != 404 {
			t.Fatal("清理残留")
		}
	}
	blocked := h.openScope(t, admin, "metadata-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	before := len(h.upstreamCalls())
	denied := h.do("POST", "/v1/chat/completions", blocked.key, map[string]any{"model": "metadata-model", "messages": []map[string]any{{"role": "user", "content": "Verify budget enforcement."}}})
	if denied.status != 403 || len(h.upstreamCalls()) != before {
		t.Fatal("零预算规则改变", denied.describe())
	}
}

// TestMetadataConfigStorageFailures 通过真实PG约束、视图及触发器制造读写删除失败，迁移前后比较。
// t为上下文；每个子例独立schema，失败不得覆盖或返回成功；SQL恢复及schema清理由harness严格检查。
func TestMetadataConfigStorageFailures(t *testing.T) {
	for _, kind := range []string{"tag", "customer", "end_user"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			admin := h.adminSession()
			field := "user_id"
			method := "GET"
			info := "/" + kind + "/info?user_id=stored"
			if kind == "tag" {
				field = "name"
				method = "POST"
				info = "/tag/info"
			}
			body := map[string]any{field: "stored", "note": "original"}
			h.ok("POST", "/"+kind+"/new", admin, body)
			catalogSQL(t, h, fmt.Sprintf(`ALTER TABLE kv ADD CONSTRAINT metadata_failure CHECK (kind <> '%s' OR body NOT LIKE '%%reject-write%%')`, kind))
			assertCatalogStorageFailure(t, h.do("POST", "/"+kind+"/new", admin, map[string]any{field: "new-failure", "note": "reject-write"}))
			assertCatalogStorageFailure(t, h.do("POST", "/"+kind+"/update", admin, map[string]any{field: "stored", "note": "reject-write"}))
			if h.ok(method, info, admin, body).json()["note"] != "original" {
				t.Fatal("写失败污染记录")
			}
			catalogSQL(t, h, `CREATE FUNCTION metadata_read_failure() RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-read-error'; END $$`)
			catalogSQL(t, h, `ALTER TABLE kv RENAME TO metadata_offline_kv`)
			catalogSQL(t, h, fmt.Sprintf(`CREATE VIEW kv AS SELECT kind,id,CASE WHEN kind='%s' THEN metadata_read_failure() ELSE body END AS body,created_at FROM metadata_offline_kv`, kind))
			if kind == "tag" {
				assertCatalogStorageFailure(t, h.do("GET", "/"+kind+"/list", admin, nil))
			}
			assertCatalogStorageFailure(t, h.do(method, info, admin, body))
			assertCatalogStorageFailure(t, h.do("POST", "/"+kind+"/update", admin, body))
			catalogSQL(t, h, `DROP VIEW kv`)
			catalogSQL(t, h, `ALTER TABLE metadata_offline_kv RENAME TO kv`)
			catalogSQL(t, h, `DROP FUNCTION metadata_read_failure()`)
			catalogSQL(t, h, fmt.Sprintf(`CREATE FUNCTION metadata_delete_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind='%s' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`, kind))
			catalogSQL(t, h, `CREATE TRIGGER metadata_delete_failure BEFORE DELETE ON kv FOR EACH ROW EXECUTE FUNCTION metadata_delete_failure()`)
			assertCatalogStorageFailure(t, h.do("POST", "/"+kind+"/delete", admin, body))
			if h.ok(method, info, admin, body).json()["note"] != "original" {
				t.Fatal("失败丢失记录")
			}
			catalogSQL(t, h, `DROP TRIGGER metadata_delete_failure ON kv`)
			catalogSQL(t, h, `DROP FUNCTION metadata_delete_failure()`)
			h.ok("POST", "/"+kind+"/update", admin, map[string]any{field: "stored", "note": "recovered"})
			if h.ok(method, info, admin, body).json()["note"] != "recovered" {
				t.Fatal("恢复后仍不可用")
			}
			h.ok("POST", "/"+kind+"/delete", admin, body)
			if got := h.do(method, info, admin, body); got.status != 404 {
				t.Fatal("清理残留", got.describe())
			}
		})
	}
}

// TestCustomerListReadFailure 先复现任一客户空间SQL读取失败仍返回200的缺陷，再验证500及恢复。
// 每例隔离schema并真实创建两个空间；不接受部分列表，恢复后2行同ID保留，不合并身份；最后清理。
func TestCustomerListReadFailure(t *testing.T) {
	for _, kind := range []string{"customer", "end_user"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			admin := h.adminSession()
			for _, space := range []string{"customer", "end_user"} {
				h.ok("POST", "/"+space+"/new", admin, map[string]any{"user_id": "same"})
			}
			catalogSQL(t, h, `CREATE FUNCTION customer_read_failure() RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-read-error'; END $$`)
			catalogSQL(t, h, `ALTER TABLE kv RENAME TO customer_offline_kv`)
			catalogSQL(t, h, fmt.Sprintf(`CREATE VIEW kv AS SELECT kind,id,CASE WHEN kind='%s' THEN customer_read_failure() ELSE body END AS body,created_at FROM customer_offline_kv`, kind))
			for _, space := range []string{"customer", "end_user"} {
				assertCatalogStorageFailure(t, h.do("GET", "/"+space+"/list", admin, nil))
			}
			catalogSQL(t, h, `DROP VIEW kv`)
			catalogSQL(t, h, `ALTER TABLE customer_offline_kv RENAME TO kv`)
			catalogSQL(t, h, `DROP FUNCTION customer_read_failure()`)
			for _, space := range []string{"customer", "end_user"} {
				if len(rowsOf(h.ok("GET", "/"+space+"/list", admin, nil), "data")) != 2 {
					t.Fatal("恢复后未保留两个空间记录")
				}
			}
			for _, space := range []string{"customer", "end_user"} {
				h.ok("POST", "/"+space+"/delete", admin, map[string]any{"user_id": "same"})
			}
			if len(rowsOf(h.ok("GET", "/customer/list", admin, nil), "data")) != 0 {
				t.Fatal("客户未清理")
			}
		})
	}
}
