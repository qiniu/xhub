package regression

import (
	"net/http"
	"testing"
)

// TestPolicyConfigServiceDatabaseWriteFailure 使用私有schema的真实PostgreSQL约束与触发器注入写失败。
// 参数 t 为测试上下文；验证创建/更新/删除返回500且没有伪造保存，并在故障撤销后成功；清理SQL失败同样使测试失败，harness最终清理schema。
func TestPolicyConfigServiceDatabaseWriteFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/policies/integrity-tool"
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT integrity_write_failure CHECK (kind <> 'policies' OR body NOT LIKE '%reject-write%')`)
	t.Cleanup(func() { catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT IF EXISTS integrity_write_failure`) })
	create := h.do(http.MethodPost, "/policies", admin, map[string]any{"policy_id": "integrity-tool", "note": "reject-write"})
	assertCatalogStorageFailure(t, create)
	if list := rowsOf(h.ok(http.MethodGet, "/policies/list", admin, nil), "policies"); len(list) != 0 {
		t.Fatalf("失败创建仍有记录: %v", list)
	}
	h.ok(http.MethodPost, "/policies", admin, map[string]any{"policy_id": "integrity-tool", "note": "original"})
	assertCatalogStorageFailure(t, h.do(http.MethodPut, path, admin, map[string]any{"note": "reject-write"}))
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("失败更新污染原值: %v", got)
	}
	catalogSQL(t, h, `CREATE FUNCTION integrity_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'policies' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`)
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
	if list := rowsOf(h.ok(http.MethodGet, "/policies/list", admin, nil), "policies"); len(list) != 0 {
		t.Fatalf("清理后仍有记录: %v", list)
	}
}

// TestPolicyConfigServiceDatabaseReadFailure 用私有schema视图仅对policies正文注入真实SQL读取失败，保留会话鉴权可读。
// 参数 t 为测试上下文；验证列表、详情和更新均不能伪装成功；恢复表后用HTTP读回原值，清理错误阻止通过。
func TestPolicyConfigServiceDatabaseReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/policies/read-failure"
	h.ok(http.MethodPost, "/policies", admin, map[string]any{"policy_id": "read-failure", "note": "original"})
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
	// CASE只在请求真正读取policies行时失败，登录会话和其他kv种类仍使用原始正文。
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'policies' THEN integrity_read_failure() ELSE body END AS body, created_at FROM integrity_offline_kv`)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/policies/list", nil}, {http.MethodGet, path, nil},
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

// TestPolicyConfigServiceLifecycle 通过真实HTTP/PostgreSQL验证策略配置CRUD、旧字段和权限，不把配置存储当作策略执行。
// 前置私有schema、独立用户和本地上游；配置更新前后实际对话及费用保持不变，失败输入不覆盖，最后显式删配置并由harness清理账号/schema。
func TestPolicyConfigServiceLifecycle(t *testing.T) {
	h := newHarness(t, chatDeployment("policy-service"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "policy-service")
	const id = "service-policy"
	const path = "/policies/" + id
	for _, op := range []struct{ method, path string }{{"GET", "/policies/list"}, {"GET", path}, {"POST", "/policies"}, {"PUT", path}, {"DELETE", path}} {
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {owner.session, 403}} {
			got := h.do(op.method, op.path, actor.token, map[string]any{"policy_id": id})
			if got.status != actor.status {
				t.Fatalf("策略管理权限改变 %s %s: %s", op.method, op.path, got.describe())
			}
		}
	}
	// infer 对普通用户发起实际对话，逐次核对上游请求及真实计费；不是策略执行能力测试。
	infer := func(stage string) {
		t.Helper()
		before := len(h.upstreamCalls())
		got := h.ok("POST", "/v1/chat/completions", owner.key, map[string]any{"model": "policy-service", "messages": []any{map[string]any{"role": "user", "content": "Reply with POLICY_OK for " + stage + "."}}})
		if len(h.upstreamCalls()) != before+1 {
			t.Fatal("管理配置影响普通用户上游调用")
		}
		h.flushSpend()
		row := logDetail(t, h, admin, got.header("x-litellm-call-id"))
		cost, _ := floatField(row, "spend")
		want := float64(defaultReply.PromptTokens)*testInputRate + float64(defaultReply.CompletionTokens)*testOutputRate
		if !nearlyEqual(cost, want) {
			t.Fatal("计费规则改变", row)
		}
	}
	infer("before")
	created := h.ok("POST", "/policies", admin, map[string]any{"policy_id": id, "name": "Display", "version": 0, "enabled": false, "password": "ignored", "policy": map[string]any{"policy_name": "Original 用户", "extension": "kept"}}).json()
	if created["policy_id"] != id || created["policie_alias"] != "Display" || created["version"] != float64(0) || created["status"] != "active" || created["enabled"] != false || created["extension"] != "kept" || created["password"] != nil {
		t.Fatal("创建兼容字段改变", created)
	}
	h.ok("PUT", path, admin, map[string]any{"policy_id": "wrong", "policy_name": "Updated 用户", "version": nil, "status": "inactive", "note": nil})
	read := h.ok("GET", path, admin, nil).json()
	if read["policy_id"] != id || read["policy_name"] != "Updated 用户" || read["version"] != float64(1) || read["status"] != "inactive" || read["note"] != nil {
		t.Fatal("更新兼容改变", read)
	}
	for _, raw := range []string{`{"name":`, `[]`, `{"jsonrpc":"2.0"}`, `{} {}`} {
		if got := catalogRawRequest(t, h, "PUT", path, admin, raw); got.status != 400 {
			t.Fatal("非法输入应拒绝", got.describe())
		}
	}
	if read := h.ok("GET", path, admin, nil).json(); read["policy_name"] != "Updated 用户" {
		t.Fatal("失败写覆盖了数据", read)
	}
	infer("updated")
	h.ok("DELETE", path, admin, nil)
	if got := h.do("GET", path, admin, nil); got.status != 404 {
		t.Fatal("删除后仍存在", got.describe())
	}
	upsert := h.ok("PUT", path, admin, map[string]any{"policy_name": "Upsert"}).json()
	if upsert["policy_id"] != id || upsert["created_at"] == nil || upsert["status"] != "active" {
		t.Fatal("upsert默认改变", upsert)
	}
	h.ok("POST", "/policies", admin, map[string]any{"policy_id": id, "policy_name": "Replacement"})
	if read := h.ok("GET", path, admin, nil).json(); read["note"] != nil || read["policy_name"] != "Replacement" {
		t.Fatal("创建未覆盖", read)
	}
	h.ok("DELETE", path, admin, nil)
	if got := h.ok("DELETE", path, admin, nil).json()["deleted"]; got != float64(0) {
		t.Fatal("缺失删除计数改变", got)
	}
	if list := rowsOf(h.ok("GET", "/policies/list", admin, nil), "policies"); len(list) != 0 {
		t.Fatal("配置未清理", list)
	}
	infer("deleted")
	// 预算耗尽仍由既有数据面拒绝，不因管理服务拆分而放行或发送上游。
	blockedOwner := h.openScope(t, admin, "policy-zero-budget")
	h.setUserBudget(t, admin, blockedOwner.userID, 0)
	before := len(h.upstreamCalls())
	denied := h.do("POST", "/v1/chat/completions", blockedOwner.key, map[string]any{"model": "policy-service", "messages": []any{map[string]any{"role": "user", "content": "Reply with BUDGET_CHECK."}}})
	if denied.status != 403 || len(h.upstreamCalls()) != before {
		t.Fatal("零预算拒绝规则改变", denied.describe())
	}
}
