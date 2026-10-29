package regression

import (
	"reflect"
	"testing"
)

// TestStage4CombinedAcceptance 在同一个真实HTTP/PG服务中交错访问五批旧入口与专用服务。
// 前置管理员、普通用户、可计费本地模型和存量配置；验证拒绝不会重置账单、修改模型/主题或清除会话，
// 且真实停用/启用、失败上游、零预算仍生效；harness清理独立schema，显式删除自建部署和KV，不访问线上。
func TestStage4CombinedAcceptance(t *testing.T) {
	h := newHarness(t)
	admin := h.personalBillingSession()
	user := h.openScope(t, admin, "stage4-combined")
	h.addDBModel(t, admin, "combined-chat", "combined-chat", "combined-deployment", nil)
	h.ok("PATCH", "/update/ui_theme_settings", admin, map[string]any{"primary_color": "#123456"})
	theme := h.ok("GET", "/get/ui_theme_settings", admin, nil).json()
	if err := h.store.PutKV("model", "block", `{"id":"block","marker":"private-combined-fixture"}`); err != nil {
		t.Fatal(err)
	}
	chat := h.ok("POST", "/v1/chat/completions", user.key, chatRequest("combined-chat", "Record a billed request before combined boundary checks."))
	h.flushSpend()
	logBefore := logDetail(t, h, admin, chat.header("x-litellm-call-id"))
	cost, _ := floatField(logBefore, "spend")
	if cost <= 0 {
		t.Fatal("组合夹具没有真实消费")
	}
	userBefore, err := h.db.GetUser(t.Context(), user.userID)
	if err != nil {
		t.Fatal(err)
	}
	models := h.ok("GET", "/v2/model/info", admin, nil).json()
	kvBefore, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
	if err != nil {
		t.Fatal(err)
	}
	// 各批完整矩阵由独立回归覆盖；这里交错选择可能互相覆盖的路径，检测同一服务中的分发与状态污染。
	for _, tc := range []struct{ method, path, kind string }{
		{"PATCH", "/update/ui_settings", "unsupported_settings_operation"},
		{"POST", "/model/block", "unsupported_model_operation"},
		{"POST", "/global/spend/reset", "unsupported_analytics_operation"},
		{"DELETE", "/fixture-provider/v1/files/combined-file", "unsupported_operation"},
		{"GET", "/routes", "unsupported_discovery_operation"},
		{"GET", "/model/info", "unsupported_model_operation"},
		{"GET", "/get/ui_settings", "unsupported_settings_operation"},
		{"GET", "/spend/logs", "unsupported_analytics_operation"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {user.session, 403}, {admin, 501}} {
				out := h.do(tc.method, tc.path, actor.token, map[string]any{"id": "block"})
				if out.status != actor.status {
					t.Fatal("组合鉴权/状态错误", out.describe())
				}
				if actor.status == 501 {
					detail, _ := out.json()["error"].(map[string]any)
					if detail["type"] != tc.kind || out.header("Cache-Control") != "no-store" {
						t.Fatal("跨批次错误分发", out.describe())
					}
				}
			}
			h.ok("GET", "/auth/me", user.session, nil)
			if !reflect.DeepEqual(theme, h.ok("GET", "/get/ui_theme_settings", admin, nil).json()) {
				t.Fatal("旧入口影响真实主题")
			}
			providers := h.ok("GET", "/search_tools/ui/available_providers", admin, nil).json()["providers"].([]any)
			if len(providers) != 3 {
				t.Fatal("旧入口影响真实搜索供应商目录", providers)
			}
		})
	}
	kvAfter, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
	if err != nil || !reflect.DeepEqual(kvBefore, kvAfter) {
		t.Fatal("组合拒绝修改了配置", err)
	}
	userAfter, err := h.db.GetUser(t.Context(), user.userID)
	if err != nil || !reflect.DeepEqual(userBefore, userAfter) {
		t.Fatal("组合拒绝修改用户或预算", err)
	}
	if !reflect.DeepEqual(models, h.ok("GET", "/v2/model/info", admin, nil).json()) || !reflect.DeepEqual(logBefore, logDetail(t, h, admin, chat.header("x-litellm-call-id"))) || len(h.upstreamCalls()) != 1 {
		t.Fatal("组合拒绝修改模型、账单或调用上游")
	}
	h.ok("POST", "/model/disable", admin, map[string]any{"id": "combined-deployment"})
	if out := h.do("POST", "/v1/chat/completions", user.key, chatRequest("combined-chat", "A disabled deployment must reject inference.")); out.status != 400 || len(h.upstreamCalls()) != 1 {
		t.Fatal("真实停用未生效", out.describe())
	}
	h.ok("POST", "/model/enable", admin, map[string]any{"id": "combined-deployment"})
	h.ok("POST", "/v1/chat/completions", user.key, chatRequest("combined-chat", "Verify inference after restoring the real deployment."))
	if len(h.upstreamCalls()) != 2 {
		t.Fatal("真实启用没有恢复上游调用")
	}
	blocked := h.openScope(t, admin, "combined-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("combined-chat", "Zero budget must remain blocked.")); out.status != 403 || len(h.upstreamCalls()) != 2 {
		t.Fatal("零预算规则改变", out.describe())
	}
	h.failUpstream(true)
	failed := h.do("POST", "/v1/chat/completions", user.key, chatRequest("combined-chat", "Record an actual upstream failure after integration."))
	h.failUpstream(false)
	if failed.status != 500 {
		t.Fatal("真实上游错误被伪成功掩盖", failed.describe())
	}
	h.flushSpend()
	if findLogByRequestID(rowsOf(h.ok("GET", "/spend/logs/ui?status_filter=error", admin, nil), "data"), failed.header("x-litellm-call-id")) == nil {
		t.Fatal("真实错误未进入日志")
	}
	h.ok("POST", "/model/delete", admin, map[string]any{"id": "combined-deployment"})
	if out := h.ok("GET", "/v2/model/info?modelId=combined-deployment", admin, nil); len(out.json()["data"].([]any)) != 0 {
		t.Fatal("模型清理失败", out.describe())
	}
	if err := h.store.DeleteKV("model", "block"); err != nil {
		t.Fatal(err)
	}
}
