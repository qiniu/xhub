package regression

import (
	"reflect"
	"strings"
	"testing"
)

// TestDiscoveryBoundary 验证C7八条未实现目录不能返回公共配置、伪角色/路由或零活动。
// 前置隔离PG、真实消费和同名污染记录；断言原权限、明确501、无副作用、会话和零预算边界。
// 夹具逐条删除，身份及日志由harness清理，不访问生产或外部供应商。
func TestDiscoveryBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("discovery-boundary"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "discovery-boundary")
	chat := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("discovery-boundary", "Verify activity before discovery requests."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, chat.header("x-litellm-call-id")), "spend")
	if cost <= 0 {
		t.Fatal("未产生真实消费，不能验证伪零统计")
	}
	fixtures := map[string]string{"misc": "fixture", "end_user": "activity", "network": "client-ip", "openapi-registry": "fixture", "registry.json": "fixture", "routes": "fixture", "user": "available_roles", "utils": "available_routes"}
	for kind, id := range fixtures {
		if err := h.store.PutKV(kind, id, `{"id":"`+id+`","private_fixture":"must-not-echo"}`); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/", "/end_user/daily/activity", "/network/client-ip", "/openapi-registry", "/registry.json", "/routes", "/user/available_roles", "/utils/available_routes"} {
		t.Run(path, func(t *testing.T) {
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {owner.key, 403}, {admin, 501}} {
				out := h.do("GET", path, actor.token, nil)
				if out.status != actor.status {
					t.Errorf("目录权限或错误状态不符: %s", out.describe())
				}
				if actor.status == 501 && (!strings.Contains(out.text(), "unsupported_discovery_operation") || strings.Contains(out.text(), "must-not-echo") || out.header("Cache-Control") != "no-store") {
					t.Errorf("目录伪成功或信息泄漏: %s", out.describe())
				}
			}
		})
	}
	for kind, id := range fixtures {
		got, err := h.store.GetKV(kind, id)
		if err != nil || got["private_fixture"] != "must-not-echo" {
			t.Fatal("目录读取修改配置", kind, err)
		}
		if err := h.store.DeleteKV(kind, id); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := floatField(logDetail(t, h, admin, chat.header("x-litellm-call-id")), "spend")
	if after != cost || len(h.upstreamCalls()) != 1 {
		t.Fatal("目录读取影响真实账单或调用上游")
	}
	h.ok("GET", "/auth/me", owner.session, nil)
	blocked := h.openScope(t, admin, "discovery-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("discovery-boundary", "Verify zero budget remains blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算拦截改变", out.describe())
	}
}

// TestSearchProviderDiscovery 验证第九条发现接口只列真实探测适配器，不能受搜索配置读取失败影响。
// 使用真实HTTP/PG和角色，读回列表及未修改记录；harness回收schema，后续SearchProbe回归验证实际协议调用。
func TestSearchProviderDiscovery(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "provider-discovery")
	for _, actor := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.session, 403}, {owner.key, 403}} {
		if out := h.do("GET", "/search_tools/ui/available_providers", actor.token, nil); out.status != actor.status {
			t.Fatal("供应商目录权限改变", out.describe())
		}
	}
	rows := h.ok("GET", "/search_tools/ui/available_providers", admin, nil).json()["providers"].([]any)
	want := []any{map[string]any{"provider_name": "tavily", "ui_friendly_name": "Tavily"}, map[string]any{"provider_name": "perplexity", "ui_friendly_name": "Perplexity"}, map[string]any{"provider_name": "serper", "ui_friendly_name": "Serper"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("目录与真实适配器不一致: %#v", rows)
	}
	// 畸形旧配置不应改变静态支持目录；缺失/故障存储边界由单元测试覆盖。
	if err := h.store.PutKV("search_tools", "broken-config", `{"id":"broken-config","litellm_params":"invalid-config"}`); err != nil {
		t.Fatal(err)
	}
	got := h.ok("GET", "/search_tools/ui/available_providers", admin, nil)
	if !reflect.DeepEqual(got.json()["providers"], want) {
		t.Fatal("目录被配置污染", got.describe())
	}
	if err := h.store.DeleteKV("search_tools", "broken-config"); err != nil {
		t.Fatal(err)
	}
}
