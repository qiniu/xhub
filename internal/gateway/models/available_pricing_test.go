package models

import (
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	"testing"
)

// TestAvailableCardBoundPrices 验证公开别名、目录绑定和手工覆盖一致；缺失或非法价格不伪装为免费。
// 前置独立目录项；所有输入在内存，Cleanup 删除目录，覆盖正常、零值和失败输入。
func TestAvailableCardBoundPrices(t *testing.T) {
	const id = "test-bound-card-price"
	catalog.SetModel(id, map[string]any{"input_cost_per_token": 0.000004, "output_cost_per_token": 0.00002})
	t.Cleanup(func() { catalog.RemoveModel(id) })
	for _, tc := range []struct {
		name, source, base string
		params             map[string]any
		want               any
	}{
		{"bound", "catalog", id, nil, float64(4)},
		{"manual", "manual", id, map[string]any{"input_cost_per_token": 0.000003}, float64(3)},
		{"zero", "manual", id, map[string]any{"input_cost_per_token": float64(0)}, float64(0)},
		{"missing", "catalog", "not-a-real-price", nil, nil},
		{"manual-missing", "manual", id, nil, nil},
		{"invalid", "manual", id, map[string]any{"input_cost_per_token": "invalid"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := availableCard(config.ModelEntry{ModelName: "renamed-public", LiteLLMParams: tc.params, ModelInfo: map[string]any{"pricing_source": tc.source, "base_model": tc.base}})
			if card["input_price"] != tc.want {
				t.Fatalf("价格展示=%v，预期=%v", card["input_price"], tc.want)
			}
		})
	}
}
