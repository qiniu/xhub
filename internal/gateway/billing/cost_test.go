package billing

import (
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
)

// TestBudgetAddChargeRetainsRateWindow 验证冻结费率的实际时段进入账单而金额不变；内存数据无需清理。
func TestBudgetAddChargeRetainsRateWindow(t *testing.T) {
	for _, window := range []string{"all", "peak", "offpeak", ""} {
		c := catalog.Charge{}
		budgetAddCharge(&c, catalog.Rate{Measure: "token", Side: "input", Window: window, USD: 0.25}, 2)
		if c.Input != 0.5 || len(c.Applied) != 1 || c.Applied[0].RateWindow != window {
			t.Fatalf("时段 %q 的账单事实不一致：%+v", window, c)
		}
	}
}

// billingPeak 返回固定中国时区高峰时刻；无参数，计价单测调用，无时钟依赖或副作用。
func billingPeak() time.Time {
	return time.Date(2026, 3, 2, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
}

// billingOffpeak 返回固定中国时区低峰时刻；无参数，计价单测调用，无时钟依赖或副作用。
func billingOffpeak() time.Time {
	return time.Date(2026, 3, 2, 20, 0, 0, 0, time.FixedZone("CST", 8*60*60))
}

// TestDeploymentCostUsesWindowsCacheImagesAndSeconds 前置部署自定义价格，验证高低峰、缓存读写、图片与秒数一起结算。参数 t 为测试上下文；所有输入在内存中，无数据清理。
func TestDeploymentCostUsesWindowsCacheImagesAndSeconds(t *testing.T) {
	dep := config.ModelEntry{LiteLLMParams: map[string]any{
		"model":                           "upstream",
		"input_cost_per_token":            1.0,
		"output_cost_per_token":           2.0,
		"input_cost_per_token_peak":       3.0,
		"output_cost_per_token_peak":      4.0,
		"cache_read_input_token_cost":     0.25,
		"cache_creation_input_token_cost": 0.5,
		"output_cost_per_image":           5.0,
		"output_cost_per_second":          6.0,
	}}
	usage := catalog.Usage{PromptTokens: 10, CompletionTokens: 2, CachedTokens: 2, CacheWriteTokens: 1, Images: 1, Seconds: 2}
	off, ok := deploymentCost(dep, usage, billingOffpeak())
	if !ok || off.Total != 8+0.5+0.5+4+5+12 {
		t.Fatalf("offpeak charge %#v ok=%v", off, ok)
	}
	peak, ok := deploymentCost(dep, usage, billingPeak())
	if !ok || peak.Total != 24+0.5+0.5+8+5+12 {
		t.Fatalf("peak charge %#v ok=%v", peak, ok)
	}
}

// TestCallCostUsesCatalogBaseModelAndManualDoesNotFallThrough 前置隔离目录价格，验证 base_model 定价和手工价格缺失时禁止回落。参数 t 为测试上下文；Cleanup 删除临时目录项。
func TestCallCostUsesCatalogBaseModelAndManualDoesNotFallThrough(t *testing.T) {
	const catalogID = "call-cost-base-model"
	catalog.SetModel(catalogID, map[string]any{
		"rates": []catalog.Rate{{Measure: "second", UnitSize: 1, Side: "output", Window: "all", USD: 2}},
	})
	t.Cleanup(func() { catalog.RemoveModel(catalogID) })

	dep := config.ModelEntry{
		ModelName:     "public",
		LiteLLMParams: map[string]any{"model": "upstream"},
		ModelInfo:     map[string]any{"pricing_source": "catalog", "base_model": catalogID},
	}
	s := &testFinder{dep: dep}
	total, _, output, ok, charge := CallCost(s, "public", "|upstream", catalog.Usage{Seconds: 3}, billingPeak())
	if !ok || total != 6 || output != 6 || len(charge.Applied) != 1 {
		t.Fatalf("catalog base model total=%v output=%v ok=%v charge=%#v", total, output, ok, charge)
	}

	dep.ModelInfo["pricing_source"] = "manual"
	dep.LiteLLMParams["input_cost_per_token"] = 1.0
	s.dep = dep
	if total, _, _, ok, _ := CallCost(s, "public", "|upstream", catalog.Usage{Seconds: 3}, billingPeak()); ok || total != 0 {
		t.Fatalf("manual deployment fell through to catalog total=%v ok=%v", total, ok)
	}
}

// TestMeasuredVideoBandDoesNotFallThroughExplicitRates 前置缺少匹配视频档位的显式费率，验证缺价不会回落到平价或目录。参数 t 为测试上下文；无外部数据。
func TestMeasuredVideoBandDoesNotFallThroughExplicitRates(t *testing.T) {
	dep := config.ModelEntry{ModelName: "video", LiteLLMParams: map[string]any{
		"model":                 "qiniu/bytedance/doubao-seedance-2-0-260128",
		"rates":                 []catalog.Rate{{Measure: "token", Side: "output", Variant: "wiv", Window: "all", USD: 1e-6}},
		"output_cost_per_token": 99.0,
	}}
	s := &testFinder{dep: dep}
	u := catalog.Usage{CompletionTokens: 1000, OutputVariant: "woiv"}
	if _, ok := deploymentCost(dep, u, billingPeak()); ok {
		t.Fatal("incomplete table fell back to flat price")
	}
	if _, _, _, ok, _ := CallCost(s, "video", "|qiniu/bytedance/doubao-seedance-2-0-260128", u, billingPeak()); ok {
		t.Fatal("incomplete explicit rates fell through to catalog")
	}
}

// TestCallCostKeepsOriginalTaskModelAfterDeploymentEdit 前置任务创建型号与已编辑部署，验证任务沿用创建型号计价。参数 t 为测试上下文；Cleanup 删除两个临时目录项。
func TestCallCostKeepsOriginalTaskModelAfterDeploymentEdit(t *testing.T) {
	const original = "seedance-original-task-model"
	const changed = "seedance-edited-deployment-model"
	catalog.SetModel(original, map[string]any{"rates": []catalog.Rate{{Measure: "token", Side: "output", Variant: "woiv", Window: "all", USD: 0.001}}})
	catalog.SetModel(changed, map[string]any{"rates": []catalog.Rate{{Measure: "token", Side: "output", Variant: "woiv", Window: "all", USD: 0.009}}})
	t.Cleanup(func() { catalog.RemoveModel(original); catalog.RemoveModel(changed) })
	dep := config.ModelEntry{ModelName: "task", LiteLLMParams: map[string]any{"model": changed}}
	s := &testFinder{dep: dep}
	total, _, _, ok, _ := CallCost(s, "task", "|"+changed, catalog.Usage{CompletionTokens: 1000, OutputVariant: "woiv", PricingModel: original}, billingPeak())
	if !ok || total != 1 {
		t.Fatalf("task billed as edited deployment: %v %v", total, ok)
	}
}

// testFinder 提供单部署测试配置，不连接存储或路由器。
type testFinder struct{ dep config.ModelEntry }

// FindDeployment 按生产部署 ID 查找测试配置；返回部署与是否匹配，无副作用。
func (s *testFinder) FindDeployment(id string) (config.ModelEntry, bool) {
	return s.dep, id == s.dep.ParamString("api_base", "")+"|"+s.dep.ParamString("model", "")
}
