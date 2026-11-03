package regression

import (
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestGuardManagementServiceLifecycle 用真实 HTTP、私有 PostgreSQL 和本地模型验证管理拆分。
// 覆盖权限、别名、参数补丁、错误优先级、执行拦截/恢复及并发重名；HTTP 删除全部测试配置，harness 回收 schema。
func TestGuardManagementServiceLifecycle(t *testing.T) {
	h := newHarness(t, chatDeployment("guard-service"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "guard-service")
	for _, op := range []struct{ method, path string }{{"GET", "/guardrails/list"}, {"GET", "/v2/guardrails/list"}, {"POST", "/guardrails"}, {"PATCH", "/guardrails/missing"}, {"DELETE", "/guardrails/missing"}} {
		for _, actor := range []struct {
			token  string
			status int
		}{{"", 401}, {owner.session, 403}} {
			got := h.do(op.method, op.path, actor.token, nil)
			if got.status != actor.status {
				t.Fatal("管理权限改变", op, got.describe())
			}
		}
	}
	body := guardrailRow("service-block", "blocked_words", []string{"secret-token"}, "pre_call")
	body["extension"] = map[string]any{"zero": 0, "null": nil}
	params := body["litellm_params"].(map[string]any)
	params["default_on"] = false
	created := h.ok("POST", "/guardrails", admin, map[string]any{"guardrail": body}).json()
	id := stringField(created, "guardrail_id")
	path := "/guardrails/" + id
	if id == "" || created["guardrail_definition_location"] != "db" {
		t.Fatal("创建未返回真实ID", created)
	}
	h.ok("GET", path+"/info", admin, nil)
	for _, url := range []string{"/guardrails/list", "/v2/guardrails/list"} {
		if len(rowsOf(h.ok("GET", url, admin, nil), "guardrails")) != 1 {
			t.Fatal("列表别名丢失配置", url)
		}
	}
	// 显式选择规则应拦截但不拨上游；关闭 default_on 不能被错误地补成 true。
	request := map[string]any{"model": "guard-service", "messages": []any{map[string]any{"role": "user", "content": "secret-token"}}}
	h.ok("POST", "/v1/chat/completions", owner.key, request)
	before := len(h.upstreamCalls())
	request["guardrails"] = []string{"service-block"}
	blocked := h.do("POST", "/v1/chat/completions", owner.key, request)
	if blocked.status != 400 || !strings.Contains(string(blocked.body), "guardrail_failed") || len(h.upstreamCalls()) != before {
		t.Fatal("护栏未拦截或仍调用上游", blocked.describe())
	}
	h.ok("PATCH", path, admin, map[string]any{"litellm_params": map[string]any{"blocked_words": []string{"different"}, "priority": 0}})
	h.ok("POST", "/v1/chat/completions", owner.key, request)
	saved := h.ok("GET", path, admin, nil).json()
	p := saved["litellm_params"].(map[string]any)
	if p["default_on"] != false || p["priority"] != float64(0) || saved["extension"] == nil {
		t.Fatal("补丁丢失零值或扩展字段", saved)
	}
	for _, tc := range []struct {
		method, path, raw string
		status            int
	}{{"PATCH", "/guardrails/missing", "{", 404}, {"PATCH", path, "{", 400}, {"PUT", path, `{"litellm_params":null}`, 400}, {"POST", "/guardrails/list", "{}", 404}, {"GET", "/guardrails", "", 404}, {"DELETE", "/guardrails", "", 404}, {"GET", "/guardrails/a/b", "", 404}} {
		got := catalogRawRequest(t, h, tc.method, tc.path, admin, tc.raw)
		if got.status != tc.status {
			t.Fatal("错误优先级/状态码改变", tc, got.describe())
		}
	}
	// 真实并发创建同名规则：只有一次成功，其余冲突，不产生重复配置。
	var wg sync.WaitGroup
	statuses := make(chan int, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- h.do("POST", "/guardrails", admin, guardrailRow("concurrent", "local", []string{"blocked"}, "pre_call")).status
		}()
	}
	wg.Wait()
	close(statuses)
	success := 0
	for code := range statuses {
		if code == 200 {
			success++
		} else if code != 409 {
			t.Fatalf("并发创建返回意外状态 %d", code)
		}
	}
	if success != 1 {
		t.Fatalf("并发同名创建成功 %d 次", success)
	}
	for _, row := range rowsOf(h.ok("GET", "/guardrails/list", admin, nil), "guardrails") {
		h.ok("DELETE", "/guardrails/"+stringField(row, "guardrail_id"), admin, nil)
	}
	if got := h.do("GET", path, admin, nil); got.status != 404 {
		t.Fatal("删除未生效", got.describe())
	}
	if rows := rowsOf(h.ok("GET", "/guardrails/list", admin, nil), "guardrails"); len(rows) != 0 {
		t.Fatal("护栏清理残留", rows)
	}
}

// assertGuardStorageFailure 检查真实 HTTP 故障为既有 503，正文不泄露数据库错误；不修改数据。
func assertGuardStorageFailure(t *testing.T, got reply) {
	t.Helper()
	if got.status != 503 || !strings.Contains(string(got.body), "guardrail store unavailable") || strings.Contains(string(got.body), "private-") {
		t.Fatal("存储失败未正确返回", got.describe())
	}
}

// TestGuardManagementServiceDatabaseWriteFailure 使用私有schema的真实PostgreSQL约束与触发器注入写失败。
// 参数 t 为测试上下文；验证创建/更新/删除返回503且没有伪造保存，并在故障撤销后成功；清理SQL失败同样使测试失败，harness最终清理schema。
func TestGuardManagementServiceDatabaseWriteFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	path := ""
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT integrity_write_failure CHECK (kind <> 'guardrails' OR body NOT LIKE '%reject-write%')`)
	t.Cleanup(func() { catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT IF EXISTS integrity_write_failure`) })
	create := h.do(http.MethodPost, "/guardrails", admin, map[string]any{"guardrail_name": "integrity-tool", "litellm_params": map[string]any{"guardrail": "always_block"}, "note": "reject-write"})
	assertGuardStorageFailure(t, create)
	if list := rowsOf(h.ok(http.MethodGet, "/guardrails/list", admin, nil), "guardrails"); len(list) != 0 {
		t.Fatalf("失败创建仍有记录: %v", list)
	}
	created := h.ok(http.MethodPost, "/guardrails", admin, map[string]any{"guardrail_name": "integrity-tool", "litellm_params": map[string]any{"guardrail": "always_block"}, "note": "original"}).json()
	path = "/guardrails/" + stringField(created, "guardrail_id")
	assertGuardStorageFailure(t, h.do(http.MethodPut, path, admin, map[string]any{"note": "reject-write"}))
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("失败更新污染原值: %v", got)
	}
	catalogSQL(t, h, `CREATE FUNCTION integrity_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'guardrails' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`)
	catalogSQL(t, h, `CREATE TRIGGER integrity_delete_failure BEFORE DELETE ON kv FOR EACH ROW EXECUTE FUNCTION integrity_reject_delete()`)
	t.Cleanup(func() {
		catalogSQL(t, h, `DROP TRIGGER IF EXISTS integrity_delete_failure ON kv`)
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS integrity_reject_delete()`)
	})
	assertGuardStorageFailure(t, h.do(http.MethodDelete, path, admin, nil))
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("失败删除却丢失记录: %v", got)
	}
	catalogSQL(t, h, `DROP TRIGGER integrity_delete_failure ON kv`)
	h.ok(http.MethodPut, path, admin, map[string]any{"note": "recovered"})
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "recovered" {
		t.Fatalf("恢复后更新未保存: %v", got)
	}
	h.ok(http.MethodDelete, path, admin, nil)
	if list := rowsOf(h.ok(http.MethodGet, "/guardrails/list", admin, nil), "guardrails"); len(list) != 0 {
		t.Fatalf("清理后仍有记录: %v", list)
	}
}

// TestGuardManagementServiceDatabaseReadFailure 用私有schema视图仅对guardrails正文注入真实SQL读取失败，保留会话鉴权可读。
// 参数 t 为测试上下文；验证列表、详情和更新均不能伪装成功；恢复表后用HTTP读回原值，清理错误阻止通过。
func TestGuardManagementServiceDatabaseReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	path := ""
	created := h.ok(http.MethodPost, "/guardrails", admin, map[string]any{"guardrail_name": "read-failure", "litellm_params": map[string]any{"guardrail": "always_block"}, "note": "original"}).json()
	path = "/guardrails/" + stringField(created, "guardrail_id")
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
	// CASE只在请求真正读取guardrails行时失败，登录会话和其他kv种类仍使用原始正文。
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'guardrails' THEN integrity_read_failure() ELSE body END AS body, created_at FROM integrity_offline_kv`)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/guardrails/list", nil}, {http.MethodGet, path, nil},
		{http.MethodPut, path, map[string]any{"note": "changed"}},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) { assertGuardStorageFailure(t, h.do(tc.method, tc.path, admin, tc.body)) })
	}
	catalogSQL(t, h, `DROP VIEW kv`)
	catalogSQL(t, h, `ALTER TABLE integrity_offline_kv RENAME TO kv`)
	restored = true
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("读失败修改了记录: %v", got)
	}
	h.ok(http.MethodDelete, path, admin, nil)
}
