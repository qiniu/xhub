package billing

import (
	"math"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
)

// TestSoftPricePlanCatalogVersions 验证先后生成的价格计划分别固定自己的目录价格，删除目录不破坏已有计划。
// 前置独占测试目录行；正常/零用量使用各自费率，缺价和非法用量明确失败；Cleanup 删除目录，无外部服务。
func TestSoftPricePlanCatalogVersions(t *testing.T) {
	const name = "unit-price-view-catalog-versions"
	t.Cleanup(func() { catalog.RemoveModel(name) })
	dep := strictBudgetDeployment()
	dep.LiteLLMParams = map[string]any{"model": name}
	dep.ModelInfo["base_model"] = name
	dep.ModelInfo["pricing_source"] = "catalog"
	catalog.SetModel(name, map[string]any{"input_cost_per_token": .001, "output_cost_per_token": .002})
	old := NewSoftPricePlan(dep, time.Now())
	catalog.SetModel(name, map[string]any{"input_cost_per_token": .01, "output_cost_per_token": .02})
	next := NewSoftPricePlan(dep, time.Now())
	catalog.RemoveModel(name)
	missing := NewSoftPricePlan(dep, time.Now())
	for _, tc := range []struct {
		name string
		plan SoftPricePlan
		want float64
	}{{"old", old, .012}, {"new", next, .12}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.plan.Charge(map[string]any{"input_tokens": 8, "output_tokens": 2})
			if err != nil || math.Abs(got.Total-tc.want) > 1e-12 {
				t.Fatalf("目录变更后计划使用错误费率: got=%g want=%g err=%v", got.Total, tc.want, err)
			}
			zero, err := tc.plan.Charge(map[string]any{"input_tokens": 0, "output_tokens": 0})
			if err != nil || zero.Total != 0 {
				t.Fatalf("明确零用量应为零消费: %+v %v", zero, err)
			}
			if _, err := tc.plan.Charge(map[string]any{"input_tokens": -1, "output_tokens": 2}); err == nil {
				t.Fatal("无效用量不能因价格已经固定而伪成功")
			}
		})
	}
	if _, err := missing.Charge(map[string]any{"input_tokens": 8, "output_tokens": 2}); err == nil {
		t.Fatal("目录删除后新计划缺价必须报错，不能复用前一次价格或记为免费")
	}
}
