package searchconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSearchFieldsNestedRedaction 验证旧配置的嵌套秘密也脱敏，且不污染原对象；前置纯JSON对象，无外部调用或待清理数据。
func TestSearchFieldsNestedRedaction(t *testing.T) {
	original := map[string]any{"litellm_params": map[string]any{"api_key": "secret", "headers": []any{map[string]any{"Authorization": "private", "label": "unchanged"}}}, "search_tool_id": "id", "empty_secret": "", "nil_secret": nil}
	redacted := Redact(original).(map[string]any)
	raw, err := json.Marshal(redacted)
	if err != nil || strings.Contains(string(raw), "private") || strings.Contains(string(raw), `:"secret"`) {
		t.Fatalf("嵌套配置未脱敏: %s error=%v", raw, err)
	}
	if original["litellm_params"].(map[string]any)["api_key"] != "secret" || redacted["search_tool_id"] != "id" || redacted["empty_secret"] != "" || redacted["nil_secret"] != nil {
		t.Fatal("脱敏修改原对象或非秘密空值")
	}
	mergePatch(original, map[string]any{"litellm_params": nil, "name": "renamed"})
	if original["name"] != "renamed" || original["litellm_params"].(map[string]any)["api_key"] != "secret" {
		t.Fatal("无参数对象更新丢失原配置")
	}
}

// TestSearchMaskedNestedRoundTrip 验证详情整体回传时headers和数组内秘密不会被掩码覆盖；前置内存JSON，无外部数据。
// 正常非秘密嵌套更新仍生效，含掩码数组保持原值，避免用数组位置把旧秘密绑定到错误对象。
func TestSearchMaskedNestedRoundTrip(t *testing.T) {
	original := map[string]any{"litellm_params": map[string]any{"api_key": "key-secret", "headers": map[string]any{"Authorization": "header-secret", "Accept": "application/json"}, "extra": []any{map[string]any{"token": "array-secret"}}}}
	copy := Redact(original).(map[string]any)
	copy["litellm_params"].(map[string]any)["headers"].(map[string]any)["Accept"] = "text/plain"
	mergePatch(original, copy)
	params := original["litellm_params"].(map[string]any)
	if params["api_key"] != "key-secret" || params["headers"].(map[string]any)["Authorization"] != "header-secret" || params["headers"].(map[string]any)["Accept"] != "text/plain" || params["extra"].([]any)[0].(map[string]any)["token"] != "array-secret" {
		t.Fatal("嵌套掩码回写损坏秘密或普通字段无法更新")
	}
	mergeParams(params, map[string]any{"new": map[string]any{"value": "ok"}, "extra": []any{"safe", "updated"}})
	if params["extra"].([]any)[1] != "updated" || valueHasMask(params["new"]) {
		t.Fatal("非秘密复合值更新失败")
	}
}
