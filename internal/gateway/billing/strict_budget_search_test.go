package billing

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
)

// searchBudgetDeployment 返回本地受信搜索计价夹具；独立手工价格和累计输入上界，无外部调用，GC清理。
func searchBudgetDeployment() config.ModelEntry {
	d := strictBudgetDeployment()
	d.ModelInfo["web_search_budget_profile"] = "openai_responses_search_actions"
	d.ModelInfo["web_search_budget_source"] = "local fixture: total input <= 200, total output cap and at most two calls, search action price"
	d.ModelInfo["web_search_budget_input_tokens"] = 200
	d.ModelInfo["web_search_budget_max_calls"] = 2
	d.LiteLLMParams["search_context_cost_per_query"] = 0.01
	return d
}

// TestStrictBudgetSearchContract 验证文字与搜索费用共同预占、真实次数结算、快照不随配置变化和历史续轮。
// 前置本地明确契约；只用内存，正常、未用搜索与越界用量都覆盖，无需持久清理。
func TestStrictBudgetSearchContract(t *testing.T) {
	d := searchBudgetDeployment()
	b := strictBudgetBody("openai-responses")
	b["tools"] = []any{map[string]any{"type": "web_search"}}
	b["max_output_tokens"] = 10
	p, out := strictBudgetMustPlan(t, d, b, "openai-responses")
	if out["max_tool_calls"] != float64(1) || p.SearchCalls != 1 || p.InputBound != 200 || math.Abs(p.Amount-0.01022) > 1e-11 {
		t.Fatalf("搜索预占上限错误: %+v %v", p, out)
	}
	d.LiteLLMParams["search_context_cost_per_query"] = 99.0
	for _, n := range []int{0, 1} {
		raw := map[string]any{"input_tokens": 20, "output_tokens": 5, "xhub_web_search_calls": n, "xhub_web_search_searches": n}
		if err := ValidateStrictBudgetUsage(raw, p); err != nil {
			t.Fatalf("合法搜索用量被拒: %v", err)
		}
		c, err := p.Charge(catalog.Usage{PromptTokens: 20, CompletionTokens: 5, Searches: n})
		if err != nil || math.Abs(c.Total-(0.00003+float64(n)*0.01)) > 1e-11 {
			t.Fatalf("未按冻结搜索价结算: %+v %v", c, err)
		}
	}
	for _, raw := range []map[string]any{
		{"input_tokens": 20, "output_tokens": 5},
		{"input_tokens": 20, "output_tokens": 5, "xhub_web_search_calls": 2, "xhub_web_search_searches": 1},
		{"input_tokens": 20, "output_tokens": 5, "xhub_web_search_calls": 1, "xhub_web_search_searches": 2},
		{"input_tokens": 20, "output_tokens": 5, "xhub_web_search_calls": 1, "xhub_web_search_searches": 1, "searches": 0},
		{"input_tokens": 20, "output_tokens": 5, "xhub_web_search_calls": 1, "xhub_web_search_searches": 1, "web_search_calls": 2},
		{"input_tokens": 20, "output_tokens": 5, "xhub_web_search_calls": 1, "xhub_web_search_searches": 1, "num_server_side_tools_used": 2},
		{"input_tokens": 20, "output_tokens": 5, "xhub_web_search_calls": 1, "xhub_web_search_searches": 1, "search_tokens": 100},
		{"input_tokens": 20, "output_tokens": 5, "xhub_web_search_calls": 1, "xhub_web_search_searches": 1, "server_side_tool_usage_details": map[string]any{"web_search_requests": 1}},
	} {
		if ValidateStrictBudgetUsage(raw, p) == nil {
			t.Fatalf("缺失/矛盾搜索事实被接受: %v", raw)
		}
	}
	b["input"] = []any{map[string]any{"type": "web_search_call", "id": "ws_history", "status": "completed", "action": map[string]any{"type": "search", "queries": []any{"Go"}}}, map[string]any{"role": "user", "content": "Continue using this source."}}
	strictBudgetMustPlan(t, searchBudgetDeployment(), b, "openai-responses")
}

// TestStrictBudgetSearchFailures 验证未认证供应商、缺失/非有限价格、非法cap、历史和嵌套工具不能绕过预算。
// 各子例独立内存部署，预期均在出站前拒绝，GC清理。
func TestStrictBudgetSearchFailures(t *testing.T) {
	cases := []struct {
		name   string
		change func(config.ModelEntry, map[string]any)
	}{
		{"no profile", func(d config.ModelEntry, b map[string]any) { delete(d.ModelInfo, "web_search_budget_profile") }},
		{"no source", func(d config.ModelEntry, b map[string]any) { d.ModelInfo["web_search_budget_source"] = " " }},
		{"no cumulative bound", func(d config.ModelEntry, b map[string]any) { delete(d.ModelInfo, "web_search_budget_input_tokens") }},
		{"small cumulative bound", func(d config.ModelEntry, b map[string]any) { d.ModelInfo["web_search_budget_input_tokens"] = 99 }},
		{"mixed rates", func(d config.ModelEntry, b map[string]any) {
			d.LiteLLMParams["rates"] = []any{map[string]any{"measure": "query", "usd": 0.02}}
		}},
		{"no price", func(d config.ModelEntry, b map[string]any) { delete(d.LiteLLMParams, "search_context_cost_per_query") }},
		{"zero price", func(d config.ModelEntry, b map[string]any) { d.LiteLLMParams["search_context_cost_per_query"] = 0 }},
		{"nonfinite price", func(d config.ModelEntry, b map[string]any) {
			d.LiteLLMParams["search_context_cost_per_query"] = math.Inf(1)
		}},
		{"negative cap", func(d config.ModelEntry, b map[string]any) { b["max_tool_calls"] = -1 }},
		{"fractional cap", func(d config.ModelEntry, b map[string]any) { b["max_tool_calls"] = 1.5 }},
		{"cap exceeds contract", func(d config.ModelEntry, b map[string]any) { b["max_tool_calls"] = 3 }},
		{"unlimited search content", func(d config.ModelEntry, b map[string]any) {
			b["tools"] = []any{map[string]any{"type": "web_search", "return_token_budget": "unlimited"}}
		}},
		{"preview", func(d config.ModelEntry, b map[string]any) {
			b["tools"] = []any{map[string]any{"type": "web_search_preview"}}
		}},
		{"other hosted tool", func(d config.ModelEntry, b map[string]any) {
			b["tools"] = append(b["tools"].([]any), map[string]any{"type": "image_generation"})
		}},
		{"nested search", func(d config.ModelEntry, b map[string]any) {
			b["tools"] = []any{map[string]any{"type": "namespace", "tools": []any{map[string]any{"type": "web_search"}}}}
		}},
		{"incomplete history", func(d config.ModelEntry, b map[string]any) {
			b["input"] = []any{map[string]any{"type": "web_search_call", "id": "ws", "status": "searching"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := searchBudgetDeployment()
			b := strictBudgetBody("openai-responses")
			b["tools"] = []any{map[string]any{"type": "web_search"}}
			tc.change(d, b)
			raw, _ := json.Marshal(b)
			if _, _, err := StrictBudgetPlan(d, raw, "openai-responses", time.Now()); err == nil {
				t.Fatal("危险或未认证搜索请求被放行")
			}
		})
	}
}
