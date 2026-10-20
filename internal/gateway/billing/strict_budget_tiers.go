package billing

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
)

// budgetTierPrices 保存一个上下文档位在请求开始时刻的完整文字费率；所有值已归一为每Token美元。
type budgetTierPrices struct {
	Input           catalog.Rate            `json:"input_rate"`
	Output          catalog.Rate            `json:"output_rate"`
	CacheRead       *catalog.Rate           `json:"cache_read_rate,omitempty"`
	CacheWrite      *catalog.Rate           `json:"cache_write_rate,omitempty"`
	CacheWriteByTTL map[string]catalog.Rate `json:"cache_write_rates_by_ttl,omitempty"`
}

// budgetPriceTier 原样保留目录输入/输出区间；来源未声明开闭，共享边界不能猜测采用较高或较低档。
type budgetPriceTier struct {
	InputRange  [2]int `json:"input_range"`
	OutputRange [2]int `json:"output_range"`
	budgetTierPrices
}

// budgetSelectPrices 在已校验费率中选择请求时段及缓存期限；参数为费率、时段和TTL，返回独立价格或缺失错误。
// 单档和多档共用，不根据是否命中缓存猜价格；未声明读取价只有实际读缓存时才拒绝结算。
func budgetSelectPrices(rates []catalog.Rate, window, ttl string) (budgetTierPrices, error) {
	var p budgetTierPrices
	var ok bool
	p.Input, ok = budgetRate(rates, "input", "uncached", window)
	if !ok {
		return p, budgetFailure("unsupported_budget", "strict budget requires an explicit input token rate")
	}
	p.Output, ok = budgetRate(rates, "output", "", window)
	if !ok {
		return p, budgetFailure("unsupported_budget", "strict budget requires an explicit output token rate")
	}
	if r, found := budgetRate(rates, "input", "cached", window); found && r.Variant == "cached" {
		r.Side = "cache_read"
		p.CacheRead = &r
	} else if r, found := budgetRate(rates, "cache_read", "", window); found {
		p.CacheRead = &r
	}
	// 客户端声明只约束其缓存块；透明代理还可能注入另一期限的系统前缀缓存。
	// 因此新计划始终冻结全部已知写价并取最大值预占，不能把请求TTL当成全部实测维度的上界。
	for _, variant := range []string{"", "1h"} {
		if r, found := budgetRate(rates, "cache_write", variant, window); found {
			if p.CacheWriteByTTL == nil {
				p.CacheWriteByTTL = map[string]catalog.Rate{}
			}
			p.CacheWriteByTTL[variant] = r
			if p.CacheWrite == nil || r.USD > p.CacheWrite.USD {
				cp := r
				p.CacheWrite = &cp
			}
		}
	}
	if ttl != "" {
		variant := ""
		if ttl == "1h" {
			variant = "1h"
		}
		if _, found := p.CacheWriteByTTL[variant]; !found {
			return p, budgetFailure("unsupported_budget", "strict budget requires a cache write rate for requested caching")
		}
	}
	return p, nil
}

// budgetTiers 从实际价格源冻结完整分档表；参数为部署、目录、声明窗口/输出cap和时段/TTL。
// 返回可达档、是否有原始分档、错误；手工价格仍优先。来源有rules时绝不退回仅由首档派生的flat/rates。
func budgetTiers(dep config.ModelEntry, row map[string]any, input, output int, window, ttl string) ([]budgetPriceTier, bool, error) {
	source := budgetPriceSource(dep, row)
	raw, exists := source["pricing_rules_v2"]
	if !exists || raw == nil {
		return nil, false, nil
	}
	fail := func(message string) ([]budgetPriceTier, bool, error) {
		return nil, true, budgetFailure("unsupported_budget", message)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return fail("strict budget pricing rules cannot be encoded")
	}
	var rules []map[string]any
	if json.Unmarshal(encoded, &rules) != nil || len(rules) == 0 || len(rules) > 64 {
		return fail("strict budget requires one to 64 explicit pricing tiers")
	}
	tiers := make([]budgetPriceTier, 0, len(rules))
	for _, rule := range rules {
		for key, value := range rule {
			switch key {
			case "input_range", "output_range", "details_v2":
			case "item_order":
				// 展示顺序没有计价语义，但只允许纯字符串数组，避免把未知条件混入此白名单。
				items, ok := value.([]any)
				if !ok {
					return fail("catalog item order must be a string array")
				}
				for _, item := range items {
					if _, ok := item.(string); !ok {
						return fail("catalog item order must be a string array")
					}
				}
			case "details":
				// 旧details仅可为空；并存另一套有效价格可能有不同币种或条件，不能默默忽略。
				if value != nil {
					m, ok := value.(map[string]any)
					if !ok || len(m) > 0 {
						return fail("legacy pricing details cannot override explicit USD tiers")
					}
				}
			case "condition", "input_item_type", "output_item_type":
				if value != nil && value != "" {
					return fail("conditional catalog pricing is unsupported for strict budgets")
				}
			default:
				if value != nil {
					return fail("unknown catalog tier metadata is unsupported for strict budgets")
				}
			}
		}
		in, err := budgetTierRange(rule["input_range"])
		if err != nil {
			return nil, true, err
		}
		out, err := budgetTierRange(rule["output_range"])
		if err != nil {
			return nil, true, err
		}
		rates, err := budgetTierRates(rule["details_v2"])
		if err != nil {
			return nil, true, err
		}
		prices, err := budgetSelectPrices(rates, window, ttl)
		if err != nil {
			return nil, true, err
		}
		tiers = append(tiers, budgetPriceTier{InputRange: in, OutputRange: out, budgetTierPrices: prices})
	}
	if err := budgetTierCoverage(tiers, input, output); err != nil {
		return nil, true, err
	}
	reachable := make([]budgetPriceTier, 0, len(tiers))
	for _, tier := range tiers {
		if tier.InputRange[0] <= input && tier.OutputRange[0] <= output {
			reachable = append(reachable, tier)
		}
	}
	return reachable, true, nil
}

// budgetTierRange 读取原始整数上下界；参数必须为两个非负有限整数且上界更大，返回闭区间候选或错误。
// 这里只保留原始边界，不擅自解释来源未声明的开闭语义。
func budgetTierRange(raw any) ([2]int, error) {
	var result [2]int
	values, ok := raw.([]any)
	if !ok || len(values) != 2 {
		return result, budgetFailure("unsupported_budget", "catalog tier range requires two explicit bounds")
	}
	for i, value := range values {
		n, ok := budgetUsageInteger(value)
		if !ok {
			return result, budgetFailure("unsupported_budget", "catalog tier bounds must be finite nonnegative integers")
		}
		result[i] = n
	}
	if result[0] >= result[1] {
		return result, budgetFailure("unsupported_budget", "catalog tier range must have positive width")
	}
	return result, nil
}

// budgetTierRates 严格转换单档details_v2为每Token USD费率；参数必须有明确单位大小和美元价格。
// CNY展示价格不作为USD后备，缺失价格不能视为零；只允许已知文字、缓存、批处理与搜索报价键。
// 请求层已拒绝服务器搜索/批处理，因此这两种独立维度不参与文字预占。
func budgetTierRates(raw any) ([]catalog.Rate, error) {
	details, ok := raw.(map[string]any)
	if !ok || len(details) == 0 {
		return nil, budgetFailure("unsupported_budget", "catalog tier requires explicit USD prices")
	}
	rates := []catalog.Rate{}
	seen := map[string]bool{}
	for key, value := range details {
		unit, ok := value.(map[string]any)
		if !ok {
			return nil, budgetFailure("unsupported_budget", "catalog tier price must be an object")
		}
		for field := range unit {
			switch field {
			case "name", "unit_name", "unit_size", "unit_price", "unit_price_usd":
			default:
				return nil, budgetFailure("unsupported_budget", "catalog tier price has unsupported conditions")
			}
		}
		usd, usdOK := floatParam(unit, "unit_price_usd")
		size, sizeOK := floatParam(unit, "unit_size")
		if !usdOK || !sizeOK || math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 || math.IsNaN(size) || math.IsInf(size, 0) || size <= 0 || size != math.Trunc(size) {
			return nil, budgetFailure("unsupported_budget", "catalog tier requires explicit finite USD price and positive unit size")
		}
		base, window := key, "all"
		for _, w := range []string{"peak", "offpeak"} {
			if strings.HasSuffix(base, "_"+w) {
				base = strings.TrimSuffix(base, "_"+w)
				window = w
				break
			}
		}
		r := catalog.Rate{Measure: "token", UnitSize: size, USD: usd / size, Window: window, SourceKey: key}
		switch base {
		case "input", "ncache":
			r.Side = "input"
			r.Variant = "uncached"
		case "output":
			r.Side = "output"
		case "cache":
			r.Side = "cache_read"
		case "c_cache":
			r.Side = "cache_write"
		case "c_1h_cache":
			r.Side = "cache_write"
			r.Variant = "1h"
		case "bi_input":
			r.Side = "batch_input"
		case "bi_output":
			r.Side = "batch_output"
		case "web_search_req":
			if unit["unit_name"] != "query" && unit["unit_name"] != "request" && unit["unit_name"] != "time" {
				return nil, budgetFailure("unsupported_budget", "catalog search price has an unsupported unit")
			}
			continue
		default:
			return nil, budgetFailure("unsupported_budget", "catalog tier contains an unsupported price dimension")
		}
		if unit["unit_name"] != "token" || math.IsNaN(r.USD) || math.IsInf(r.USD, 0) || (usd > 0 && r.USD == 0) {
			return nil, budgetFailure("unsupported_budget", "catalog tier token unit or normalized USD price is invalid")
		}
		identity := r.Side + "/" + r.Variant + "/" + r.Window
		if seen[identity] {
			return nil, budgetFailure("unsupported_budget", "catalog tier contains ambiguous duplicate prices")
		}
		seen[identity] = true
		rates = append(rates, r)
	}
	return rates, nil
}

// budgetTierCoverage 检查声明输入/输出矩形被档位完整覆盖；允许共边界，禁止内部重叠及任意间隙。
// 参数为已解析档和上界，返回错误。以所有边界及相邻中点验证覆盖，不依赖请求文本或启发式计数。
func budgetTierCoverage(tiers []budgetPriceTier, input, output int) error {
	for i, a := range tiers {
		for _, b := range tiers[i+1:] {
			if max(a.InputRange[0], b.InputRange[0]) < min(a.InputRange[1], b.InputRange[1]) && max(a.OutputRange[0], b.OutputRange[0]) < min(a.OutputRange[1], b.OutputRange[1]) {
				return budgetFailure("unsupported_budget", "catalog pricing tier interiors overlap")
			}
		}
	}
	samples := func(bound int, inputAxis bool) []float64 {
		points := []int{0, bound}
		for _, tier := range tiers {
			r := tier.OutputRange
			if inputAxis {
				r = tier.InputRange
			}
			for _, n := range r {
				if n > 0 && n < bound {
					points = append(points, n)
				}
			}
		}
		sort.Ints(points)
		values := []float64{}
		for i, n := range points {
			if i > 0 && points[i-1] == n {
				continue
			}
			if len(values) > 0 {
				values = append(values, (values[len(values)-1]+float64(n))/2)
			}
			values = append(values, float64(n))
		}
		return values
	}
	for _, in := range samples(input, true) {
		for _, out := range samples(output, false) {
			covered := false
			for _, tier := range tiers {
				if in >= float64(tier.InputRange[0]) && in <= float64(tier.InputRange[1]) && out >= float64(tier.OutputRange[0]) && out <= float64(tier.OutputRange[1]) {
					covered = true
					break
				}
			}
			if !covered {
				return budgetFailure("unsupported_budget", "catalog pricing tiers do not cover the declared input and output bounds")
			}
		}
	}
	return nil
}

// budgetTierMaximum 取得所有可达档各收费维度最坏值；参数为非空冻结档，返回保守预占费率，不用于实际结算。
func budgetTierMaximum(tiers []budgetPriceTier) (catalog.Rate, catalog.Rate, *catalog.Rate, *catalog.Rate) {
	p := tiers[0].budgetTierPrices
	for _, tier := range tiers[1:] {
		if tier.Input.USD > p.Input.USD {
			p.Input = tier.Input
		}
		if tier.Output.USD > p.Output.USD {
			p.Output = tier.Output
		}
		if tier.CacheRead != nil && (p.CacheRead == nil || tier.CacheRead.USD > p.CacheRead.USD) {
			p.CacheRead = tier.CacheRead
		}
		if tier.CacheWrite != nil && (p.CacheWrite == nil || tier.CacheWrite.USD > p.CacheWrite.USD) {
			p.CacheWrite = tier.CacheWrite
		}
	}
	return p.Input, p.Output, p.CacheRead, p.CacheWrite
}

// budgetSelectTier 根据完整实测输入/输出从冻结表选择费率；输入总量包括独立缓存读与写，避免缓存绕过长上下文档。
// 来源未说明共享阈值的开闭，因此阈值两侧费率不同必须保留未知占用；相同收费契约可安全结算。
func budgetSelectTier(tiers []budgetPriceTier, u catalog.Usage) (budgetTierPrices, error) {
	input := int64(u.PromptTokens) + int64(u.CacheWriteTokens)
	var selected *budgetTierPrices
	for _, tier := range tiers {
		if input < int64(tier.InputRange[0]) || input > int64(tier.InputRange[1]) || u.CompletionTokens < tier.OutputRange[0] || u.CompletionTokens > tier.OutputRange[1] {
			continue
		}
		if selected != nil && !budgetSamePrices(*selected, tier.budgetTierPrices) {
			return budgetTierPrices{}, budgetFailure("invalid_usage", "measured usage lies on an ambiguous catalog pricing boundary")
		}
		p := tier.budgetTierPrices
		selected = &p
	}
	if selected == nil {
		return budgetTierPrices{}, budgetFailure("invalid_usage", "measured usage has no frozen pricing tier")
	}
	return *selected, nil
}

// budgetSamePrices 比较相邻边界完整收费契约；参数为两组每Token价格，返回相等标志，不比较展示标签和来源键。
func budgetSamePrices(a, b budgetTierPrices) bool {
	equal := func(a, b *catalog.Rate) bool {
		if a == nil || b == nil {
			return a == nil && b == nil
		}
		return a.USD == b.USD && a.Variant == b.Variant
	}
	if len(a.CacheWriteByTTL) != len(b.CacheWriteByTTL) {
		return false
	}
	for ttl, rate := range a.CacheWriteByTTL {
		other, exists := b.CacheWriteByTTL[ttl]
		if !exists || rate.USD != other.USD || rate.Variant != other.Variant {
			return false
		}
	}
	return a.Input.USD == b.Input.USD && a.Output.USD == b.Output.USD && equal(a.CacheRead, b.CacheRead) && equal(a.CacheWrite, b.CacheWrite)
}

// budgetPriceSource 统一选择实际费率源；显式manual始终优先，其他模式只有非null价格覆盖才切换手工来源。
// 参数为部署和目录，返回只读源；控制台切回catalog常以null清除旧价格，不能把这个常规清除操作误当不完整手工报价。
// 非null非法值仍交给严格验证拒绝，不能借此退回目录掩盖配置错误。
func budgetPriceSource(dep config.ModelEntry, row map[string]any) map[string]any {
	if dep.ModelInfo["pricing_source"] == "manual" {
		return dep.LiteLLMParams
	}
	for key, value := range dep.LiteLLMParams {
		if value != nil && (strings.Contains(key, "cost_per_") || strings.Contains(key, "_token_cost") || key == "rates" || key == "pricing_rules_v2") {
			return dep.LiteLLMParams
		}
	}
	return row
}
