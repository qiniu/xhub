package family

import (
	"encoding/json"
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

// TestSearchFieldsNestedRedaction 验证旧配置的嵌套秘密也脱敏，且不污染原对象；前置纯JSON对象，无外部调用或待清理数据。
func TestSearchFieldsNestedRedaction(t *testing.T) {
	original := map[string]any{"litellm_params": map[string]any{"api_key": "secret", "headers": []any{map[string]any{"Authorization": "private", "label": "unchanged"}}}, "search_tool_id": "id", "empty_secret": "", "nil_secret": nil}
	redacted := redactCatalogObject("search_tools", original, true)
	raw, err := json.Marshal(redacted)
	if err != nil || strings.Contains(string(raw), "private") || strings.Contains(string(raw), `:"secret"`) {
		t.Fatalf("嵌套配置未脱敏: %s error=%v", raw, err)
	}
	if original["litellm_params"].(map[string]any)["api_key"] != "secret" || redacted["search_tool_id"] != "id" || redacted["empty_secret"] != "" || redacted["nil_secret"] != nil {
		t.Fatal("脱敏修改原对象或非秘密空值")
	}
	mergeSearchToolPatch(original, map[string]any{"litellm_params": nil, "name": "renamed"})
	if original["name"] != "renamed" || original["litellm_params"].(map[string]any)["api_key"] != "secret" {
		t.Fatal("无参数对象更新丢失原配置")
	}
}

// TestSearchMaskedNestedRoundTrip 验证详情整体回传时headers和数组内秘密不会被掩码覆盖；前置内存JSON，无外部数据。
// 正常非秘密嵌套更新仍生效，含掩码数组保持原值，避免用数组位置把旧秘密绑定到错误对象。
func TestSearchMaskedNestedRoundTrip(t *testing.T) {
	original := map[string]any{"litellm_params": map[string]any{"api_key": "key-secret", "headers": map[string]any{"Authorization": "header-secret", "Accept": "application/json"}, "extra": []any{map[string]any{"token": "array-secret"}}}}
	copy := redactCatalogObject("search_tools", original, true)
	copy["litellm_params"].(map[string]any)["headers"].(map[string]any)["Accept"] = "text/plain"
	mergeSearchToolPatch(original, copy)
	params := original["litellm_params"].(map[string]any)
	if params["api_key"] != "key-secret" || params["headers"].(map[string]any)["Authorization"] != "header-secret" || params["headers"].(map[string]any)["Accept"] != "text/plain" || params["extra"].([]any)[0].(map[string]any)["token"] != "array-secret" {
		t.Fatal("嵌套掩码回写损坏秘密或普通字段无法更新")
	}
	mergeSearchParams(params, map[string]any{"new": map[string]any{"value": "ok"}, "extra": []any{"safe", "updated"}})
	if params["extra"].([]any)[1] != "updated" || searchValueHasMask(params["new"]) {
		t.Fatal("非秘密复合值更新失败")
	}
}
