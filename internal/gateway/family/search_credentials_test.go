package family

import (
	"net/http"
	"strings"
	"testing"
)

// TestSearchCredentialsMergeAndRedact 验证搜索配置的真实存储边界不会把编辑掩码写入Key，且创建/列表/详情都不返回秘密。
// 参数t为测试上下文；前置独立内存存储，正常更新、省略/空白/掩码与新Key均覆盖；测试结束对象自行回收，真实数据库由regression验证。
func TestSearchCredentialsMergeAndRedact(t *testing.T) {
	st := &catalogMemoryStore{rows: map[string]map[string]any{}}
	secret := "fixture-search-secret"
	body := map[string]any{"search_tool_id": "secret-test", "litellm_params": map[string]any{"search_provider": "tavily", "api_key": secret, "api_base": "https://example.com/search"}}
	created := callCatalogStore(st, http.MethodPost, "/search_tools", body)
	if created.Code != 200 || strings.Contains(created.Body.String(), secret) {
		t.Fatalf("创建响应泄漏秘密或保存失败: %s", created.Body.String())
	}
	for _, value := range []any{nil, "", "   ", "********"} {
		updated := callCatalogStore(st, http.MethodPut, "/search_tools/secret-test", map[string]any{"litellm_params": map[string]any{"api_key": value, "api_base": "https://example.com/new"}})
		if updated.Code != 200 || strings.Contains(updated.Body.String(), secret) {
			t.Fatalf("编辑响应失败或泄漏秘密: %s", updated.Body.String())
		}
		saved, err := st.GetKV("search_tools", "secret-test")
		if err != nil {
			t.Fatal(err)
		}
		params := saved["litellm_params"].(map[string]any)
		if params["api_key"] != secret || params["search_provider"] != "tavily" || params["api_base"] != "https://example.com/new" {
			t.Fatal("普通字段更新丢失Key或省略配置")
		}
	}
	changed := callCatalogStore(st, http.MethodPut, "/search_tools/secret-test", map[string]any{"litellm_params": map[string]any{"api_key": "replacement-secret"}})
	if changed.Code != 200 {
		t.Fatal("替换Key失败")
	}
	saved, _ := st.GetKV("search_tools", "secret-test")
	if saved["litellm_params"].(map[string]any)["api_key"] != "replacement-secret" {
		t.Fatal("明确输入新Key未保存")
	}
	for _, path := range []string{"/search_tools/list", "/search_tools/secret-test"} {
		response := callCatalogStore(st, http.MethodGet, path, nil)
		if response.Code != 200 || strings.Contains(response.Body.String(), "replacement-secret") || !strings.Contains(response.Body.String(), "********") {
			t.Fatalf("列表/详情未正确脱敏: %s", response.Body.String())
		}
	}
}
