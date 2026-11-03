package regression

import (
	"reflect"
	"strings"
	"testing"
)

// TestPluginRegistrationLifecycle 用真实HTTP/PostgreSQL复现按名称注册和重名保护；前置私有schema、管理员和本地模型。
// 验证名称寻址、冲突不覆盖、权限、marketplace明确不支持、推理预算与清理；不访问插件源或安装客户端。
func TestPluginRegistrationLifecycle(t *testing.T) {
	h := newHarness(t, chatDeployment("plugin-registration"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "plugin-registration")
	for _, name := range []string{"plugin-a", "plugin-b", "list"} {
		out := h.ok("POST", "/claude-code/plugins", admin, map[string]any{"name": name, "source": map[string]any{"source": "github", "repo": "example/fixture"}, "description": "original"})
		if out.json()["id"] != name {
			t.Errorf("注册没有按名称保存: %s", out.describe())
		}
		read := h.do("GET", "/claude-code/plugins/"+name, admin, nil)
		if read.status != 200 || read.json()["name"] != name {
			t.Errorf("按名称读回失败: %s", read.describe())
		}
	}
	for _, actor := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.session, 403}} {
		if out := h.do("POST", "/claude-code/plugins", actor.token, map[string]any{"name": "forbidden", "source": map[string]any{"source": "github"}}); out.status != actor.status {
			t.Fatal("注册权限改变", out.describe())
		}
	}
	for _, body := range []map[string]any{{}, {"name": "../bad", "source": map[string]any{"source": "github"}}, {"name": "missing-source"}} {
		if out := h.do("POST", "/claude-code/plugins", admin, body); out.status != 400 {
			t.Fatal("非法注册应400", out.describe())
		}
	}
	if out := catalogRawRequest(t, h, "POST", "/claude-code/plugins", admin, `{"name":`); out.status != 400 {
		t.Fatal("损坏JSON应400", out.describe())
	}
	before, err := h.store.ListKV("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	// 旧固定ID中的同名数据必须保留且拒绝重复创建，不能在升级时悄悄产生两份。
	if err := h.store.PutKV("claude-code", "plugins", `{"name":"legacy-plugin","description":"legacy-original"}`); err != nil {
		t.Fatal(err)
	}
	if out := h.do("POST", "/claude-code/plugins", admin, map[string]any{"name": "legacy-plugin", "source": map[string]any{"source": "github"}}); out.status != 409 {
		t.Fatal("旧名称冲突应409", out.describe())
	}
	if got, err := h.store.GetKV("claude-code", "plugins"); err != nil || got["description"] != "legacy-original" {
		t.Fatal("旧记录被修改", got, err)
	}
	if err := h.store.DeleteKV("claude-code", "plugins"); err != nil {
		t.Fatal("旧记录夹具清理失败", err)
	}
	out := h.do("POST", "/claude-code/plugins", admin, map[string]any{"name": "plugin-a", "source": map[string]any{"source": "github", "repo": "example/other"}})
	if out.status != 409 {
		t.Errorf("重名注册必须409且不覆盖: %s", out.describe())
	}
	after, err := h.store.ListKV("claude-code")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Error("重名注册修改了配置", err)
	}
	for _, actor := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.session, 403}, {admin, 501}} {
		out := h.do("GET", "/claude-code/marketplace.json", actor.token, nil)
		if out.status != actor.status {
			t.Errorf("marketplace身份或支持边界错误: %s", out.describe())
		}
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("plugin-registration", "Verify registry changes leave inference available."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("正常推理或费用变化")
	}
	blocked := h.openScope(t, admin, "plugin-registration-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("plugin-registration", "Verify zero budget stays blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算规则变化", out.describe())
	}
	for _, name := range []string{"plugin-a", "plugin-b", "list"} {
		h.ok("DELETE", "/claude-code/plugins/"+name, admin, nil)
	}
	if rows := rowsOf(h.ok("GET", "/claude-code/plugins", admin, nil), "plugins"); len(rows) != 0 {
		t.Error("注册配置未清理", rows)
	}
}

// TestPluginRegistrationStorageFailure注入真实SQL写失败并恢复，验证无部分记录及marketplace不回显假清单；harness回收隔离schema。
func TestPluginRegistrationStorageFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	catalogSQL(t, h, `ALTER TABLE kv ADD CONSTRAINT plugin_write_failure CHECK (kind <> 'claude-code')`)
	body := map[string]any{"name": "failure-test", "source": map[string]any{"source": "github", "repo": "example/fixture"}}
	assertCatalogStorageFailure(t, h.do("POST", "/claude-code/plugins", admin, body))
	if out := h.do("GET", "/claude-code/plugins/failure-test", admin, nil); out.status != 404 {
		t.Fatal("失败注册不应残留记录", out.describe())
	}
	catalogSQL(t, h, `ALTER TABLE kv DROP CONSTRAINT plugin_write_failure`)
	h.ok("POST", "/claude-code/plugins", admin, body)
	h.ok("DELETE", "/claude-code/plugins/failure-test", admin, nil)
	if err := h.store.PutKV("claude-code", "marketplace.json", `{"secret":"private-manifest"}`); err != nil {
		t.Fatal(err)
	}
	out := h.do("GET", "/claude-code/marketplace.json", admin, nil)
	if out.status != 501 || out.header("Cache-Control") != "no-store" || strings.Contains(out.text(), "private-manifest") {
		t.Fatal("普通KV被当作安装清单", out.describe())
	}
	if got, err := h.store.GetKV("claude-code", "marketplace.json"); err != nil || got["secret"] != "private-manifest" {
		t.Fatal("拒绝清单修改原数据", got, err)
	}
	if err := h.store.DeleteKV("claude-code", "marketplace.json"); err != nil {
		t.Fatal("清单夹具清理失败", err)
	}
}
