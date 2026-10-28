package regression

import (
	"net/http"
	"testing"
)

// vectorCall 构造真实管理请求；h/token指定隔离服务与身份，action/id/body指定操作。
// 返回未断言的HTTP结果；复制正文避免污染测试参数，不直接读写存储。
func vectorCall(h *harness, token, action, id string, body map[string]any) reply {
	data := map[string]any{}
	for k, v := range body {
		data[k] = v
	}
	if id != "" {
		data["vector_store_id"] = id
	}
	method := "POST"
	if action == "list" {
		method = "GET"
	}
	return h.do(method, "/vector_store/"+action, token, data)
}

// vectorOK 对已声明的真实管理请求断言200；失败包含HTTP上下文，供成功流程调用，无额外副作用。
func vectorOK(t *testing.T, h *harness, token, action, id string, body map[string]any) reply {
	t.Helper()
	got := vectorCall(h, token, action, id, body)
	if got.status != 200 {
		t.Fatalf("vector %s failed: %s", action, got.describe())
	}
	return got
}

// TestVectorConfigServiceWriteFailure 在私有PostgreSQL注入CHECK和删除触发器故障。
// 验证写失败为500且原数据保留、恢复后可写和删除；严格清理SQL与schema，失败不可跳过。
func TestVectorConfigServiceWriteFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT vector_write_failure CHECK (kind <> 'vector_store' OR body NOT LIKE '%reject-write%')`)
	t.Cleanup(func() { catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT IF EXISTS vector_write_failure`) })
	assertCatalogStorageFailure(t, vectorCall(h, admin, "new", "one", map[string]any{"note": "reject-write"}))
	if rows := rowsOf(vectorOK(t, h, admin, "list", "", nil), "data"); len(rows) != 0 {
		t.Fatal("失败创建产生记录", rows)
	}
	vectorOK(t, h, admin, "new", "one", map[string]any{"note": "original"})
	assertCatalogStorageFailure(t, vectorCall(h, admin, "update", "one", map[string]any{"note": "reject-write"}))
	if got := vectorOK(t, h, admin, "info", "one", nil).json()["note"]; got != "original" {
		t.Fatal("失败写污染", got)
	}
	catalogSQL(t, h, `CREATE FUNCTION vector_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'vector_store' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`)
	catalogSQL(t, h, `CREATE TRIGGER vector_delete_failure BEFORE DELETE ON kv FOR EACH ROW EXECUTE FUNCTION vector_reject_delete()`)
	t.Cleanup(func() {
		catalogSQL(t, h, `DROP TRIGGER IF EXISTS vector_delete_failure ON kv`)
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS vector_reject_delete()`)
	})
	assertCatalogStorageFailure(t, vectorCall(h, admin, "delete", "one", nil))
	if got := vectorOK(t, h, admin, "info", "one", nil).json()["note"]; got != "original" {
		t.Fatal("失败删除丢数据", got)
	}
	catalogSQL(t, h, `DROP TRIGGER vector_delete_failure ON kv`)
	vectorOK(t, h, admin, "update", "one", map[string]any{"note": "recovered"})
	if got := vectorOK(t, h, admin, "info", "one", nil).json()["note"]; got != "recovered" {
		t.Fatal("恢复写未保存", got)
	}
	vectorOK(t, h, admin, "delete", "one", nil)
	if rows := rowsOf(vectorOK(t, h, admin, "list", "", nil), "data"); len(rows) != 0 {
		t.Fatal("未清理", rows)
	}
}

// TestVectorConfigServiceReadFailure 用真实视图使配置读取失败、保持登录会话可读。
// 列表/详情/更新必须500且不覆盖，恢复读回原值；t.Cleanup负责故障恢复和严格schema清理。
func TestVectorConfigServiceReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	vectorOK(t, h, admin, "new", "one", map[string]any{"note": "original"})
	catalogSQL(t, h, `CREATE FUNCTION vector_read_failure() RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-read-error'; END $$`)
	catalogSQL(t, h, `ALTER TABLE kv RENAME TO vector_offline_kv`)
	restored := false
	t.Cleanup(func() {
		if !restored {
			catalogSQL(t, h, `DROP VIEW IF EXISTS kv`)
			catalogSQL(t, h, `ALTER TABLE vector_offline_kv RENAME TO kv`)
		}
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS vector_read_failure()`)
	})
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind,id,CASE WHEN kind='vector_store' THEN vector_read_failure() ELSE body END AS body,created_at FROM vector_offline_kv`)
	for _, action := range []string{"list", "info", "update"} {
		t.Run(action, func(t *testing.T) {
			assertCatalogStorageFailure(t, vectorCall(h, admin, action, "one", map[string]any{"note": "changed"}))
		})
	}
	catalogSQL(t, h, `DROP VIEW kv`)
	catalogSQL(t, h, `ALTER TABLE vector_offline_kv RENAME TO kv`)
	restored = true
	if got := vectorOK(t, h, admin, "info", "one", nil).json()["note"]; got != "original" {
		t.Fatal("读取失败覆盖记录", got)
	}
	vectorOK(t, h, admin, "delete", "one", nil)
}

// TestVectorConfigServiceLifecycle 通过真实HTTP验证五个管理操作、默认值、替换/upsert及旧权限。
// 配置不会成为数据面已索引资源，上传/搜索仍501；私有用户和schema由harness清理。
func TestVectorConfigServiceLifecycle(t *testing.T) {
	h := newHarness(t, chatDeployment("vector-model"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "vector-service")
	for _, action := range []string{"list", "new", "info", "update", "delete"} {
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {owner.session, 403}} {
			got := vectorCall(h, actor.token, action, "one", nil)
			if got.status != actor.status {
				t.Fatal("权限改变", action, got.describe())
			}
		}
	}
	created := vectorOK(t, h, admin, "new", "one", map[string]any{"name": "Display", "note": nil, "password": "remove", "vector_store": map[string]any{"note": "expanded", "extension": false}}).json()
	if created["vector_store_id"] != "one" || created["vector_store_alias"] != "Display" || created["vector_store_name"] != "Display" || created["status"] != "completed" || created["object"] != "vector_store" || created["note"] != "expanded" || created["password"] != nil {
		t.Fatal("默认/展开改变", created)
	}
	if counts, ok := created["file_counts"].(map[string]any); !ok || counts["total"] != float64(0) {
		t.Fatal("默认计数改变", created)
	}
	vectorOK(t, h, admin, "update", "one", map[string]any{"vector_store_name": "Original 用户", "name": "", "status": "", "file_counts": nil, "extension": 0})
	read := vectorOK(t, h, admin, "info", "one", nil).json()
	if read["vector_store_name"] != "Original 用户" || read["status"] != "" || read["extension"] != float64(0) {
		t.Fatal("边界更新改变", read)
	}
	for _, raw := range []string{`{"name":`, `[]`, `{"jsonrpc":"2.0"}`, `{} {}`} {
		if got := catalogRawRequest(t, h, "POST", "/vector_store/update", admin, raw); got.status != 400 {
			t.Fatal("非法正文未拒绝", got.describe())
		}
	}
	if got := vectorOK(t, h, admin, "info", "one", nil).json()["vector_store_name"]; got != "Original 用户" {
		t.Fatal("非法正文污染", got)
	}
	list := rowsOf(vectorOK(t, h, admin, "list", "", nil), "data")
	if len(list) != 1 || list[0]["vector_store_name"] != "Original 用户" {
		t.Fatal("列表未回读", list)
	}
	// 管理配置与数据面资源分离；不把历史completed元数据当作已索引文件。
	before := len(h.upstreamCalls())
	for _, prefix := range []string{"/vector_stores", "/v1/vector_stores"} {
		for _, path := range []string{prefix, prefix + "/one/files", prefix + "/one/search"} {
			got := h.do(http.MethodPost, path, owner.key, map[string]any{"query": "Find a document."})
			if got.status != 501 {
				t.Fatal("未支持能力伪成功", path, got.describe())
			}
		}
		if rows := rowsOf(h.ok("GET", prefix, owner.key, nil), "data"); len(rows) != 0 {
			t.Fatal("管理配置泄漏为数据面资源", rows)
		}
	}
	if len(h.upstreamCalls()) != before {
		t.Fatal("未支持操作发起上游")
	}
	vectorOK(t, h, admin, "delete", "one", nil)
	if got := vectorCall(h, admin, "info", "one", nil); got.status != 404 {
		t.Fatal("删除未生效", got.describe())
	}
	upsert := vectorOK(t, h, admin, "update", "one", map[string]any{"note": "upsert"}).json()
	if upsert["vector_store_id"] != "one" || upsert["status"] != "completed" {
		t.Fatal("upsert改变", upsert)
	}
	vectorOK(t, h, admin, "new", "one", map[string]any{"name": "replacement"})
	if got := vectorOK(t, h, admin, "info", "one", nil).json(); got["note"] != nil || got["name"] != "replacement" {
		t.Fatal("创建非替换", got)
	}
	vectorOK(t, h, admin, "delete", "one", nil)
	if got := vectorOK(t, h, admin, "delete", "one", nil).json()["deleted"]; got != float64(0) {
		t.Fatal("缺失删除改变", got)
	}
}

// TestVectorConfigServiceBillingIsolation 验证配置创建前/后及删除后的真实推理和费用，零预算仍拦截。
// 私有schema及本地协议上游，唯一提示避免缓存；配置和账号最终严格清理，不使用生产凭据。
func TestVectorConfigServiceBillingIsolation(t *testing.T) {
	h := newHarness(t, chatDeployment("vector-model"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "vector-billing")
	for _, stage := range []string{"before", "created", "deleted"} {
		if stage == "created" {
			vectorOK(t, h, admin, "new", "one", map[string]any{"models": []string{"none"}, "max_budget": 0})
		}
		if stage == "deleted" {
			vectorOK(t, h, admin, "delete", "one", nil)
		}
		before := len(h.upstreamCalls())
		got := h.ok("POST", "/v1/chat/completions", owner.key, map[string]any{"model": "vector-model", "messages": []any{map[string]any{"role": "user", "content": "Reply with VECTOR_OK for " + stage + "."}}})
		if len(h.upstreamCalls()) != before+1 {
			t.Fatal("管理配置影响真实调用")
		}
		h.flushSpend()
		cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
		want := float64(defaultReply.PromptTokens)*testInputRate + float64(defaultReply.CompletionTokens)*testOutputRate
		if !nearlyEqual(cost, want) {
			t.Fatal("计费改变", cost, want)
		}
	}
	blocked := h.openScope(t, admin, "vector-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	before := len(h.upstreamCalls())
	got := h.do("POST", "/v1/chat/completions", blocked.key, map[string]any{"model": "vector-model", "messages": []any{map[string]any{"role": "user", "content": "Reply with BUDGET_CHECK."}}})
	if got.status != 403 || len(h.upstreamCalls()) != before {
		t.Fatal("零预算改变", got.describe())
	}
	if rows := rowsOf(vectorOK(t, h, admin, "list", "", nil), "data"); len(rows) != 0 {
		t.Fatal("配置未清理", rows)
	}
}
