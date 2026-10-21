package regression

import "testing"

// TestModelRenamePreservesAccessAndPrice 使用隔离数据库、真实路由和本地上游验证改名和价格绑定。
// 覆盖四层显式授权、旧Key调用、上游ID修改、冲突拒绝和持久化；harness清理schema及测试配置。
func TestModelRenamePreservesAccessAndPrice(t *testing.T) {
	h := newHarness(t, chatDeployment("occupied"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "rename")
	old, name, price := "rename-before", "rename-after", "catalog/rename-price"
	h.ok("POST", "/price/model", admin, map[string]any{"id": price, "litellm_provider": "custom", "input_cost_per_token": 0.000004, "output_cost_per_token": 0.00002})
	created := h.ok("POST", "/model/new", admin, map[string]any{"model_name": old, "litellm_params": map[string]any{"model": "custom/rename", "api_base": h.prices.URL + "/v1", "api_key": "sk-local", "custom_llm_provider": "custom"}, "model_info": map[string]any{"transport": "bypass_openai_chat", "endpoint_types": []string{"chat"}, "pricing_source": "catalog", "base_model": price}}).json()
	id := stringField(created["model_info"].(map[string]any), "id")
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": owner.orgID, "models": []string{old}})
	h.ok("POST", "/team/update", admin, map[string]any{"team_id": owner.teamID, "models": []string{old}})
	h.ok("POST", "/project/update", admin, map[string]any{"project_id": owner.projectID, "models": []string{old}})
	h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "models": []string{old}})
	h.ok("POST", "/model/update", admin, map[string]any{"model_info": map[string]any{"id": id}, "model_name": name})
	cards := listField(h.ok("GET", "/model/available", owner.session, nil).json(), "data")
	if len(cards) != 1 || cards[0]["id"] != name || cards[0]["input_price"] != float64(4) || cards[0]["output_price"] != float64(20) {
		t.Fatalf("改名后目录授权或价格丢失: %v", cards)
	}
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest(name, "Reply briefly."))
	if r := h.do("POST", "/v1/chat/completions", owner.key, chatRequest(old, "Reply briefly.")); r.status < 400 {
		t.Fatal("旧公开名仍获授权")
	}
	h.ok("POST", "/model/update", admin, map[string]any{"model_info": map[string]any{"id": id}, "litellm_params": map[string]any{"model": "custom/renamed-upstream"}})
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest(name, "Reply again."))
	if r := h.do("POST", "/model/update", owner.session, map[string]any{"id": id, "model_name": "unauthorized"}); r.status != 403 {
		t.Fatalf("普通用户改名未拒绝: %s", r.describe())
	}
	if r := h.do("POST", "/model/update", admin, map[string]any{"id": id, "model_name": "occupied"}); r.status != 409 {
		t.Fatalf("合并已有模型未拒绝: %s", r.describe())
	}
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest(name, "Reply after rejected rename."))
	h.flushSpend()
}
