package billing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/sunqirui1987/xhub/internal/catalog"
)

// autoCacheRaw 构造5m/1h真实usage形状，输入是非缓存tokens，输出固定2；纯内存测试辅助无需清理。
func autoCacheRaw(input, five, hour int) map[string]any {
	return map[string]any{"input_tokens": input, "output_tokens": 2, "cache_creation_input_tokens": five + hour, "cache_creation": map[string]any{"ephemeral_5m_input_tokens": five, "ephemeral_1h_input_tokens": hour}}
}

// TestStrictBudgetAutomaticCacheTTL 验证未声明TTL的普通请求预占最高已知写价，按真实5m/1h或混合桶收费。
// 参数t为测试上下文；使用独立合成部署和冻结快照，原配置变化不能影响结算；无网络、数据库和清理需求。
func TestStrictBudgetAutomaticCacheTTL(t *testing.T) {
	d := strictBudgetDeployment()
	d.LiteLLMParams["cache_creation_input_token_cost"] = 1.25e-6
	d.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 2e-6
	p, _ := strictBudgetMustPlan(t, d, strictBudgetBody("anthropic-messages"), "anthropic-messages")
	expected := float64(p.InputBound)*3e-6 + float64(p.OutputCap)*2e-6
	if math.Abs(p.Amount-expected) > 2e-12 {
		t.Fatalf("自动缓存未按最坏已知费用预占: %g want %g", p.Amount, expected)
	}
	if len(p.frozen.CacheWriteByTTL) != 2 || p.frozen.CacheWrite.Variant != "1h" {
		t.Fatalf("缓存费率未完整冻结: %s", p.PriceSnapshot)
	}
	var persisted map[string]any
	if json.Unmarshal([]byte(p.PriceSnapshot), &persisted) != nil || len(persisted["cache_write_rates_by_ttl"].(map[string]any)) != 2 {
		t.Fatal("持久快照遗漏缓存期限价格")
	}
	d.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 99
	before := p.PriceSnapshot
	for _, tc := range []struct {
		name       string
		five, hour int
		want       float64
	}{{"five", 3, 0, 17.75e-6}, {"hour", 0, 3, 20e-6}, {"mixed", 1, 2, 19.25e-6}, {"no_write", 0, 0, 14e-6}} {
		t.Run(tc.name, func(t *testing.T) {
			raw := autoCacheRaw(10, tc.five, tc.hour)
			if err := ValidateStrictBudgetUsage(raw, p); err != nil {
				t.Fatal(err)
			}
			c, err := p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: tc.five + tc.hour}, raw)
			if err != nil || math.Abs(c.Total-tc.want) > 1e-12 {
				t.Fatalf("自动缓存实扣错误: %+v %v want %g", c, err, tc.want)
			}
		})
	}
	if p.PriceSnapshot != before {
		t.Fatal("结算修改了冻结快照")
	}
}

// TestStrictBudgetAutomaticCacheTTLFailures 验证未知期限、缺少实测、错误总数、异常类型和显式期限冲突仍失败。
// 前置双期限计划，逐例构造原始和归一化计量；期望invalid_usage，不能按最大费率代替缺失事实；纯内存无需清理。
func TestStrictBudgetAutomaticCacheTTLFailures(t *testing.T) {
	d := strictBudgetDeployment()
	d.LiteLLMParams["cache_creation_input_token_cost"] = 1.25e-6
	d.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 2e-6
	p, _ := strictBudgetMustPlan(t, d, strictBudgetBody("anthropic-messages"), "anthropic-messages")
	cases := map[string]func(map[string]any){
		"missing_buckets": func(r map[string]any) { delete(r, "cache_creation") },
		"unknown_bucket":  func(r map[string]any) { r["cache_creation"] = map[string]any{"ephemeral_24h_input_tokens": 3} },
		"wrong_total":     func(r map[string]any) { r["cache_creation_input_tokens"] = 4 },
		"zero_buckets": func(r map[string]any) {
			r["cache_creation"] = map[string]any{"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0}
		},
		"fractional": func(r map[string]any) { r["cache_creation"] = map[string]any{"ephemeral_1h_input_tokens": 3.5} },
		"overflow": func(r map[string]any) {
			r["cache_creation"] = map[string]any{"ephemeral_5m_input_tokens": 2147483647, "ephemeral_1h_input_tokens": 2147483647}
		},
		"bad_shape": func(r map[string]any) { r["cache_creation"] = "1h" },
		"bad_alias": func(r map[string]any) { r["cache_write_tokens"] = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			raw := autoCacheRaw(10, 0, 3)
			mutate(raw)
			_, err := p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3}, raw)
			strictBudgetExpectError(t, err, "invalid_usage")
		})
	}
	_, err := p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3})
	strictBudgetExpectError(t, err, "invalid_usage")
	_, err = p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 0}, autoCacheRaw(10, 0, 3))
	strictBudgetExpectError(t, err, "invalid_usage")
	delete(d.LiteLLMParams, "cache_creation_input_token_cost_above_1hr")
	single, _ := strictBudgetMustPlan(t, d, strictBudgetBody("anthropic-messages"), "anthropic-messages")
	_, err = single.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3}, autoCacheRaw(10, 0, 3))
	strictBudgetExpectError(t, err, "invalid_usage")
	if _, err = single.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CacheWriteTokens: 3}); err != nil {
		t.Fatalf("单5m旧聚合账单不再兼容: %v", err)
	}
}

// TestStrictBudgetAutomaticCacheTTLTiers 验证混合TTL费用按实际上下文档位结算、边界保持完整契约比较。
// 参数t为测试上下文；每档冻结两费率，覆盖跨档总输入与共享边界不确定性；独立合成规则无需外部清理。
func TestStrictBudgetAutomaticCacheTTLTiers(t *testing.T) {
	low, high := tierRule(0, 50, 1), tierRule(50, 200, 2)
	low["details_v2"].(map[string]any)["c_1h_cache"] = tierUnit(.002)
	high["details_v2"].(map[string]any)["c_1h_cache"] = tierUnit(.004)
	p := tierPlan(t, tierDeployment(low, high))
	if math.Abs(p.Amount-.64) > 2e-12 {
		t.Fatalf("分档缓存最大预占错误: %g", p.Amount)
	}
	for _, tc := range []struct {
		name              string
		input, five, hour int
		want              float64
		bad               bool
	}{{"low", 40, 2, 3, .0525, false}, {"high", 45, 2, 4, .119, false}, {"boundary", 45, 2, 3, 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := p.Charge(catalog.Usage{PromptTokens: tc.input, CompletionTokens: 2, CacheWriteTokens: tc.five + tc.hour}, autoCacheRaw(tc.input, tc.five, tc.hour))
			if tc.bad {
				strictBudgetExpectError(t, err, "invalid_usage")
				return
			}
			if err != nil || math.Abs(c.Total-tc.want) > 1e-12 {
				t.Fatalf("实测上下文/TTL档位错误: %+v %v want %g", c, err, tc.want)
			}
		})
	}
	// 两档最高价相同也不能掩盖5m价格差异；共享边界必须比较每一个冻结桶。
	low, high = tierRule(0, 50, 1), tierRule(50, 200, 1)
	low["details_v2"].(map[string]any)["c_1h_cache"] = tierUnit(.01)
	high["details_v2"].(map[string]any)["c_1h_cache"] = tierUnit(.01)
	high["details_v2"].(map[string]any)["c_cache"] = tierUnit(.002)
	p = tierPlan(t, tierDeployment(low, high))
	_, err := p.Charge(catalog.Usage{PromptTokens: 45, CompletionTokens: 2, CacheWriteTokens: 5}, autoCacheRaw(45, 5, 0))
	strictBudgetExpectError(t, err, "invalid_usage")
}
