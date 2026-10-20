package billing

import (
	"math"
	"testing"

	"github.com/sunqirui1987/xhub/internal/catalog"
)

// TestStrictBudgetExplicitCacheAdditionalTTL 验证显式TTL不会排除透明上游新增的其他已知期限缓存。
// 参数t为测试上下文；创建5m/1h独立新计划，验证最坏预占、跨期限/混合实扣和冻结后价格不可变；纯内存无需清理。
func TestStrictBudgetExplicitCacheAdditionalTTL(t *testing.T) {
	for _, ttl := range []string{"5m", "1h"} {
		t.Run(ttl, func(t *testing.T) {
			d := strictBudgetDeployment()
			d.LiteLLMParams["cache_creation_input_token_cost"] = 1.25e-6
			d.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 2e-6
			body := strictBudgetBody("anthropic-messages")
			body["cache_control"] = map[string]any{"type": "ephemeral", "ttl": ttl}
			p, _ := strictBudgetMustPlan(t, d, body, "anthropic-messages")
			expected := float64(p.InputBound)*3e-6 + float64(p.OutputCap)*2e-6
			if math.Abs(p.Amount-expected) > 2e-12 || len(p.frozen.CacheWriteByTTL) != 2 {
				t.Fatalf("显式%s未冻结全部已知写价: %s", ttl, p.PriceSnapshot)
			}
			d.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 99
			for _, tc := range []struct {
				name       string
				five, hour int
				cost       float64
			}{{"five", 3, 0, 17.75e-6}, {"hour", 0, 3, 20e-6}, {"mixed", 1, 2, 19.25e-6}} {
				t.Run(tc.name, func(t *testing.T) {
					c, err := p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3}, autoCacheRaw(10, tc.five, tc.hour))
					if err != nil || math.Abs(c.Total-tc.cost) > 1e-12 {
						t.Fatalf("显式TTL下实测缓存结算错误: %+v %v", c, err)
					}
				})
			}
			raw := autoCacheRaw(10, 1, 2)
			delete(raw, "cache_creation")
			_, err := p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3}, raw)
			strictBudgetExpectError(t, err, "invalid_usage")
			raw = autoCacheRaw(10, 1, 2)
			raw["cache_creation_input_tokens"] = 4
			_, err = p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3}, raw)
			strictBudgetExpectError(t, err, "invalid_usage")
			raw = autoCacheRaw(10, 1, 2)
			raw["cache_creation"] = map[string]any{"ephemeral_24h_input_tokens": 3}
			_, err = p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3}, raw)
			strictBudgetExpectError(t, err, "invalid_usage")
		})
	}
}

// TestStrictBudgetExplicitCacheActualTierMissingRate 验证其他档的1h报价不能补足实际档缺价。
// 参数t为测试上下文；前置每档都有5m但仅高档有1h，显式5m请求；低档1h失败、高档混合精算，纯内存无需清理。
func TestStrictBudgetExplicitCacheActualTierMissingRate(t *testing.T) {
	low, high := tierRule(0, 50, 1), tierRule(50, 200, 2)
	high["details_v2"].(map[string]any)["c_1h_cache"] = tierUnit(.004)
	d := tierDeployment(low, high)
	body := strictBudgetBody("anthropic-messages")
	body["max_tokens"] = 10
	body["cache_control"] = map[string]any{"type": "ephemeral", "ttl": "5m"}
	p, _ := strictBudgetMustPlan(t, d, body, "anthropic-messages")
	if math.Abs(p.Amount-.64) > 2e-12 {
		t.Fatalf("未覆盖高档最坏费用: %g", p.Amount)
	}
	_, err := p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3}, autoCacheRaw(10, 0, 3))
	strictBudgetExpectError(t, err, "invalid_usage")
	c, err := p.Charge(catalog.Usage{PromptTokens: 45, CompletionTokens: 2, CacheWriteTokens: 6}, autoCacheRaw(45, 2, 4))
	if err != nil || math.Abs(c.Total-.119) > 1e-12 {
		t.Fatalf("实际高档混合期限计费错误: %+v %v", c, err)
	}
}
