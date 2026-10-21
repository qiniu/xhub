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
