package billing

import (
	"encoding/json"
	"math"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
)

// SoftPricePlan 冻结实际出站部署和目录价格，结算时使用实际token，不需要输入窗口或预占金额。
// 内部快照独立于可变模型配置；仅供网关单次尝试使用，不写数据库。
type SoftPricePlan struct {
	dep     config.ModelEntry
	row     map[string]any
	started time.Time
	err     error
}

// NewSoftPricePlan 在出站前复制价格源；参数是实际部署及开始时间，返回只读计价计划。
// 缺少价格不阻止对话，结算返回明确错误供日志标记未定价，绝不冒充免费。
func NewSoftPricePlan(dep config.ModelEntry, started time.Time) SoftPricePlan {
	p := SoftPricePlan{started: started}
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

// Charge 按实测token和冻结价格计算软预算费用；保留缓存TTL、分档、缺失/冲突用量检查。
// 参数为上游原始usage，返回分项账单或未定价错误；工具次数不收费，不修改请求或存储。
func (p SoftPricePlan) Charge(raw map[string]any) (catalog.Charge, error) {
	if p.err != nil {
		return catalog.Charge{}, p.err
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
