package regression

import (
	"reflect"
	"strings"
	"testing"
)

// TestSkillBoundaryResources 使用真实HTTP和私有PostgreSQL验证Skill配置寻址；管理员创建保留词ID后读回、删除，不能误写其他资源。
// 同时验证原权限、正常推理费用与零预算拦截；显式清理配置，harness负责账号和schema清理，不访问外部Skill源。
func TestSkillBoundaryResources(t *testing.T) {
	h := newHarness(t, chatDeployment("skill-boundary"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "skill-boundary")
	for _, id := range []string{"ordinary", "new", "list", "delete", "register", "skills", "archive"} {
		h.ok("POST", "/v1/skills", admin, map[string]any{"skill_id": id, "name": "fixture-" + id, "enabled": false, "note": nil, "zero": 0})
	}
	before, err := h.store.ListKV("skills")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ordinary", "new", "list", "delete", "register", "skills", "archive"} {
		out := h.do("GET", "/v1/skills/"+id, admin, nil)
		if out.status != 200 || out.json()["skill_id"] != id || out.json()["name"] != "fixture-"+id {
			t.Errorf("Skill详情必须按ID读取，不能将ID解释成动作: id=%s %s", id, out.describe())
		}
	}
	after, err := h.store.ListKV("skills")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Error("GET修改了Skill配置", err)
	}
	for _, actor := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.session, 403}, {owner.key, 403}} {
		for _, method := range []string{"GET", "DELETE"} {
			if out := h.do(method, "/v1/skills/new", actor.token, nil); out.status != actor.status {
				t.Errorf("Skill权限改变: %s", out.describe())
			}
		}
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("skill-boundary", "Verify skill metadata leaves inference available."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("正常推理或费用改变")
	}
	blocked := h.openScope(t, admin, "skill-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("skill-boundary", "Verify zero budget stays blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算规则改变", out.describe())
	}
	h.ok("POST", "/v1/skills", admin, map[string]any{"skill_id": "untouched", "name": "must-remain"})
	for _, id := range []string{"ordinary", "new", "list", "delete", "register", "skills", "archive"} {
		out := h.do("DELETE", "/v1/skills/"+id, admin, map[string]any{"skill_id": "untouched", "id": "untouched", "skillss": []any{"untouched", id, id}})
		if out.status != 200 || out.json()["deleted"] != float64(1) {
			t.Errorf("删除未定位真实Skill: id=%s %s", id, out.describe())
		}
		if out := h.do("GET", "/v1/skills/"+id, admin, nil); out.status != 404 {
			t.Errorf("删除后仍返回成功: %s", out.describe())
		}
	}
	kept := h.ok("GET", "/v1/skills/untouched", admin, nil)
	if kept.json()["name"] != "must-remain" {
		t.Fatal("正文指定的无关Skill被误改", kept.describe())
	}
	h.ok("DELETE", "/v1/skills/untouched", admin, nil)
	rows, err := h.store.ListKV("skills")
	if err != nil || len(rows) != 0 {
		t.Error("Skill配置清理不完整", rows, err)
	}
}

// TestSkillBoundaryArchive 在隔离数据库种入同名archive配置，验证下载入口明确501且不回显配置、不访问外部存储；权限先行，最后删除夹具和schema。
func TestSkillBoundaryArchive(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "skill-archive")
	h.ok("POST", "/v1/skills", admin, map[string]any{"skill_id": "archive", "name": "not-an-archive", "payload": "private-fixture"})
	before, err := h.store.ListKV("skills")
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.session, 403}, {owner.key, 403}, {admin, 501}} {
		out := h.do("GET", "/v1/skills/missing/archive", actor.token, nil)
		if out.status != actor.status {
			t.Errorf("下载支持边界或权限错误: %s", out.describe())
		}
		if actor.token == admin && (!strings.Contains(out.text(), "unsupported_operation") || out.header("Cache-Control") != "no-store" || strings.Contains(out.text(), "private-fixture")) {
			t.Error("下载接口把配置伪装为归档", out.describe())
		}
	}
	after, err := h.store.ListKV("skills")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Error("下载改变配置", err)
	}
	h.ok("DELETE", "/v1/skills/archive", admin, nil)
}

// TestSkillBoundaryStorageFailure 通过真实SQL故障验证Skill读取、创建和删除不会伪报成功；恢复后核对原记录，显式删除并由harness清理schema。
func TestSkillBoundaryStorageFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	h.ok("POST", "/v1/skills", admin, map[string]any{"skill_id": "new", "name": "original", "enabled": false, "note": nil, "zero": 0})
	before := h.ok("GET", "/v1/skills/new", admin, nil).json()
	if before["enabled"] != false || before["zero"] != float64(0) {
		t.Fatal("默认值改变", before)
	}
	if value, ok := before["note"]; !ok || value != nil {
		t.Fatal("null字段丢失")
	}
	// 仅破坏Skill行读取，登录会话共用KV但必须保持有效，避免401掩盖业务存储失败。
	catalogSQL(t, h, `CREATE FUNCTION skill_read_failure() RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-read-error'; END $$`)
	catalogSQL(t, h, `ALTER TABLE kv RENAME TO skill_boundary_unavailable`)
	restored := false
	t.Cleanup(func() {
		if !restored {
			catalogSQL(t, h, `DROP VIEW IF EXISTS kv`)
			catalogSQL(t, h, `ALTER TABLE skill_boundary_unavailable RENAME TO kv`)
		}
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS skill_read_failure()`)
	})
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'skills' THEN skill_read_failure() ELSE body END AS body, created_at FROM skill_boundary_unavailable`)
	for _, path := range []string{"/v1/skills", "/v1/skills/new"} {
		assertCatalogStorageFailure(t, h.do("GET", path, admin, nil))
	}
	catalogSQL(t, h, `DROP VIEW kv`)
	catalogSQL(t, h, `ALTER TABLE skill_boundary_unavailable RENAME TO kv`)
	restored = true
	// 独立注入写入和删除失败，不影响会话或其他资源。
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT skill_write_failure CHECK(kind <> 'skills' OR body NOT LIKE '%reject-write%')`)
	assertCatalogStorageFailure(t, h.do("POST", "/v1/skills", admin, map[string]any{"skill_id": "new", "name": "reject-write"}))
	catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT skill_write_failure`)
	catalogSQL(t, h, `CREATE FUNCTION skill_delete_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind = 'skills' THEN RAISE EXCEPTION 'private-delete-error'; END IF; RETURN OLD; END $$`)
	catalogSQL(t, h, `CREATE TRIGGER skill_delete_failure BEFORE DELETE ON kv FOR EACH ROW EXECUTE FUNCTION skill_delete_failure()`)
	assertCatalogStorageFailure(t, h.do("DELETE", "/v1/skills/new", admin, nil))
	catalogSQL(t, h, `DROP TRIGGER skill_delete_failure ON kv`)
	catalogSQL(t, h, `DROP FUNCTION skill_delete_failure()`)
	if got := h.ok("GET", "/v1/skills/new", admin, nil).json(); !reflect.DeepEqual(before, got) {
		t.Fatal("失败请求修改了原记录", got)
	}
	if out := catalogRawRequest(t, h, "POST", "/v1/skills", admin, `{"skill_id":`); out.status != 400 {
		t.Fatal("损坏JSON应400", out.describe())
	}
	h.ok("DELETE", "/v1/skills/new", admin, nil)
	if out := h.do("GET", "/v1/skills/new", admin, nil); out.status != 404 {
		t.Fatal("清理后应404", out.describe())
	}
}
