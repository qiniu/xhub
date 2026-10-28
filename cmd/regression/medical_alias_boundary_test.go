package regression

import (
	"reflect"
	"strings"
	"testing"
)

// TestMedicalAliasBoundary 通过真实HTTP/PG验证旧文本分析入口不把请求当作公共配置操作。
// 前置独立schema、存量同名配置与可计数本地模型；验证原鉴权、明确错误、零副作用及正常计费和零预算拦截。
// 配置夹具显式清理，用户、Key与账单由harness回收；不访问外部供应商或生产。
func TestMedicalAliasBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("medical-alias"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "medical-alias")
	if err := h.store.PutKV("comprehendmedical", "sentinel", `{"id":"sentinel","name":"keep-config","private_fixture":"must-not-echo"}`); err != nil {
		t.Fatal(err)
	}
	before, err := h.store.ListKV("comprehendmedical")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path string }{
		{"root", "/comprehendmedical"},
		{"analyze", "/comprehendmedical/detect-entities-v2"},
		{"reserved-info", "/comprehendmedical/info"},
		{"reserved-delete", "/comprehendmedical/delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {owner.key, 403}, {admin, 501}} {
				out := h.do("POST", tc.path, actor.token, map[string]any{"id": "sentinel", "Text": "must-not-echo", "LanguageCode": "en"})
				if out.status != actor.status {
					t.Errorf("旧分析入口状态错误: %s", out.describe())
				}
				if actor.token == admin && (!strings.Contains(out.text(), "unsupported_operation") || strings.Contains(out.text(), "must-not-echo") || out.header("Cache-Control") != "no-store") {
					t.Errorf("旧分析入口伪成功或泄漏配置: %s", out.describe())
				}
			}
			after, err := h.store.ListKV("comprehendmedical")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Errorf("旧分析入口误写公共配置: %v err=%v", after, err)
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("未实现分析操作不能调用供应商")
			}
		})
	}
	for _, invalid := range []any{[]any{}, "invalid-object", map[string]any{"jsonrpc": "2.0", "method": "submit"}} {
		if out := h.do("POST", "/comprehendmedical", admin, invalid); out.status != 400 {
			t.Fatal("JSON校验改变", out.describe())
		}
	}
	// 未登记的方法和多层路径仍由路由器返回404，不能扩大旧接口覆盖范围。
	for _, tc := range []struct{ method, path string }{{"GET", "/comprehendmedical"}, {"POST", "/comprehendmedical/a/b"}} {
		if out := h.do(tc.method, tc.path, admin, nil); out.status != 404 {
			t.Fatal("未登记路径被接管", out.describe())
		}
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("medical-alias", "Verify chat remains available after rejected analysis operations."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if len(h.upstreamCalls()) != 1 || cost <= 0 {
		t.Fatal("正常推理与计费异常")
	}
	blocked := h.openScope(t, admin, "medical-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("medical-alias", "Verify zero budget remains blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("预算拦截改变", out.describe())
	}
	if err := h.store.DeleteKV("comprehendmedical", "sentinel"); err != nil {
		t.Fatal(err)
	}
}
