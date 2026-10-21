package billing

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
)

// SoftPricePlan 冻结实际出站部署和目录价格，结算时使用实际token，不需要输入窗口或预占金额。
// 内部快照独立于可变模型配置；仅供网关单次尝试使用，不写数据库。
type SoftPricePlan struct {
	dep       config.ModelEntry
	row       map[string]any
	started   time.Time
	err       error
	tokenOnly bool
}

// NewSoftPricePlan 在出站前复制价格源；参数是实际部署、开始时间及可选的仅Token标志（默认true），返回只读计价计划。
// 缺少价格不阻止对话，结算返回明确错误供日志标记未定价，绝不冒充免费。
func NewSoftPricePlan(dep config.ModelEntry, started time.Time, tokenOnly ...bool) SoftPricePlan {
	p := SoftPricePlan{started: started, tokenOnly: true}
	if len(tokenOnly) > 0 {
		p.tokenOnly = tokenOnly[0]
	}
	raw, err := json.Marshal(dep)
	if err == nil {
		err = json.Unmarshal(raw, &p.dep)
	}
	if err == nil {
		raw, err = json.Marshal(budgetCatalogRow(dep))
	}
	if err == nil {
		err = json.Unmarshal(raw, &p.row)
	}
	p.err = err
	return p
}

// Charge 按实际用量和冻结价格计算软预算费用；对话保留缓存TTL、分档、缺失/冲突检查，其他模型保留各维度定价。
// 参数为上游原始usage，返回分项账单或未定价错误；工具次数不收费，不修改请求或存储。
func (p SoftPricePlan) Charge(raw map[string]any) (catalog.Charge, error) {
	if p.err != nil {
		return catalog.Charge{}, p.err
	}
	if !p.tokenOnly {
		u := catalog.NormalizeUsage(raw)
		if c, ok := deploymentCost(p.dep, u, p.started); ok {
			return c, nil
		}
		if _, has := deploymentRates(p.dep.LiteLLMParams); has || p.dep.ModelInfo["pricing_source"] == "manual" {
			return catalog.Charge{}, fmt.Errorf("model pricing is incomplete")
		}
		if rates, ok := catalog.DecodeRates(p.row["rates"]); ok && len(rates) > 0 {
			if c, ok := catalog.CostFromRates(rates, u, p.started); ok {
				return c, nil
			}
		} else if c, ok := catalog.CostFromFlatOrRates(func(field string) (float64, bool) { return floatParam(p.row, field) }, u, p.started); ok {
			return c, nil
		}
		return catalog.Charge{}, fmt.Errorf("model pricing is unavailable")
	}
	u := SearchTokenUsage(raw)
	input := u.PromptTokens + u.CacheWriteTokens
	snapshot := budgetPriceSnapshot{InputImagesIncluded: true, InputBound: input, OutputCap: u.CompletionTokens, Amount: math.MaxFloat64, Window: catalog.WindowAt(p.started), Model: p.dep.ParamString("model", "")}
	tiers, tiered, err := budgetTiers(p.dep, p.row, input, u.CompletionTokens, snapshot.Window, "")
	if err != nil {
		return catalog.Charge{}, err
	}
	if tiered {
		snapshot.Tiers = tiers
		snapshot.Input, snapshot.Output, snapshot.CacheRead, snapshot.CacheWrite = budgetTierMaximum(tiers)
		snapshot.CacheWriteByTTL = budgetMaximumCacheTTL(tiers)
	} else {
		rates, e := budgetRates(p.dep, p.row)
		if e != nil {
			return catalog.Charge{}, e
		}
		prices, e := budgetSelectPrices(rates, snapshot.Window, "")
		if e != nil {
			return catalog.Charge{}, e
		}
		snapshot.Input, snapshot.Output, snapshot.CacheRead, snapshot.CacheWrite = prices.Input, prices.Output, prices.CacheRead, prices.CacheWrite
		snapshot.CacheWriteByTTL = prices.CacheWriteByTTL
	}
	plan := BudgetPlan{frozen: &snapshot, soft: true}
	return plan.Charge(u, raw)
}

// SoftTokenOnly 判断对话是否采用仅Token计费；参数为部署和出站协议，返回是否排除托管工具次数。
// 图片/音视频接口及仅按秒、张、查询收费的部署保留已有计费维度，准入仍统一软预算，不把它们改成免费。
func SoftTokenOnly(dep config.ModelEntry, protocol string) bool {
	switch protocol {
	case "openai-chat", "openai-responses", "anthropic", "anthropic-messages", "gemini", "vertex":
	default:
		return false
	}
	source := budgetPriceSource(dep, budgetCatalogRow(dep))
	if rates, ok := catalog.DecodeRates(source["rates"]); ok && len(rates) > 0 {
		for _, r := range rates {
			if r.Measure == "token" {
				return true
			}
		}
		return false
	}
	_, input := floatParam(source, "input_cost_per_token")
	_, output := floatParam(source, "output_cost_per_token")
	if input || output {
		return true
	}
	for _, field := range []string{"input_cost_per_second", "output_cost_per_second", "input_cost_per_image", "output_cost_per_image", "search_context_cost_per_query"} {
		if _, ok := floatParam(source, field); ok {
			return false
		}
	}
	return true
}
