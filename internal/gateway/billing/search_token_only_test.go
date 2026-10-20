package billing

import "testing"

// TestSearchTokenOnlyRequest 验证已知搜索类型保持原生能力且不放开其他收费工具；纯解析涵盖缺失、错误和嵌套输入，无资源清理。
func TestSearchTokenOnlyRequest(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want, bad bool
	}{
		{"modern", `{"tools":[{"type":"web_search","return_token_budget":"unlimited"}]}`, true, false},
		{"preview", `{"tools":[{"type":"web_search_preview"}]}`, true, false},
		{"dated", `{"tools":[{"type":"web_search_2025_08_26"}]}`, true, false},
		{"dated-preview", `{"tools":[{"type":"web_search_preview_2025_03_11"}]}`, true, false},
		{"empty", `{"input":"Hello"}`, false, false},
		{"invalid-json", `{`, false, false},
		{"client-tools", `{"tools":[{"type":"web_search"},{"type":"namespace","tools":[{"type":"function","name":"read"},{"type":"custom","name":"patch"}]},{"type":"tool_search","execution":"client"}]}`, true, false},
		{"image", `{"tools":[{"type":"web_search"},{"type":"image_generation"}]}`, false, true},
		{"code", `{"tools":[{"type":"web_search"},{"type":"code_interpreter"}]}`, false, true},
		{"server-search", `{"tools":[{"type":"web_search"},{"type":"tool_search","execution":"server"}]}`, false, true},
		{"nested-search", `{"tools":[{"type":"web_search"},{"type":"namespace","tools":[{"type":"web_search"}]}]}`, false, true},
		{"malformed", `{"tools":[{"type":"web_search"},42]}`, false, true},
		{"malformed-namespace", `{"tools":[{"type":"web_search"},{"type":"namespace","tools":42}]}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SearchTokenOnlyRequest("openai-responses", []byte(tc.raw))
			if got != tc.want || (err != nil) != tc.bad {
				t.Fatalf("搜索分类不符合预期 got=%v err=%v", got, err)
			}
		})
	}
	if got, err := SearchTokenOnlyRequest("openai-chat", []byte(`{"tools":[{"type":"web_search"}]}`)); got || err != nil {
		t.Fatal("非Responses不得误判为原生搜索")
	}
}

// TestSearchTokenUsage 验证仅收完整实测token与缓存费用；工具计量排除、缺失和矛盾保留未知标记，原始对象不修改，无持久资源。
func TestSearchTokenUsage(t *testing.T) {
	raw := map[string]any{"input_tokens": 100, "output_tokens": 9000, "input_tokens_details": map[string]any{"cached_tokens": 20}, "searches": 9, "web_search_calls": 3, "num_server_side_tools_used": 3}
	got := SearchTokenUsage(raw)
	if got.PromptTokens != 100 || got.CompletionTokens != 9000 || got.CachedTokens != 20 || got.Searches != 0 || got.PricingBlocked != "" {
		t.Fatalf("token-only计量错误 %+v", got)
	}
	if raw["searches"] != 9 {
		t.Fatal("修改了上游原始计量")
	}
	for _, bad := range []map[string]any{nil, {"input_tokens": 1}, {"input_tokens": 1, "prompt_tokens": 2, "output_tokens": 3}} {
		if SearchTokenUsage(bad).PricingBlocked == "" {
			t.Fatal("不完整计量不得伪装为已知费用")
		}
	}
}

// TestSearchTokenUsageModalDetails 验证搜索仅剥离非token费用仍保留视觉token分桶和输出变体；异常分桶保持未定价，无外部资源。
func TestSearchTokenUsageModalDetails(t *testing.T) {
	raw := map[string]any{"input_tokens": 100, "output_tokens": 5, "input_tokens_details": map[string]any{"text_tokens": 40, "image_tokens": 60, "cached_tokens": 10}, "output_variant": "image", "images": 2, "input_image_count": 1, "seconds": 3, "searches": 1}
	got := SearchTokenUsage(raw)
	if !got.InputTokenDetails || got.InputTextTokens != 40 || got.InputImageTokens != 60 || got.OutputVariant != "image" || got.CachedTokens != 10 || got.Images != 0 || got.InputImages != 0 || got.Seconds != 0 || got.Searches != 0 {
		t.Fatalf("token模态被丢弃或非token费用未排除 %+v", got)
	}
	raw["input_tokens"] = 50
	if SearchTokenUsage(raw).PricingBlocked == "" {
		t.Fatal("矛盾模态token不得按文字价计算")
	}
}
