package regression

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestCatalogIntegrityLifecycle 验证通用管理目录的真实 HTTP 契约，涵盖认证、缺省/null/零、凭据脱敏、更新和删除。
// 参数 t 为测试上下文；前置隔离 PostgreSQL，无付费上游；预期读回真实字段、缺失资源404，显式删除资源后由 harness 删除 schema。
func TestCatalogIntegrityLifecycle(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "catalog-integrity")
	const path = "/credentials/integrity-credential"
	for _, tc := range []struct {
		name, token string
		status      int
	}{
		{"anonymous", "", 401}, {"ordinary-user", owner.session, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := h.do(http.MethodPost, "/credentials", tc.token, map[string]any{"credential_name": "integrity-credential"})
			if r.status != tc.status {
				t.Fatalf("无管理权限创建目录: %s", r.describe())
			}
		})
	}
	h.ok(http.MethodPost, "/credentials", admin, map[string]any{
		"credential_name": "integrity-credential", "note": nil, "zero": 0, "enabled": false,
		"credential_values": map[string]any{"api_key": "fixture-only-secret", "api_base": "http://127.0.0.1:1"},
	})
	read := h.ok(http.MethodGet, strings.Replace(path, "/credentials/", "/credentials/by_name/", 1), admin, nil)
	obj := read.json()
	if _, exists := obj["absent"]; exists || obj["zero"] != float64(0) || obj["enabled"] != false {
		t.Fatalf("零值或缺省字段改变: %s", read.describe())
	}
	if value, present := obj["note"]; !present || value != nil {
		t.Fatalf("显式null丢失: %s", read.describe())
	}
	if strings.Contains(read.text(), "fixture-only-secret") {
		t.Fatal("读取目录泄露凭据")
	}
	h.ok(http.MethodPatch, path, admin, map[string]any{"note": "saved", "credential_values": map[string]any{"api_key": "********"}})
	if got := h.ok(http.MethodGet, strings.Replace(path, "/credentials/", "/credentials/by_name/", 1), admin, nil).json(); got["note"] != "saved" || got["zero"] != float64(0) {
		t.Fatalf("成功更新没有持久化或覆盖缺省字段: %v", got)
	}
	deleted := h.ok(http.MethodDelete, path, admin, nil)
	if deleted.json()["deleted"] != float64(1) {
		t.Fatalf("删除计数错误: %s", deleted.describe())
	}
	if missing := h.do(http.MethodGet, strings.Replace(path, "/credentials/", "/credentials/by_name/", 1), admin, nil); missing.status != 404 {
		t.Fatalf("不存在的对象被伪造: %s", missing.describe())
	}
	if again := h.ok(http.MethodDelete, path, admin, nil); again.json()["deleted"] != float64(0) {
		t.Fatalf("重复删除不幂等: %s", again.describe())
	}
	// 保留通用目录既有的 upsert 行为，不把修复数据库吞错扩张为更新语义重写。
	h.ok(http.MethodPatch, path, admin, map[string]any{"note": "upsert"})
	if got := h.ok(http.MethodGet, strings.Replace(path, "/credentials/", "/credentials/by_name/", 1), admin, nil).json()["note"]; got != "upsert" {
		t.Fatalf("既有upsert失效: %v", got)
	}
	h.ok(http.MethodDelete, path, admin, nil)
}

// TestCatalogIntegrityInvalidJSON 验证损坏JSON不能通过真实管理HTTP入口写入或覆盖目录。
// 参数 t 为测试上下文；使用隔离数据库和真实鉴权；正常空白删除保持兼容，每个失败后回读对象和列表不变，最后清理记录及schema。
func TestCatalogIntegrityInvalidJSON(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/credentials/integrity-json"
	// 本修复不引入新的大小策略；历史允许的大对象仍应完整读写。
	large := strings.Repeat("x", (1<<20)+1)
	h.ok(http.MethodPost, "/credentials", admin, map[string]any{"credential_name": "integrity-large", "note": large})
	if got := h.ok(http.MethodGet, "/credentials/by_name/integrity-large", admin, nil).json()["note"]; got != large {
		t.Fatal("合法大对象被新增大小限制拒绝或截断")
	}
	h.ok(http.MethodDelete, "/credentials/integrity-large", admin, nil)
	h.ok(http.MethodPost, "/credentials", admin, map[string]any{"credential_name": "integrity-json", "note": "original"})
	before := h.ok(http.MethodGet, strings.Replace(path, "/credentials/", "/credentials/by_name/", 1), admin, nil).json()
	beforeList := h.ok(http.MethodGet, "/credentials", admin, nil).json()
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		for name, body := range map[string]string{"truncated": `{"note":"changed",`, "null": `null`, "array": `[]`, "scalar": `42`, "multiple": `{} {}`, "trailing": `{}oops`} {
			t.Run(method+"/"+name, func(t *testing.T) {
				target := path
				if method == http.MethodPost {
					target = "/credentials"
				}
				r := catalogRawRequest(t, h, method, target, admin, body)
				if r.status != 400 {
					t.Fatalf("非法JSON未拒绝: %s", r.describe())
				}
				if !reflect.DeepEqual(before, h.ok(http.MethodGet, strings.Replace(path, "/credentials/", "/credentials/by_name/", 1), admin, nil).json()) {
					t.Fatal("非法更新污染原记录")
				}
				if !reflect.DeepEqual(beforeList, h.ok(http.MethodGet, "/credentials", admin, nil).json()) {
					t.Fatal("非法JSON创建了额外记录")
				}
			})
		}
	}
	if r := catalogRawRequest(t, h, http.MethodDelete, path, admin, " \n"); r.status != 200 || r.json()["deleted"] != float64(1) {
		t.Fatalf("空白删除正文兼容性改变: %s", r.describe())
	}
}

// TestCatalogIntegrityDatabaseWriteFailure 使用私有schema的真实PostgreSQL约束与触发器注入写失败。
// 参数 t 为测试上下文；验证创建/更新/删除返回500且没有伪造保存，并在故障撤销后成功；清理SQL失败同样使测试失败，harness最终清理schema。
func TestCatalogIntegrityDatabaseWriteFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/search_tools/integrity-tool"
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT integrity_write_failure CHECK (kind <> 'search_tools' OR body NOT LIKE '%reject-write%')`)
	t.Cleanup(func() { catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT IF EXISTS integrity_write_failure`) })
	create := h.do(http.MethodPost, "/search_tools", admin, map[string]any{"search_tool_id": "integrity-tool", "note": "reject-write"})
	assertCatalogStorageFailure(t, create)
	if list := rowsOf(h.ok(http.MethodGet, "/search_tools/list", admin, nil), "search_tools"); len(list) != 0 {
		t.Fatalf("失败创建仍有记录: %v", list)
	}
	h.ok(http.MethodPost, "/search_tools", admin, map[string]any{"search_tool_id": "integrity-tool", "note": "original"})
	assertCatalogStorageFailure(t, h.do(http.MethodPut, path, admin, map[string]any{"note": "reject-write"}))
	if got := h.ok(http.MethodGet, path, admin, nil).json()["note"]; got != "original" {
		t.Fatalf("失败更新污染原值: %v", got)
	}
	catalogSQL(t, h, `CREATE FUNCTION integrity_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'search_tools' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`)
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
	if list := rowsOf(h.ok(http.MethodGet, "/search_tools/list", admin, nil), "search_tools"); len(list) != 0 {
		t.Fatalf("清理后仍有记录: %v", list)
	}
}

// TestCatalogIntegrityDatabaseReadFailure 用私有schema视图仅对search_tools正文注入真实SQL读取失败，保留会话鉴权可读。
// 参数 t 为测试上下文；验证列表、详情和更新均不能伪装成功；恢复表后用HTTP读回原值，清理错误阻止通过。
func TestCatalogIntegrityDatabaseReadFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	const path = "/search_tools/read-failure"
	h.ok(http.MethodPost, "/search_tools", admin, map[string]any{"search_tool_id": "read-failure", "note": "original"})
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
	// CASE只在请求真正读取search_tools行时失败，登录会话和其他kv种类仍使用原始正文。
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'search_tools' THEN integrity_read_failure() ELSE body END AS body, created_at FROM integrity_offline_kv`)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/search_tools/list", nil}, {http.MethodGet, path, nil},
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

// TestCatalogIntegrityBatchDelete 验证真实批量删除在中途数据库故障时返回500并停止，避免部分完成却显示全成功。
// 参数 t 为上下文；私有schema触发器仅拒绝第二项，验证第一项已删而后两项仍存；撤销触发器后清理剩余记录和schema，不承诺事务回滚。
func TestCatalogIntegrityBatchDelete(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	names := []string{"integrity-first", "integrity-blocked", "integrity-last"}
	for _, name := range names {
		h.ok(http.MethodPost, "/tag/new", admin, map[string]any{"name": name, "tag_name": name})
	}
	catalogSQL(t, h, `CREATE FUNCTION integrity_batch_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'tag' AND OLD.id = 'integrity-blocked' THEN RAISE EXCEPTION 'private-batch-error'; END IF; RETURN OLD; END $$`)
	catalogSQL(t, h, `CREATE TRIGGER integrity_batch_failure BEFORE DELETE ON kv FOR EACH ROW EXECUTE FUNCTION integrity_batch_reject()`)
	t.Cleanup(func() {
		catalogSQL(t, h, `DROP TRIGGER IF EXISTS integrity_batch_failure ON kv`)
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS integrity_batch_reject()`)
	})
	assertCatalogStorageFailure(t, h.do(http.MethodPost, "/tag/delete", admin, map[string]any{"tags": names}))
	// /tag/list 是用量聚合的专用读取；用真正的目录详情接口核对逐项持久化状态。
	for i, name := range names {
		got := h.do(http.MethodPost, "/tag/info", admin, map[string]any{"name": name})
		if i == 0 && got.status != 404 {
			t.Fatalf("已删除首项仍存在: %s", got.describe())
		}
		if i > 0 && (got.status != 200 || got.json()["id"] != name) {
			t.Fatalf("失败后未停止或丢失剩余记录: %s", got.describe())
		}
	}
	catalogSQL(t, h, `DROP TRIGGER integrity_batch_failure ON kv`)
	deleted := h.ok(http.MethodPost, "/tag/delete", admin, map[string]any{"tags": names})
	if deleted.json()["deleted"] != float64(2) {
		t.Fatalf("恢复后剩余删除计数错误: %s", deleted.describe())
	}
	for _, name := range names {
		if got := h.do(http.MethodPost, "/tag/info", admin, map[string]any{"name": name}); got.status != 404 {
			t.Fatalf("批量清理后残留: %s", got.describe())
		}
	}
}

// catalogSQL 仅供本文件的隔离数据库故障注入及恢复；参数为测试、harness、固定SQL，返回无，执行错误立即报告失败。
// 不接受用户输入或连接生产；调用方注册清理，避免测试成功却遗留触发器或表重命名。
func catalogSQL(t *testing.T, h *harness, statement string) {
	t.Helper()
	if _, err := h.store.Engine.Exec(statement); err != nil {
		t.Fatalf("隔离数据库故障准备/清理失败: %v", err)
	}
}

// assertCatalogStorageFailure 检查真实HTTP错误包络，不只匹配500；参数为测试与响应，返回无，不写数据。
// 必须返回固定内部错误且无底层SQL、凭据或成功对象，失败信息只包含专用测试输入。
func assertCatalogStorageFailure(t *testing.T, r reply) {
	t.Helper()
	err, ok := r.json()["error"].(map[string]any)
	if r.status != 500 || !ok || err["type"] != "internal" || err["message"] != "catalog store failure" {
		t.Fatalf("存储失败被伪装成功或泄漏详情: %s", r.describe())
	}
}

// catalogRawRequest 为非法JSON用例发送未经过Marshal的真实HTTP正文；参数为测试、harness、方法、路径、临时凭据和原文。
// 返回状态与原始响应，网络或读取失败终止测试；响应体总是关闭，业务数据由调用方清理。
func catalogRawRequest(t *testing.T, h *harness, method, path, token, body string) reply {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: resp.StatusCode, body: raw, headers: resp.Header.Clone()}
}
