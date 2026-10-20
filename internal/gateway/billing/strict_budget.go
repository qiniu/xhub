package billing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// DefaultBudgetOutputTokens 是有限预算请求未声明输出上限时公开采用的上限；推理和可见输出共用该额度。
const DefaultBudgetOutputTokens = 4096

// BudgetPlanError 区分调用参数错误、缺少可证明的计价能力和上游用量违约；执行器据 Code 映射安全HTTP错误。
// Message 不包含供应商凭据或请求内容；未知费用不能作为零费用放行。
type BudgetPlanError struct {
	Code    string
	Message string
}

// Error 返回可用于诊断的固定原因；参数为错误接收者，无外部副作用。
func (e *BudgetPlanError) Error() string { return e.Message }

// BudgetPlan 保存单次实际出站尝试的严格预占量和冻结价格；公开字段供执行器响应头、IAM持久化和日志使用。
// 输入按可信模型完整上下文预占，不采用启发式分词。调用方负责原子占用所有预算层级并管理重试/未知结果。
// 未导出快照供 Charge 使用；公开字段改变不会改变实际校验或收费依据。
type BudgetPlan struct {
	InputBound    int
	OutputCap     int
	Amount        float64
	Defaulted     bool
	PriceSnapshot string
	SearchCalls   int
	frozen        *budgetPriceSnapshot
}

// budgetPriceSnapshot 是无需重新查询可变目录的定价契约；金额和所有费率都只包含值，不引用部署配置。
type budgetPriceSnapshot struct {
	Search             *budgetSearch     `json:"search,omitempty"`
	InputBound         int               `json:"input_bound"`
	OutputCap          int               `json:"output_cap"`
	Amount             float64           `json:"reserved_amount"`
	Protocol           string            `json:"protocol"`
	Window             string            `json:"window"`
	Model              string            `json:"model"`
	StartedAt          time.Time         `json:"started_at"`
	Input              catalog.Rate      `json:"input_rate"`
	Output             catalog.Rate      `json:"output_rate"`
	CacheRead          *catalog.Rate     `json:"cache_read_rate,omitempty"`
	CacheWrite         *catalog.Rate     `json:"cache_write_rate,omitempty"`
	Tiers              []budgetPriceTier `json:"tiers,omitempty"`
	IntervalSemantics  string            `json:"interval_semantics,omitempty"`
	CapSemanticsSource string            `json:"cap_semantics_source,omitempty"`
}

// budgetFailure 构造不包含输入正文的分类错误；参数为固定代码和原因，返回错误，严格计划和结算共用。
func budgetFailure(code, message string) error { return &BudgetPlanError{Code: code, Message: message} }

// StrictBudgetPlan 校验最终JSON、设置真实输出cap并冻结单次尝试的费用上界。
// 参数必须是已恢复历史、经护栏和协议转换后的实际部署/正文/协议与原请求开始时间；返回计划、新正文或分类错误。
// 支持可信文字上界及显式认证的 Responses 搜索契约；其他服务器工具、多模态、未知定价和远程历史引用拒绝。
// Chat 的cap字段须由可信 supported_parameters 声明，不从模型名称猜测；不修改传入部署或字节，不访问供应商。
func StrictBudgetPlan(dep config.ModelEntry, finalBody []byte, protocol string, started time.Time) (BudgetPlan, []byte, error) {
	fail := func(err error) (BudgetPlan, []byte, error) { return BudgetPlan{}, nil, err }
	decoder := json.NewDecoder(bytes.NewReader(finalBody))
	decoder.UseNumber()
	var body map[string]any
	if decoder.Decode(&body) != nil || body == nil {
		return fail(budgetFailure("invalid_request", "budgeted request must be a JSON object"))
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fail(budgetFailure("invalid_request", "budgeted request has trailing JSON"))
	}
	if protocol != "openai-chat" && protocol != "openai-responses" && protocol != "anthropic-messages" {
		return fail(budgetFailure("unsupported_budget", "strict budget requires a supported text dialogue protocol"))
	}
	search, err := budgetPrepareSearch(dep, protocol, body)
	if err != nil {
		return fail(err)
	}
	cacheTTL, err := budgetRequest(body, protocol, search != nil)
	if err != nil {
		return fail(err)
	}
	row := budgetCatalogRow(dep)
	input, err := budgetInputBound(dep, row)
	if err != nil {
		return fail(err)
	}
	cap, defaulted, err := budgetOutputCap(body, protocol, dep, row)
	if err != nil {
		return fail(err)
	}
	if search != nil {
		if search.InputBound < input {
			return fail(budgetFailure("unsupported_budget", "search cumulative input bound must cover the model input window"))
		}
		input = search.InputBound
	}
	window := catalog.WindowAt(started)
	snapshot := budgetPriceSnapshot{Search: search, InputBound: input, OutputCap: cap, Protocol: protocol, Window: window, Model: dep.ParamString("model", ""), StartedAt: started}
	if protocol == "openai-chat" && body["max_tokens"] != nil && budgetTrustedLegacyCap(dep) {
		snapshot.CapSemanticsSource = strings.TrimSpace(dep.ModelInfo["max_tokens_semantics_source"].(string))
	}
	tiers, tiered, err := budgetTiers(dep, row, input, cap, window, cacheTTL)
	if err != nil {
		return fail(err)
	}
	if tiered {
		if search != nil {
			return fail(budgetFailure("unsupported_budget", "web search requires flat token pricing"))
		}
		snapshot.Tiers = tiers
		snapshot.IntervalSemantics = "shared_boundaries_require_equal_rates"
		// 原始目录不声明开闭边界；预占覆盖所有可达档，最终共享边界只有费率一致才可结算。
		snapshot.Input, snapshot.Output, snapshot.CacheRead, snapshot.CacheWrite = budgetTierMaximum(tiers)
	} else {
		rates, err := budgetRates(dep, row)
		if err != nil {
			return fail(err)
		}
		prices, err := budgetSelectPrices(rates, window, cacheTTL)
		if err != nil {
			return fail(err)
		}
		snapshot.Input, snapshot.Output, snapshot.CacheRead, snapshot.CacheWrite = prices.Input, prices.Output, prices.CacheRead, prices.CacheWrite
	}
	maxInput := snapshot.Input.USD
	if snapshot.CacheRead != nil {
		maxInput = math.Max(maxInput, snapshot.CacheRead.USD)
	}
	amount := float64(input)*maxInput + float64(cap)*snapshot.Output.USD
	if snapshot.CacheWrite != nil {
		amount += float64(input) * snapshot.CacheWrite.USD
	}
	if search != nil {
		amount += float64(search.MaxCalls) * search.Rate.USD
	}
	snapshot.Amount, err = budgetRoundUp(amount)
	if err != nil {
		return fail(err)
	}
	rawSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		return fail(budgetFailure("unsupported_budget", "strict budget snapshot cannot be encoded"))
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fail(budgetFailure("invalid_request", "budgeted request cannot be encoded"))
	}
	searchCalls := 0
	if search != nil {
		searchCalls = search.MaxCalls
	}
	return BudgetPlan{SearchCalls: searchCalls, InputBound: input, OutputCap: cap, Amount: snapshot.Amount, Defaulted: defaulted, PriceSnapshot: string(rawSnapshot), frozen: &snapshot}, encoded, nil
}

// Charge 用冻结费率核对并计算本次已完成的实测账单；参数为归一化Usage，返回可直接结算的分项或违约错误。
// 调用方须先用 NormalizeDialogueUsage 标记缺失实测量，再转为 catalog.Usage；本结构的零值无法表达字段缺失。
// 错误时不得退款或改用当前目录补算；本函数不执行扣费、不更改占用。上游超过声明上界也不能穿透硬预算。
func (p BudgetPlan) Charge(u catalog.Usage) (catalog.Charge, error) {
	s := p.frozen
	fail := func(message string) (catalog.Charge, error) {
		return catalog.Charge{}, budgetFailure("invalid_usage", message)
	}
	if s == nil {
		return fail("strict budget plan is uninitialized")
	}
	if len(s.Tiers) > 0 {
		if int64(u.PromptTokens)+int64(u.CacheWriteTokens) > int64(s.InputBound) {
			return fail("measured total input including cache writes exceeds the reserved bound")
		}
		prices, err := budgetSelectTier(s.Tiers, u)
		if err != nil {
			return fail(err.Error())
		}
		// 临时副本仅替换本次实际档位，绝不修改持久快照或让目录热更新穿透冻结契约。
		selected := *s
		selected.Input, selected.Output, selected.CacheRead, selected.CacheWrite = prices.Input, prices.Output, prices.CacheRead, prices.CacheWrite
		s = &selected
	}
	if u.PricingBlocked != "" {
		return fail("strict budget requires complete measured usage")
	}
	if u.PromptTokens < 0 || u.PromptTokens > s.InputBound || u.CompletionTokens < 0 || u.CompletionTokens > s.OutputCap || u.CachedTokens < 0 || u.CachedTokens > u.PromptTokens || u.CacheWriteTokens < 0 || u.CacheWriteTokens > s.InputBound {
		return fail("measured token usage exceeds the reserved bounds")
	}
	if u.InputImageTokens != 0 || u.InputImages != 0 || u.Images != 0 || u.InputSeconds != 0 || u.Seconds != 0 || u.Searches < 0 || (u.Searches != 0 && (s.Search == nil || u.Searches > s.Search.MaxCalls)) || u.ImageVariant != "" || u.OutputVariant != "" || (u.PricingModel != "" && u.PricingModel != s.Model) {
		return fail("measured usage contains an unreserved pricing dimension")
	}
	if u.InputTextTokens < 0 || u.InputTextTokens > u.PromptTokens {
		return fail("measured input token details contradict the total")
	}
	if u.CacheWriteTokens > 0 && s.CacheWrite == nil {
		return fail("measured cache writes have no reserved price")
	}
	c := catalog.Charge{Window: s.Window}
	cached := u.CachedTokens
	if cached > 0 && s.CacheRead == nil {
		// 预占可以按普通输入上界，但缺少命中费率不能冒充精确实测账单。
		return fail("measured cache reads have no reserved price")
	}
	budgetAddCharge(&c, s.Input, u.PromptTokens-cached)
	budgetAddCharge(&c, s.Output, u.CompletionTokens)
	if cached > 0 {
		budgetAddCharge(&c, *s.CacheRead, cached)
	}
	if u.CacheWriteTokens > 0 {
		budgetAddCharge(&c, *s.CacheWrite, u.CacheWriteTokens)
	}
	if s.Search != nil {
		budgetAddCharge(&c, s.Search.Rate, u.Searches)
	}
	c.Total = c.Input + c.Output + c.Cache
	if math.IsNaN(c.Total) || math.IsInf(c.Total, 0) || c.Total > s.Amount {
		return fail("measured cost exceeds the reserved amount")
	}
	return c, nil
}

// ValidateStrictBudgetUsage 在目录归一化之前验证原始实测数值，防止小数、字符串和缺失事实被转成较小整数。
// 参数为原始usage及可选冻结计划，返回违约错误；budgetCharge应传计划以校验缓存TTL，随后调用Normalize和Charge。
// 本函数只读输入；零值是合法实测，缺少必要输入/输出量、混合TTL和未预留的多模态/服务器工具计量均拒绝。
func ValidateStrictBudgetUsage(raw map[string]any, plans ...BudgetPlan) error {
	fail := func(message string) error { return budgetFailure("invalid_usage", message) }
	if raw == nil || raw["pricing_blocked"] != nil && raw["pricing_blocked"] != "" {
		return fail("strict budget requires complete measured usage")
	}
	// 原生同步旁路尚未归一化，必须与流入口使用同一缓存别名校验，不能按优先级掩盖矛盾后退款。
	if llmdialogue.DialogueUsageProblem(raw) != "" {
		return fail("measured usage aliases or counts are inconsistent")
	}
	counts := map[string]int{}
	for _, group := range [][]string{{"prompt_tokens", "input_tokens"}, {"completion_tokens", "output_tokens"}} {
		seen, first := false, 0
		for _, key := range group {
			if value, exists := raw[key]; exists {
				n, ok := budgetUsageInteger(value)
				if !ok || seen && first != n {
					return fail("measured token totals must be consistent nonnegative integers")
				}
				seen, first = true, n
			}
		}
		if !seen {
			return fail("strict budget requires complete measured usage")
		}
		counts[group[0]] = first
	}
	var search *budgetSearch
	if len(plans) > 0 && plans[0].frozen != nil {
		search = plans[0].frozen.Search
	}
	if err := budgetSearchUsage(raw, search); err != nil {
		return err
	}
	if err := budgetUsageDimensions(raw, search != nil); err != nil {
		return err
	}
	// 推理属于输出总量的子集，不能把明显遗漏隐藏推理的上游账单当成完整事实。
	for _, key := range []string{"completion_tokens_details", "output_tokens_details"} {
		if details, ok := raw[key].(map[string]any); ok {
			if value, exists := details["reasoning_tokens"]; exists {
				n, _ := budgetUsageInteger(value)
				if n > counts["completion_tokens"] {
					return fail("measured reasoning tokens exceed the output total")
				}
			}
		}
	}
	if value := raw["cache_creation"]; value != nil {
		details, ok := value.(map[string]any)
		if !ok {
			return fail("measured cache creation details must be an object")
		}
		five, hour := 0, 0
		for key, value := range details {
			n, ok := budgetUsageInteger(value)
			if !ok {
				return fail("measured cache creation counts must be nonnegative integers")
			}
			switch key {
			case "ephemeral_5m_input_tokens":
				five = n
			case "ephemeral_1h_input_tokens":
				hour = n
			default:
				if n != 0 {
					return fail("measured cache creation TTL is unsupported")
				}
			}
		}
		if five > 0 && hour > 0 {
			return fail("mixed cache creation TTLs require separate prices")
		}
		if five+hour > 0 {
			var plan *budgetPriceSnapshot
			if len(plans) > 0 {
				plan = plans[0].frozen
			}
			if plan == nil || plan.CacheWrite == nil || hour > 0 && plan.CacheWrite.Variant != "1h" || five > 0 && plan.CacheWrite.Variant == "1h" {
				return fail("measured cache creation TTL differs from the reserved price")
			}
			total, ok := budgetUsageInteger(raw["cache_creation_input_tokens"])
			if !ok {
				total, ok = budgetUsageInteger(raw["cache_write_tokens"])
			}
			if !ok || total != five+hour {
				return fail("measured cache creation details contradict the total")
			}
		}
	}
	return nil
}

// budgetUsageInteger 严格读取非负实测整数；参数为上游动态值，返回数和有效标志，不接受数字字符串或溢出。
func budgetUsageInteger(value any) (int, bool) {
	switch value.(type) {
	case int, int64, float32, float64, json.Number:
	default:
		return 0, false
	}
	n, ok := floatParam(map[string]any{"v": value}, "v")
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n != math.Trunc(n) || n > math.MaxInt32 {
		return 0, false
	}
	return int(n), true
}

// budgetUsageDimensions 递归检查usage计数，防止可选明细被静默丢失；参数为用量树及已认证根层搜索授权，返回不可计价错误。
// 只遍历小型计量对象，不读取请求正文；未知多模态和服务器工具非零计量不能套用文字价格。
func budgetUsageDimensions(values map[string]any, search bool) error {
	for key, value := range values {
		if value == nil {
			continue
		}
		if strings.HasSuffix(key, "_tokens") || key == "cached_tokens" || key == "searches" || key == "search_count" || key == "web_search_requests" || key == "web_fetch_requests" {
			n, ok := budgetUsageInteger(value)
			if !ok {
				return budgetFailure("invalid_usage", "measured usage counts must be nonnegative integers")
			}
			if n > 0 && (strings.Contains(key, "audio") || strings.Contains(key, "image") || (strings.Contains(key, "search") && !(search && (key == "searches" || key == "search_count" || key == "web_search_requests"))) || strings.Contains(key, "fetch")) {
				return budgetFailure("invalid_usage", "measured usage contains an unreserved pricing dimension")
			}
		}
		if strings.HasSuffix(key, "_tokens_details") {
			if _, ok := value.(map[string]any); !ok {
				return budgetFailure("invalid_usage", "measured token details must be an object")
			}
		}
		if nested, ok := value.(map[string]any); ok {
			if err := budgetUsageDimensions(nested, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// budgetAddCharge 将一维已验证实测数量按冻结价格计入分项；参数为账单、费率、数量，无返回，不访问可变目录。
func budgetAddCharge(c *catalog.Charge, rate catalog.Rate, quantity int) {
	if quantity == 0 {
		return
	}
	amount := float64(quantity) * rate.USD
	switch rate.Side {
	case "input":
		c.Input += amount
	case "output":
		c.Output += amount
	default:
		c.Cache += amount
	}
	c.Applied = append(c.Applied, catalog.AppliedRate{Measure: rate.Measure, Side: rate.Side, Variant: rate.Variant, UnitSize: rate.UnitSize, USD: rate.USD, Quantity: float64(quantity), SourceKey: rate.SourceKey})
}

// budgetRoundUp 将预占金额向上舍入到万亿分之一美元；参数必须非负有限，返回金额或溢出错误，不使用放宽比较的epsilon。
func budgetRoundUp(amount float64) (float64, error) {
	const scale = 1e12
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || amount > float64(math.MaxInt64)/scale {
		return 0, budgetFailure("unsupported_budget", "strict budget amount is outside the supported range")
	}
	rounded := math.Ceil(amount*scale) / scale
	if rounded < amount {
		rounded = math.Nextafter(rounded, math.Inf(1))
	}
	return rounded, nil
}

// budgetInteger 读取可信限制或JSON整数；参数允许数字及配置字符串，返回正整数和标志，拒绝分数、溢出和非有限值。
func budgetInteger(value any) (int, bool) {
	n, ok := floatParam(map[string]any{"value": value}, "value")
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n != math.Trunc(n) || n > math.MaxInt32 {
		return 0, false
	}
	return int(n), true
}

// budgetJSONInteger 只接受真实JSON数字，避免绕过协议解析器的旁路将字符串或布尔值误当有效cap。
// 参数为已UseNumber解码的值，返回正整数与标志；配置数字仍由budgetInteger兼容字符串。
func budgetJSONInteger(value any) (int, bool) {
	if _, ok := value.(json.Number); !ok {
		return 0, false
	}
	return budgetInteger(value)
}

// budgetCatalogRow 只按明确base_model、上游ID和公开名查找可信目录行；返回行或nil，不猜供应商别名。
// 无法准确解析的别名宁可拒绝；部署手工费率仍可使用该行的上下文及能力声明。
func budgetCatalogRow(dep config.ModelEntry) map[string]any {
	ids := []string{}
	if id, ok := dep.ModelInfo["base_model"].(string); ok && id != "" {
		ids = append(ids, id)
	} else {
		ids = append(ids, dep.ParamString("model", ""), dep.ModelName)
	}
	rows := catalog.CostMap()
	for _, id := range ids {
		keys := []string{id, strings.ToLower(id)}
		if at := strings.Index(id, "/"); at > 0 {
			keys = append(keys, id[at+1:], strings.ToLower(id[at+1:]))
		}
		for _, key := range keys {
			if row := rows[key]; row != nil {
				return row
			}
		}
	}
	return nil
}

// budgetInputBound 读取部署优先的明确输入窗口；参数为部署和目录行，返回整窗上界，未知或非法声明拒绝。
func budgetInputBound(dep config.ModelEntry, row map[string]any) (int, error) {
	for _, source := range []map[string]any{dep.ModelInfo, dep.LiteLLMParams, row} {
		for _, key := range []string{"max_input_tokens", "context_length"} {
			if value, exists := source[key]; exists && value != nil {
				if n, ok := budgetInteger(value); ok {
					return n, nil
				}
				return 0, budgetFailure("unsupported_budget", "strict budget requires a valid trusted input token limit")
			}
		}
	}
	return 0, budgetFailure("unsupported_budget", "strict budget requires a trusted input token limit")
}

// budgetOutputCap 按实际出站协议验证并写入唯一cap；参数为私有正文/协议/部署/目录，返回上限与是否缺省，未知Chat能力拒绝。
func budgetOutputCap(body map[string]any, protocol string, dep config.ModelEntry, row map[string]any) (int, bool, error) {
	field := "max_output_tokens"
	if protocol == "anthropic-messages" {
		field = "max_tokens"
	}
	supported := map[string]bool{}
	if protocol == "openai-chat" {
		for _, source := range []map[string]any{dep.ModelInfo, dep.LiteLLMParams, row} {
			if value, exists := source["supported_parameters"]; exists && value != nil {
				raw, _ := json.Marshal(value)
				var names []string
				if json.Unmarshal(raw, &names) == nil {
					for _, name := range names {
						supported[name] = true
					}
				}
				break
			}
		}
		if supported["max_completion_tokens"] {
			field = "max_completion_tokens"
		} else if supported["max_tokens"] {
			field = "max_tokens"
		} else {
			return 0, false, budgetFailure("unsupported_budget", "strict Chat budget requires a declared output token limit parameter")
		}
	}
	cap, found := DefaultBudgetOutputTokens, false
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if value, exists := body[key]; exists {
			if found {
				return 0, false, budgetFailure("invalid_request", "output token limits are mutually exclusive")
			}
			if protocol == "openai-chat" && supported[key] {
				// 新cap包含隐藏推理，旧max_tokens仅为可见输出的模型必须改用更强的总输出约束。
				if !supported["max_completion_tokens"] {
					field = key
				}
			} else if key != field {
				return 0, false, budgetFailure("invalid_request", "output token limit does not match the selected protocol")
			}
			n, ok := budgetJSONInteger(value)
			if !ok {
				return 0, false, budgetFailure("invalid_request", "output token limit must be a positive bounded integer")
			}
			cap, found = n, true
		}
	}
	for _, source := range []map[string]any{dep.ModelInfo, dep.LiteLLMParams, row} {
		if value, exists := source["max_output_tokens"]; exists && value != nil {
			limit, ok := budgetInteger(value)
			if !ok {
				return 0, false, budgetFailure("unsupported_budget", "trusted output token limit is invalid")
			}
			if cap > limit {
				if found {
					return 0, false, budgetFailure("invalid_request", "output token limit exceeds the deployment limit")
				}
				cap = limit
			}
			break
		}
	}
	if thinking, ok := body["thinking"].(map[string]any); ok {
		if value, exists := thinking["budget_tokens"]; exists {
			n, valid := budgetJSONInteger(value)
			if !valid || n >= cap {
				return 0, false, budgetFailure("invalid_request", "thinking budget must fit inside the output token limit")
			}
		}
	}
	// 旧cap默认不能证明包含隐藏推理；仅管理员提供明确语义声明和来源后接受，不按模型名称猜测。
	if protocol == "openai-chat" && field == "max_tokens" && (body["reasoning"] != nil || body["reasoning_effort"] != nil || body["thinking"] != nil) && !budgetTrustedLegacyCap(dep) {
		return 0, false, budgetFailure("unsupported_budget", "strict reasoning budget requires a declared total output token limit")
	}
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		delete(body, key)
	}
	body[field] = cap
	return cap, !found, nil
}

// budgetRates 选择完整可信费率源并复制所有值；手工价格不回落目录，条件价格与未知变体由后续精确选择拒绝。
func budgetRates(dep config.ModelEntry, row map[string]any) ([]catalog.Rate, error) {
	source := budgetPriceSource(dep, row)
	if source == nil {
		return nil, budgetFailure("unsupported_budget", "strict budget requires complete token prices")
	}
	var rates []catalog.Rate
	if raw, exists := source["rates"]; exists {
		encoded, err := json.Marshal(raw)
		if err != nil || json.Unmarshal(encoded, &rates) != nil || len(rates) == 0 {
			return nil, budgetFailure("unsupported_budget", "strict budget rate table is invalid")
		}
	} else {
		known := map[string]bool{}
		for _, key := range strings.Fields("input_cost_per_token output_cost_per_token input_cost_per_token_peak output_cost_per_token_peak cache_read_input_token_cost cache_creation_input_token_cost cache_creation_input_token_cost_above_1hr input_cost_per_image output_cost_per_image input_cost_per_second output_cost_per_second search_context_cost_per_query") {
			known[key] = true
		}
		// 平面价格表中任何新增阈值/缓存价都不能静默忽略；非数字不是免费，未知阶梯不能用基础低价代替。
		for key, value := range source {
			if !strings.Contains(key, "cost_per_") && !strings.Contains(key, "_token_cost") {
				continue
			}
			if !known[key] {
				return nil, budgetFailure("unsupported_budget", "strict budget does not support this price dimension")
			}
			price, ok := floatParam(map[string]any{"v": value}, "v")
			if !ok || math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
				return nil, budgetFailure("unsupported_budget", "strict budget prices must be explicit finite nonnegative numbers")
			}
		}
		rates = catalog.RatesFromFlat(func(key string) (float64, bool) { return floatParam(source, key) })
		if v, exists := source["cache_creation_input_token_cost_above_1hr"]; exists {
			n, _ := floatParam(map[string]any{"v": v}, "v")
			rates = append(rates, catalog.Rate{Measure: "token", Side: "cache_write", Variant: "1h", Window: "all", UnitSize: 1, USD: n, SourceKey: "cache_creation_input_token_cost_above_1hr"})
		}
	}
	seen := map[string]bool{}
	for _, rate := range rates {
		if rate.Measure == "" || rate.Side == "" || rate.Fallback || math.IsNaN(rate.USD) || math.IsInf(rate.USD, 0) || rate.USD < 0 || rate.UnitSize <= 0 || math.IsNaN(rate.UnitSize) || math.IsInf(rate.UnitSize, 0) {
			return nil, budgetFailure("unsupported_budget", "strict budget rate must be finite and nonnegative")
		}
		switch rate.Measure {
		case "token", "second", "picture", "query":
			// 后三种维度只在实际请求对应功能时产生；文字请求校验已拒绝这些功能，实测出现时仍保持占用。
		default:
			return nil, budgetFailure("unsupported_budget", "strict budget rate measure is unsupported")
		}
		// generic费率不能掩盖同表中的条件推理/模态价；未实现的token变体必须由运营者给出明确统一价格。
		if rate.Measure == "token" {
			valid := false
			switch rate.Side {
			case "input":
				valid = rate.Variant == "" || rate.Variant == "uncached" || rate.Variant == "cached"
			case "output", "cache_read", "batch_input", "batch_output":
				valid = rate.Variant == ""
			case "cache_write":
				valid = rate.Variant == "" || rate.Variant == "1h"
			}
			if !valid {
				return nil, budgetFailure("unsupported_budget", "strict budget does not support conditional token price variants")
			}
		}
		if rate.Window != "all" && rate.Window != "peak" && rate.Window != "offpeak" {
			return nil, budgetFailure("unsupported_budget", "strict budget rate window is unsupported")
		}
		key := rate.Measure + "/" + rate.Side + "/" + rate.Variant + "/" + rate.Window
		if seen[key] {
			return nil, budgetFailure("unsupported_budget", "strict budget rate table contains ambiguous duplicates")
		}
		seen[key] = true
	}
	return rates, nil
}

// budgetRate 精确选择当前时段和已知变体，不使用目录的最低价fallback；参数为不可变费率及维度，返回副本与是否命中。
func budgetRate(rates []catalog.Rate, side, variant, window string) (catalog.Rate, bool) {
	variants := []string{variant}
	// 普通输入允许没有uncached标签；缓存/TTL必须精确匹配，不能被当前时段的普通输入价遮住。
	if variant == "uncached" {
		variants = append(variants, "")
	}
	for _, w := range []string{window, "all"} {
		for _, v := range variants {
			for _, rate := range rates {
				if rate.Measure == "token" && rate.Side == side && rate.Variant == v && rate.Window == w {
					return rate, true
				}
			}
		}
	}
	return catalog.Rate{}, false
}

// budgetRequest 验证收费维度并识别缓存TTL；参数为最终私有JSON、协议和已冻结的搜索授权，返回TTL或错误，不修改内容。
func budgetRequest(body map[string]any, protocol string, search bool) (string, error) {
	allowed := strings.Fields("model stream stream_options messages input instructions system tools functions function_call tool_choice parallel_tool_calls max_tool_calls max_tokens max_completion_tokens max_output_tokens temperature top_p frequency_penalty presence_penalty logit_bias logprobs top_logprobs seed stop user metadata reasoning reasoning_effort text response_format include store service_tier prompt_cache_key prompt_cache_retention thinking output_config n safety_identifier client_metadata verbosity cache_control")
	keys := map[string]bool{}
	for _, key := range allowed {
		keys[key] = true
	}
	for key, value := range body {
		if !keys[key] && value != nil {
			return "", budgetFailure("unsupported_budget", "strict budget does not support request field "+key)
		}
	}
	if value, exists := body["n"]; exists {
		n, ok := budgetJSONInteger(value)
		if !ok || n != 1 {
			return "", budgetFailure("invalid_request", "strict budget supports one completion only")
		}
	}
	if value := body["service_tier"]; value != nil && value != "default" && value != "standard" {
		return "", budgetFailure("unsupported_budget", "strict budget does not support dynamic service tiers")
	}
	if value := body["prompt_cache_retention"]; value != nil && value != "in_memory" {
		return "", budgetFailure("unsupported_budget", "strict budget does not support extended prompt cache retention")
	}
	ttl := ""
	if err := budgetCacheHint(body, &ttl); err != nil {
		return "", err
	}
	for _, key := range []string{"tools", "functions"} {
		if value := body[key]; value != nil {
			if err := budgetTools(value, &ttl, search && key == "tools"); err != nil {
				return "", err
			}
		}
	}
	if value := body["system"]; value != nil {
		if err := budgetContent(value, &ttl); err != nil {
			return "", err
		}
	}
	field := "messages"
	if protocol == "openai-responses" {
		field = "input"
	}
	value := body[field]
	if _, ok := value.(string); ok && protocol == "openai-responses" {
		return ttl, nil
	}
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return "", budgetFailure("invalid_request", "strict budget requires explicit dialogue history")
	}
	for _, value := range list {
		item, ok := value.(map[string]any)
		if !ok {
			return "", budgetFailure("invalid_request", "dialogue history items must be objects")
		}
		if err := budgetCacheHint(item, &ttl); err != nil {
			return "", err
		}
		kind, _ := item["type"].(string)
		switch kind {
		case "", "message":
			// 原生Chat音频可挂在message顶层，不能只检查content数组；未建模的消息扩展拒绝。
			for key, value := range item {
				if value == nil {
					continue
				}
				switch key {
				case "type", "id", "role", "content", "name", "tool_calls", "tool_call_id", "function_call", "reasoning_content", "reasoning_items", "refusal", "cache_control", "phase", "status":
				default:
					return "", budgetFailure("unsupported_budget", "strict budget does not support dialogue field "+key)
				}
			}
			if err := budgetContent(item["content"], &ttl); err != nil {
				return "", err
			}
		case "web_search_call":
			if protocol != "openai-responses" || !llmdialogue.ValidWebSearchHistory(item) {
				return "", budgetFailure("invalid_request", "invalid web search history item")
			}
		case "reasoning", "function_call", "custom_tool_call":
		case "function_call_output", "custom_tool_call_output":
			if err := budgetContent(item["output"], &ttl); err != nil {
				return "", err
			}
		case "additional_tools", "tool_search_output":
			if err := budgetTools(item["tools"], &ttl); err != nil {
				return "", err
			}
		case "tool_search_call":
			if item["execution"] != "client" {
				return "", budgetFailure("unsupported_budget", "strict budget does not support server tool search")
			}
		default:
			return "", budgetFailure("unsupported_budget", "strict budget does not support this history item")
		}
	}
	return ttl, nil
}

// budgetCacheHint 检查显式缓存写入期限；参数为块和当前TTL累积值，返回格式/混用错误，不解释缓存命中率。
func budgetCacheHint(item map[string]any, ttl *string) error {
	value, exists := item["cache_control"]
	if !exists || value == nil {
		return nil
	}
	hint, ok := value.(map[string]any)
	if !ok || hint["type"] != "ephemeral" {
		return budgetFailure("invalid_request", "cache control must declare ephemeral caching")
	}
	for key := range hint {
		if key != "type" && key != "ttl" {
			return budgetFailure("unsupported_budget", "strict budget does not support extended cache controls")
		}
	}
	next := "5m"
	if v, exists := hint["ttl"]; exists {
		next, ok = v.(string)
		if !ok || (next != "5m" && next != "1h") {
			return budgetFailure("invalid_request", "cache TTL must be 5m or 1h")
		}
	}
	if *ttl != "" && *ttl != next {
		return budgetFailure("unsupported_budget", "strict budget does not support mixed cache write TTLs")
	}
	*ttl = next
	return nil
}

// budgetContent 只接受文字、原生推理和客户端工具结果；参数为内容与TTL累积值，返回多模态/远程资源拒绝，无外部读取。
func budgetContent(value any, ttl *string) error {
	if value == nil {
		return nil
	}
	if _, ok := value.(string); ok {
		return nil
	}
	parts, ok := value.([]any)
	if !ok {
		return budgetFailure("unsupported_budget", "strict budget requires text-only content")
	}
	for _, value := range parts {
		part, ok := value.(map[string]any)
		if !ok {
			return budgetFailure("invalid_request", "content blocks must be objects")
		}
		if err := budgetCacheHint(part, ttl); err != nil {
			return err
		}
		switch part["type"] {
		case "text", "input_text", "output_text", "thinking", "redacted_thinking", "refusal":
		case "tool_use":
			if caller := part["caller"]; caller != nil {
				m, ok := caller.(map[string]any)
				if !ok || m["type"] != "direct" {
					return budgetFailure("unsupported_budget", "strict budget requires direct client tools")
				}
			}
		case "tool_result":
			if err := budgetContent(part["content"], ttl); err != nil {
				return err
			}
		default:
			return budgetFailure("unsupported_budget", "strict budget does not support multimodal or server-generated content")
		}
	}
	return nil
}

// budgetTools 检查工具定义；参数为数组、TTL及根层搜索授权，返回格式/能力错误。
// 客户端schema由输入整窗覆盖；仅已冻结预算的根层web_search允许，嵌套或其他服务器工具拒绝。
func budgetTools(value any, ttl *string, search ...bool) error {
	list, ok := value.([]any)
	if !ok {
		return budgetFailure("invalid_request", "tool definitions must be an array")
	}
	for _, value := range list {
		tool, ok := value.(map[string]any)
		if !ok {
			return budgetFailure("invalid_request", "tool definitions must be objects")
		}
		if err := budgetCacheHint(tool, ttl); err != nil {
			return err
		}
		kind, _ := tool["type"].(string)
		switch kind {
		case "web_search":
			if len(search) == 0 || !search[0] {
				return budgetFailure("unsupported_budget", "web search requires a verified budget contract")
			}
		case "function", "custom":
		case "":
			if _, ok := tool["name"].(string); !ok {
				return budgetFailure("invalid_request", "client tools require a name")
			}
		case "namespace":
			if err := budgetTools(tool["tools"], ttl); err != nil {
				return err
			}
		case "tool_search":
			if tool["execution"] != "client" {
				return budgetFailure("unsupported_budget", "strict budget does not support server tool search")
			}
		default:
			return budgetFailure("unsupported_budget", "strict budget does not support hosted tools")
		}
	}
	return nil
}

// String 返回计划的非敏感简述，供诊断记录；只读公开上限，不包含正文、凭据或完整费率表。
func (p BudgetPlan) String() string {
	return fmt.Sprintf("input<=%d output<=%d reserve=%g", p.InputBound, p.OutputCap, p.Amount)
}

// budgetTrustedLegacyCap 检查管理员对旧max_tokens总输出语义的明确认证；参数为实际部署，返回可信声明是否完整。
// 必须是真正布尔true并附非空来源，普通supported_parameters只证明字段存在，不能证明包含隐藏推理。
func budgetTrustedLegacyCap(dep config.ModelEntry) bool {
	declared, ok := dep.ModelInfo["max_tokens_includes_reasoning"].(bool)
	source, sourceOK := dep.ModelInfo["max_tokens_semantics_source"].(string)
	return ok && declared && sourceOK && strings.TrimSpace(source) != ""
}
