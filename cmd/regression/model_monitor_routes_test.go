package regression

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestMonitorRoutingAndFallback 使用隔离数据库、真实网关及本地上游验证组请求和跨模型回退的监控统计。
// 测试会撤销来源或目标的当前授权，断言记录不再显示；独立网关、上游和 schema 在测试结束时清理。
func TestMonitorRoutingAndFallback(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "monitor-routing")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("读取上游请求: %v", err)
			w.WriteHeader(400)
			return
		}
		model, _ := body["model"].(string)
		if model == "fail-primary" {
			w.WriteHeader(500)
			writeJSON(w, map[string]any{"error": map[string]any{"message": "test upstream"}})
			return
		}
		writeJSON(w, chatAnswer(model, map[string]any{"prompt_tokens": 11, "completion_tokens": 5}))
	}))
	t.Cleanup(upstream.Close)
	for _, model := range []struct{ alias, upstream string }{{"monitor-member", "good-member"}, {"monitor-primary", "fail-primary"}, {"monitor-backup", "good-backup"}} {
		h.ok("POST", "/model/new", admin, map[string]any{"model_name": model.alias, "litellm_params": map[string]any{"model": model.upstream, "api_base": upstream.URL, "api_key": "sk-local", "custom_llm_provider": "custom", "input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}, "model_info": map[string]any{"id": model.alias, "transport": "bypass_openai_chat", "endpoint_types": []string{"chat"}, "pricing_source": "manual"}})
	}
	body := routeTemplateBody([]any{}, 1, 60, 0, 0)
	body["routing_groups"] = []any{map[string]any{"group_name": "monitor-group", "models": []string{"monitor-member"}, "routing_strategy": "cost-based-routing"}}
	templateID := routeTemplate(t, h, admin, "monitor routing", body)
	bindTemplate(t, h, admin, "key", owner.keyID, templateID)
	h.ok("PUT", "/model/fallback", admin, map[string]any{"model_name": "monitor-primary", "policy": map[string]any{"fallbacks": []string{"monitor-backup"}}})
	t.Cleanup(func() {
		bindTemplate(t, h, admin, "key", owner.keyID, "")
		h.ok("POST", "/route_template/"+templateID+"/delete", admin, map[string]any{})
		h.ok("PUT", "/model/fallback", admin, map[string]any{"model_name": "monitor-primary", "policy": map[string]any{}})
		for _, name := range []string{"monitor-member", "monitor-primary", "monitor-backup"} {
			h.ok("POST", "/model/delete", admin, map[string]any{"id": name})
		}
	})
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("monitor-group", "Verify group monitoring."))
	h.flushSpend()
	group := h.ok("GET", "/models/monitor", owner.session, nil).json()
	if got := group["summary"].(map[string]any)["requests"]; got != float64(1) {
		t.Errorf("路由组成功调用应计入监控，got=%v body=%v", got, group)
	}
	now := time.Now().UTC()
	base := "/spend/logs/ui/filter-options?start_date=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)) + "&end_date=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339))
	if got := h.ok("GET", base+"&field=model", owner.session, nil).json()["data"].([]any); len(got) != 1 || got[0] != "monitor-group" {
		t.Errorf("组调用的公开模型应出现在有权查看的日志候选中: %v", got)
	}
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("monitor-primary", "Verify fallback monitoring."))
	h.flushSpend()
	both := h.ok("GET", "/models/monitor", owner.session, nil).json()
	if got := both["summary"].(map[string]any)["requests"]; got != float64(2) {
		t.Errorf("回退成功调用应计入监控，got=%v body=%v", got, both)
	}
	if got := h.ok("GET", base+"&field=model", owner.session, nil).json()["data"].([]any); len(got) != 2 || got[0] != "monitor-group" || got[1] != "monitor-primary" {
		t.Errorf("回退调用的来源模型应出现在日志候选中: %v", got)
	}
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": owner.orgID, "models": []string{"monitor-group", "monitor-member", "monitor-primary"}})
	restricted := h.ok("GET", "/models/monitor", owner.session, nil).json()
	if got := restricted["summary"].(map[string]any)["requests"]; got != float64(1) {
		t.Fatalf("撤销实际回退目标权限后不能显示其供应商，got=%v body=%v", got, restricted)
	}
	if got := h.ok("GET", base+"&field=model", owner.session, nil).json()["data"].([]any); len(got) != 1 || got[0] != "monitor-group" {
		t.Fatalf("受限授权仍应显示组候选，且不能显示无权回退目标的记录：%v", got)
	}
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": owner.orgID, "models": []string{"monitor-backup"}})
	revoked := h.ok("GET", "/models/monitor", owner.session, nil).json()
	if got := revoked["summary"].(map[string]any)["requests"]; got != float64(0) {
		t.Fatalf("撤销请求来源权限后不能保留其监控记录，got=%v body=%v", got, revoked)
	}
	if got := h.ok("GET", base+"&field=model", owner.session, nil).json()["data"].([]any); len(got) != 0 {
		t.Fatalf("撤销来源权限后不能显示对应日志候选：%v", got)
	}
}

// TestMonitorEnvironmentIdentity 使用数据库创建的部署及本地上游验证运行时环境变量展开后的模型、连接和筛选候选。
// 前置隔离环境变量及 schema；测试还验证撤销授权不泄露候选，测试框架在结束时还原环境并清理数据库。
func TestMonitorEnvironmentIdentity(t *testing.T) {
	h := newHarness(t)
	t.Setenv("XHUB_TEST_MONITOR_ACTUAL", "env-resolved-actual")
	t.Setenv("XHUB_TEST_MONITOR_BASE", h.prices.URL+"/v1")
	admin := h.adminSession()
	owner := h.openScope(t, admin, "monitor-env")
	h.ok("POST", "/model/new", admin, map[string]any{"model_name": "monitor-env-alias", "litellm_params": map[string]any{"model": "os.environ/XHUB_TEST_MONITOR_ACTUAL", "api_base": "os.environ/XHUB_TEST_MONITOR_BASE", "api_key": "sk-fake", "custom_llm_provider": "custom", "input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}, "model_info": map[string]any{"id": "monitor-env-id", "transport": "bypass_openai_chat", "endpoint_types": []string{"chat"}, "pricing_source": "manual"}})
	t.Cleanup(func() { h.ok("POST", "/model/delete", admin, map[string]any{"id": "monitor-env-id"}) })
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("monitor-env-alias", "Verify environment model."))
	h.flushSpend()
	monitor := h.ok("GET", "/models/monitor", owner.session, nil).json()
	if got := monitor["summary"].(map[string]any)["requests"]; got != float64(1) {
		t.Fatalf("环境变量部署的成功调用应计入监控，got=%v body=%v", got, monitor)
	}
	options := monitor["options"].([]any)
	if len(options) != 1 || options[0].(map[string]any)["model"] != "env-resolved-actual" || options[0].(map[string]any)["supplier"] != h.prices.URL+"/v1" {
		t.Fatalf("监控目录必须使用解析后的身份：%v", options)
	}
	now := time.Now().UTC()
	base := "/spend/logs/ui/filter-options?start_date=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)) + "&end_date=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339))
	for _, probe := range []struct{ field, want string }{{"model", "monitor-env-alias"}, {"supplier", h.prices.URL + "/v1"}} {
		got := h.ok("GET", base+"&field="+probe.field, owner.session, nil).json()["data"].([]any)
		if len(got) != 1 || got[0] != probe.want {
			t.Fatalf("%s 应包含解析后的身份 %q: %v", probe.field, probe.want, got)
		}
	}
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": owner.orgID, "models": []string{"not-authorized"}})
	if got := h.ok("GET", base+"&field=supplier", owner.session, nil).json()["data"].([]any); len(got) != 0 {
		t.Fatalf("撤权后候选不能泄露供应商：%v", got)
	}
}
