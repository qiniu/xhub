package regression

import (
	"net/http"
	"testing"
)

// TestPromptConfigServiceLifecycle 验证真实管理接口保存后模板更新立即进入模型调用、日志和计费。
// 前置私有PostgreSQL、本地模型上游和独立账号，覆盖CRUD权限、详情别名、浅层补丁、upsert、失败和清理；不依赖生产。
func TestPromptConfigServiceLifecycle(t *testing.T) {
	h := newHarness(t, chatDeployment("prompt-service"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "prompt-service")
	const id = "service-template"
	const path = "/prompts/" + id
	for _, op := range []struct{ method, path string }{{"GET", "/prompts/list"}, {"GET", path}, {"POST", "/prompts"}, {"PATCH", path}, {"PUT", path}, {"DELETE", path}} {
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {owner.session, 403}} {
			got := h.do(op.method, op.path, actor.token, map[string]any{"prompt_id": id})
			if got.status != actor.status {
				t.Fatalf("提示词管理权限改变 %s %s: %s", op.method, op.path, got.describe())
			}
		}
	}
	created := h.ok("POST", "/prompts", admin, map[string]any{"prompt_id": id, "name": "Template", "model": "prompt-service", "version": 0, "zero": 0, "enabled": false, "note": nil, "password": "ignored", "prompt": map[string]any{"content": "First {{name}}", "extension": "kept"}})
	obj := created.json()
	if obj["content"] != "First {{name}}" || obj["extension"] != "kept" || obj["version"] != float64(0) || obj["zero"] != float64(0) || obj["enabled"] != false || obj["password"] != nil {
		t.Fatal("创建兼容字段改变", created.describe())
	}
	// 每次按存储ID执行；核对出站原文、真实用量和费用，不以CRUD 200代替功能可用。
	infer := func(want string) {
		t.Helper()
		before := len(h.upstreamCalls())
		got := h.ok("POST", "/prompts/test", admin, map[string]any{"prompt_id": id, "prompt_variables": map[string]any{"name": "Original 用户"}})
		calls := h.upstreamCalls()
		if len(calls) != before+1 || calls[before].Body["messages"].([]any)[0].(map[string]any)["content"] != want {
			t.Fatalf("模板未进入真实调用: %#v", calls)
		}
		h.flushSpend()
		row := logDetail(t, h, admin, got.header("x-litellm-call-id"))
		cost, _ := floatField(row, "spend")
		wantCost := float64(defaultReply.PromptTokens)*testInputRate + float64(defaultReply.CompletionTokens)*testOutputRate
		if firstString(row, "model") != "prompt-service" || !nearlyEqual(cost, wantCost) {
			t.Fatal("调用日志或费用不符", row)
		}
	}
	// version=0 可作为旧管理元数据存储，但既有执行器拒绝非1版本；验证拒绝后恢复默认，不改变该限制。
	rejectedVersion := h.do("POST", "/prompts/test", admin, map[string]any{"prompt_id": id})
	if rejectedVersion.status != 400 || len(h.upstreamCalls()) != 0 {
		t.Fatal("非默认版本限制改变或误发上游", rejectedVersion.describe())
	}
	h.ok("PATCH", path, admin, map[string]any{"version": nil})
	infer("First Original 用户")
	h.ok("PATCH", path, admin, map[string]any{"content": "Updated {{name}}", "version": nil, "note": nil})
	read := h.ok("GET", path+"/info", admin, nil)
	if read.json()["content"] != "Updated {{name}}" || read.json()["version"] != float64(1) || read.json()["note"] != nil {
		t.Fatal("更新或旧详情别名改变", read.describe())
	}
	infer("Updated Original 用户")
	for _, raw := range []string{`{"name":`, `{"jsonrpc":"2.0","method":"tools/list"}`} {
		rejected := catalogRawRequest(t, h, "PATCH", path, admin, raw)
		if rejected.status != 400 {
			t.Fatalf("非法正文应拒绝: %s", rejected.describe())
		}
	}
	infer("Updated Original 用户")
	h.ok("DELETE", path, admin, nil)
	if got := h.do("GET", path, admin, nil); got.status != 404 {
		t.Fatal("删除后仍存在", got.describe())
	}
	upsert := h.ok("PUT", path, admin, map[string]any{"model": "prompt-service", "content": "Upsert {{name}}"})
	if upsert.json()["created_at"] == nil || upsert.json()["prompt_id"] != id {
		t.Fatal("缺失更新的默认值丢失")
	}
	infer("Upsert Original 用户")
	h.ok("POST", "/prompts", admin, map[string]any{"prompt_id": id, "name": "replacement"})
	if got := h.ok("GET", path, admin, nil).json(); got["content"] != nil || got["name"] != "replacement" {
		t.Fatal("同名创建未替换")
	}
	h.ok("DELETE", path, admin, nil)
	if deleted := h.ok("DELETE", path, admin, nil).json()["deleted"]; deleted != float64(0) {
		t.Fatal("删除幂等语义改变")
	}
	if rows := rowsOf(h.ok("GET", "/prompts/list", admin, nil), "prompts"); len(rows) != 0 {
		t.Fatal("未清理模板")
	}
}

// TestPromptConfigServiceDatabaseWriteFailure 使用私有schema的真实PostgreSQL约束与触发器注入写失败。
// 参数 t 为测试上下文；验证创建/更新/删除返回500且没有伪造保存，并在故障撤销后成功；清理SQL失败同样使测试失败，harness最终清理schema。
func TestPromptConfigServiceDatabaseWriteFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/prompts/integrity-tool"
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT integrity_write_failure CHECK (kind <> 'prompts' OR body NOT LIKE '%reject-write%')`)
	t.Cleanup(func() { catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT IF EXISTS integrity_write_failure`) })
	create := h.do(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "integrity-tool", "note": "reject-write"})
	assertCatalogStorageFailure(t, create)
	if list := rowsOf(h.ok(http.MethodGet, "/prompts/list", admin, nil), "prompts"); len(list) != 0 {
		t.Fatalf("失败创建仍有记录: %v", list)
	}
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "integrity-tool", "note": "original"})
	assertCatalogStorageFailure(t, h.do(http.MethodPut, path, admin, map[string]any{"note": "reject-write"}))
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("失败更新污染原值: %v", got)
	}
	catalogSQL(t, h, `CREATE FUNCTION integrity_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'prompts' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`)
	catalogSQL(t, h, `CREATE TRIGGER integrity_delete_failure BEFORE DELETE ON kv FOR EACH ROW EXECUTE FUNCTION integrity_reject_delete()`)
	t.Cleanup(func() {
		catalogSQL(t, h, `DROP TRIGGER IF EXISTS integrity_delete_failure ON kv`)
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS integrity_reject_delete()`)
	})
	assertCatalogStorageFailure(t, h.do(http.MethodDelete, path, admin, nil))
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("失败删除却丢失记录: %v", got)
	}
	catalogSQL(t, h, `DROP TRIGGER integrity_delete_failure ON kv`)
	h.ok(http.MethodPut, path, admin, map[string]any{"note": "recovered"})
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "recovered" {
		t.Fatalf("恢复后更新未保存: %v", got)
	}
	h.ok(http.MethodDelete, path, admin, nil)
	if list := rowsOf(h.ok(http.MethodGet, "/prompts/list", admin, nil), "prompts"); len(list) != 0 {
		t.Fatalf("清理后仍有记录: %v", list)
	}
}

// TestPromptConfigServiceDatabaseReadFailure 用私有schema视图仅对prompts正文注入真实SQL读取失败，保留会话鉴权可读。
// 参数 t 为测试上下文；验证列表、详情和更新均不能伪装成功；恢复表后用HTTP读回原值，清理错误阻止通过。
func TestPromptConfigServiceDatabaseReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/prompts/read-failure"
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "read-failure", "note": "original"})
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
	// CASE只在请求真正读取prompts行时失败，登录会话和其他kv种类仍使用原始正文。
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'prompts' THEN integrity_read_failure() ELSE body END AS body, created_at FROM integrity_offline_kv`)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/prompts/list", nil}, {http.MethodGet, path, nil},
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
