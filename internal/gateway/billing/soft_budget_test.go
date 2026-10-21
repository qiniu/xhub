package billing

import (
	"math"
	"testing"
	"time"
)

// TestSoftPricePlan 验证无窗口、零用量、大于旧窗口、缓存TTL与价格冻结；纯内存夹具无需清理。
func TestSoftPricePlan(t *testing.T) {
	dep := strictBudgetDeployment()
	delete(dep.ModelInfo, "max_input_tokens")
	dep.LiteLLMParams["cache_creation_input_token_cost"] = 1.25e-6
	dep.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 2e-6
	plan := NewSoftPricePlan(dep, time.Now())
	dep.LiteLLMParams["input_cost_per_token"] = 99
	for _, tc := range []struct {
		name string
		raw  map[string]any
		want float64
	}{
		{"normal", map[string]any{"input_tokens": 10, "output_tokens": 2}, 14e-6},
		{"zero", map[string]any{"input_tokens": 0, "output_tokens": 0}, 0},
		{"large", map[string]any{"input_tokens": 1000000, "output_tokens": 10000}, 1.02},
		{"cache", autoCacheRaw(10, 1, 2), 19.25e-6},
		{"search", map[string]any{"input_tokens": 10, "output_tokens": 2, "web_search_calls": 3}, 14e-6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := plan.Charge(tc.raw)
			if err != nil || math.Abs(c.Total-tc.want) > 1e-10 {
				t.Fatalf("实测计价错误: %g want=%g err=%v", c.Total, tc.want, err)
			}
		})
	}
	for _, raw := range []map[string]any{
		nil, {"input_tokens": 1}, {"input_tokens": -1, "output_tokens": 2}, {"input_tokens": 1.5, "output_tokens": 2},
		{"input_tokens": 1, "prompt_tokens": 2, "output_tokens": 2},
		{"input_tokens": 1, "output_tokens": 2, "cache_creation_input_tokens": 3},
	} {
		if _, err := plan.Charge(raw); err == nil {
			t.Fatalf("错误计量未标记未知: %#v", raw)
		}
	}
	bad := strictBudgetDeployment()
	bad.LiteLLMParams["not_json"] = make(chan int)
	if _, err := NewSoftPricePlan(bad, time.Now()).Charge(map[string]any{"input_tokens": 1, "output_tokens": 1}); err == nil {
		t.Fatal("快照损坏未返回错误")
	}
}

// TestSoftBudgetNonTokenPricing 验证软预算不丢失原有非Token费用；纯内存部署，覆盖冻结价格、空用量和缺价，无需清理。
func TestSoftBudgetNonTokenPricing(t *testing.T) {
	dep := strictBudgetDeployment()
	dep.LiteLLMParams = map[string]any{"output_cost_per_second": 0.05}
	dep.ModelInfo["pricing_source"] = "manual"
	if SoftTokenOnly(dep, "openai-chat") {
		t.Fatal("按秒模型不应被当成Token对话")
	}
	plan := NewSoftPricePlan(dep, time.Now(), false)
	dep.LiteLLMParams["output_cost_per_second"] = 99
	charge, err := plan.Charge(map[string]any{"seconds": 8})
	if err != nil || math.Abs(charge.Total-0.4) > 1e-10 {
		t.Fatalf("按秒冻结计价错误: %+v %v", charge, err)
	}
	dep.LiteLLMParams = map[string]any{}
	if _, err := NewSoftPricePlan(dep, time.Now(), false).Charge(map[string]any{"seconds": 8}); err == nil {
		t.Fatal("缺价不能宣称免费")
	}
	if SoftTokenOnly(strictBudgetDeployment(), "openai-image-generation") {
		t.Fatal("图片生成应保留模型计费维度")
	}
	if !SoftTokenOnly(strictBudgetDeployment(), "openai-responses") {
		t.Fatal("普通对话应排除托管工具次数")
	}
	dep.LiteLLMParams = map[string]any{"rates": []any{map[string]any{"measure": "query", "side": "output", "unit_size": 1, "usd": 0.01}}}
	if SoftTokenOnly(dep, "openai-chat") {
		t.Fatal("仅查询费率应保留原计费方式")
	}
}
