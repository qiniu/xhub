package regression

import (
	"net/http"
	"testing"
)

// TestAccessGroupConfigServiceDatabaseWriteFailure 使用私有schema的真实PostgreSQL约束与触发器注入写失败。
// 参数 t 为测试上下文；验证创建/更新/删除返回500且没有伪造保存，并在故障撤销后成功；清理SQL失败同样使测试失败，harness最终清理schema。
func TestAccessGroupConfigServiceDatabaseWriteFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/v1/access_group/integrity-tool"
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT integrity_write_failure CHECK (kind <> 'access_group' OR body NOT LIKE '%reject-write%')`)
	t.Cleanup(func() { catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT IF EXISTS integrity_write_failure`) })
	create := h.do(http.MethodPost, "/v1/access_group", admin, map[string]any{"access_group_id": "integrity-tool", "note": "reject-write"})
	assertCatalogStorageFailure(t, create)
	if list := rowsOf(h.ok(http.MethodGet, "/access_group/list", admin, nil), "access_group"); len(list) != 0 {
		t.Fatalf("失败创建仍有记录: %v", list)
	}
	h.ok(http.MethodPost, "/v1/access_group", admin, map[string]any{"access_group_id": "integrity-tool", "note": "original"})
	assertCatalogStorageFailure(t, h.do(http.MethodPut, path, admin, map[string]any{"note": "reject-write"}))
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("失败更新污染原值: %v", got)
	}
	catalogSQL(t, h, `CREATE FUNCTION integrity_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'access_group' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`)
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
	if list := rowsOf(h.ok(http.MethodGet, "/access_group/list", admin, nil), "access_group"); len(list) != 0 {
		t.Fatalf("清理后仍有记录: %v", list)
	}
}

// TestAccessGroupConfigServiceDatabaseReadFailure 用私有schema视图仅对access_group正文注入真实SQL读取失败，保留会话鉴权可读。
// 参数 t 为测试上下文；验证列表、详情和更新均不能伪装成功；恢复表后用HTTP读回原值，清理错误阻止通过。
func TestAccessGroupConfigServiceDatabaseReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/v1/access_group/read-failure"
	h.ok(http.MethodPost, "/v1/access_group", admin, map[string]any{"access_group_id": "read-failure", "note": "original"})
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
	// CASE只在请求真正读取access_group行时失败，登录会话和其他kv种类仍使用原始正文。
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'access_group' THEN integrity_read_failure() ELSE body END AS body, created_at FROM integrity_offline_kv`)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/access_group/list", nil}, {http.MethodGet, path, nil},
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

// TestAccessGroupConfigServiceLifecycle 验证11个真实旧URL、两个存储命名空间与CRUD兼容。
// 私有schema内以管理员写入、普通用户拒绝；错误不覆盖，删除严格读回，harness回收所有数据。
func TestAccessGroupConfigServiceLifecycle(t *testing.T) {
	h := newHarness(t, chatDeployment("access-allowed"), chatDeployment("access-denied"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "access-config")
	const id = "service-access"
	const path = "/v1/access_group/" + id
	const old = "/access_group/" + id
	ops := []struct{ method, path string }{
		{"GET", "/access_group/list"}, {"POST", "/access_group/new"}, {"DELETE", old + "/delete"}, {"GET", old + "/info"}, {"PUT", old + "/update"},
		{"GET", "/access_groups"}, {"GET", "/v1/access_group"}, {"POST", "/v1/access_group"}, {"DELETE", path}, {"GET", path}, {"PUT", path},
	}
	for _, op := range ops {
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {owner.session, 403}} {
			got := h.do(op.method, op.path, actor.token, map[string]any{"access_group_id": id})
			if got.status != actor.status {
				t.Fatalf("管理权限改变 %s %s: %s", op.method, op.path, got.describe())
			}
		}
	}
	created := h.ok("POST", "/access_group/new", admin, map[string]any{"access_group_id": id, "name": "Display", "password": "ignored", "models": []string{"access-denied"}, "access_model_names": []string{"access-denied"}, "assigned_key_ids": []string{owner.keyID}, "assigned_team_ids": []string{owner.teamID}, "enabled": false, "note": nil}).json()
	if created["access_group_id"] != id || created["access_group_name"] != "Display" || created["enabled"] != false || created["description"] != "" || created["password"] != nil {
		t.Fatal("创建兼容改变", created)
	}
	for _, p := range []string{path, old + "/info"} {
		if got := h.ok("GET", p, admin, nil).json(); got["access_group_id"] != id {
			t.Fatal("详情别名不一致", got)
		}
	}
	for _, p := range []string{"/access_group/list", "/v1/access_group"} {
		list := rowsOf(h.ok("GET", p, admin, nil))
		if len(list) != 1 || list[0]["access_group_id"] != id {
			t.Fatal("列表别名不一致", p, list)
		}
	}
	// 历史复数路由读取独立种类，不能把迁移变成隐式数据合并；直接种子用于验证没有写入口的旧记录。
	catalogSQL(t, h, `INSERT INTO kv(kind,id,body) VALUES ('access_groups','plural-only','{"id":"plural-only","name":"Legacy plural"}')`)
	plural := rowsOf(h.ok("GET", "/access_groups", admin, nil))
	if len(plural) != 1 || plural[0]["id"] != "plural-only" {
		t.Fatal("历史复数命名空间改变", plural)
	}
	h.ok("PUT", old+"/update", admin, map[string]any{"id": "wrong", "access_group_name": "Updated 用户", "description": nil, "models": nil, "budget_id": 0})
	read := h.ok("GET", path, admin, nil).json()
	if read["id"] != id || read["access_group_name"] != "Updated 用户" || read["description"] != "" || read["budget_id"] != float64(0) || read["updated_at"] != created["created_at"] {
		t.Fatal("更新语义改变", read)
	}
	for _, raw := range []string{`{"name":`, `[]`, `{"jsonrpc":"2.0"}`, `{} {}`} {
		if got := catalogRawRequest(t, h, "PUT", path, admin, raw); got.status != 400 {
			t.Fatal("非法输入未拒绝", got.describe())
		}
	}
	if got := h.ok("GET", old+"/info", admin, nil).json(); got["access_group_name"] != "Updated 用户" {
		t.Fatal("失败更新污染", got)
	}
	h.ok("DELETE", old+"/delete", admin, nil)
	if got := h.do("GET", path, admin, nil); got.status != 404 {
		t.Fatal("删除无效", got.describe())
	}
	upsert := h.ok("PUT", path, admin, map[string]any{"access_group_name": "Upsert"}).json()
	if upsert["access_group_id"] != id || upsert["created_at"] == nil {
		t.Fatal("upsert改变", upsert)
	}
	h.ok("POST", "/v1/access_group", admin, map[string]any{"access_group_id": id, "name": "Replacement"})
	if got := h.ok("GET", path, admin, nil).json(); got["access_group_name"] != "Replacement" || got["budget_id"] != nil {
		t.Fatal("创建不是替换", got)
	}
	h.ok("DELETE", path, admin, nil)
	if got := h.ok("DELETE", path, admin, nil).json()["deleted"]; got != float64(0) {
		t.Fatal("缺失删除", got)
	}
	for _, p := range []string{"/access_group/list", "/v1/access_group"} {
		if list := rowsOf(h.ok("GET", p, admin, nil)); len(list) != 0 {
			t.Fatal("配置未清理", list)
		}
	}
	catalogSQL(t, h, `DELETE FROM kv WHERE kind='access_groups' AND id='plural-only'`)
	if list := rowsOf(h.ok("GET", "/access_groups", admin, nil)); len(list) != 0 {
		t.Fatal("复数种子未清理", list)
	}
	for _, p := range []string{old + "/budget", "/v1/unified_access_group"} {
		if got := h.do("GET", p, admin, nil); got.status != 404 {
			t.Fatal("已移除路由意外恢复", got.describe())
		}
	}
}

// TestAccessGroupConfigServiceDataPlaneIsolation 验证配置字段不会成为新的授权或预算通道。
// 独立账号、Key、真实HTTP及本地上游；创建/修改/删除组前后，允许模型真实计费、禁止模型及零预算保持拒绝；私有schema最终回收。
func TestAccessGroupConfigServiceDataPlaneIsolation(t *testing.T) {
	h := newHarness(t, chatDeployment("access-allowed"), chatDeployment("access-denied"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "access-isolation")
	h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "models": []string{"access-allowed"}})
	// verify 用不同提示词绕开缓存，比较真实上游次数与账单；拒绝路径不得到达上游。
	verify := func(stage string) {
		t.Helper()
		before := len(h.upstreamCalls())
		got := h.ok("POST", "/v1/chat/completions", owner.key, map[string]any{"model": "access-allowed", "messages": []any{map[string]any{"role": "user", "content": "Reply with ACCESS_OK for " + stage + "."}}})
		if len(h.upstreamCalls()) != before+1 {
			t.Fatal("允许模型未真实调用上游")
		}
		h.flushSpend()
		cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
		want := float64(defaultReply.PromptTokens)*testInputRate + float64(defaultReply.CompletionTokens)*testOutputRate
		if !nearlyEqual(cost, want) {
			t.Fatal("费用改变", cost, want)
		}
		denied := h.do("POST", "/v1/chat/completions", owner.key, map[string]any{"model": "access-denied", "messages": []any{map[string]any{"role": "user", "content": "Reply with DENIED_CHECK."}}})
		if denied.status != 401 || len(h.upstreamCalls()) != before+1 {
			t.Fatal("配置越过模型授权", denied.describe())
		}
	}
	verify("before")
	const path = "/v1/access_group/grant-attempt"
	h.ok("POST", "/v1/access_group", admin, map[string]any{"access_group_id": "grant-attempt", "models": []string{"access-denied"}, "access_model_names": []string{"access-denied"}, "assigned_key_ids": []string{owner.keyID}, "assigned_team_ids": []string{owner.teamID}, "budget_id": "unlimited"})
	verify("created")
	h.ok("PUT", path, admin, map[string]any{"models": []string{}, "access_model_names": []string{}, "budget_id": nil})
	verify("updated")
	h.ok("DELETE", path, admin, nil)
	verify("deleted")
	blocked := h.openScope(t, admin, "access-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	h.ok("POST", "/v1/access_group", admin, map[string]any{
		"access_group_id": "zero-budget-attempt", "assigned_key_ids": []string{blocked.keyID},
		"models": []string{"access-allowed"}, "budget_id": "unlimited", "max_budget": 999999,
	})
	before := len(h.upstreamCalls())
	denied := h.do("POST", "/v1/chat/completions", blocked.key, map[string]any{"model": "access-allowed", "messages": []any{map[string]any{"role": "user", "content": "Reply with BUDGET_CHECK."}}})
	if denied.status != 403 || len(h.upstreamCalls()) != before {
		t.Fatal("零预算改变", denied.describe())
	}
	h.ok("DELETE", "/v1/access_group/zero-budget-attempt", admin, nil)
	if rows := rowsOf(h.ok("GET", "/v1/access_group", admin, nil)); len(rows) != 0 {
		t.Fatal("隔离测试访问组未清理", rows)
	}
}
