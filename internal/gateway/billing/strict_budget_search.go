package billing

import (
	"fmt"
	"strings"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
)

// budgetSearch 为受信 Responses 搜索契约冻结次数和单价；只支持按 search action 收费的文字搜索。
// Source 必须由部署管理员记录实际供应商的上限、累计输入和费用来源，不能由模型名推断。
type budgetSearch struct {
	MaxCalls   int          `json:"max_calls"`
	InputBound int          `json:"input_bound"`
	Rate       catalog.Rate `json:"rate"`
	Source     string       `json:"source"`
}

// budgetPrepareSearch 检查最终请求中的搜索声明并冻结显式部署契约；参数为部署、协议、正文，返回搜索预占资料或错误。
// StrictBudgetPlan 调用；只改实际出站 max_tool_calls，不删除搜索、不更改用户提示、不调用外部服务。
func budgetPrepareSearch(dep config.ModelEntry, protocol string, body map[string]any) (*budgetSearch, error) {
	tools, _ := body["tools"].([]any)
	present := false
	for _, value := range tools {
		tool, _ := value.(map[string]any)
		kind, _ := tool["type"].(string)
		if kind == "web_search" {
			if present {
				return nil, budgetFailure("invalid_request", "budgeted requests require one web search declaration")
			}
			present = true
			for key := range tool {
				switch key {
				case "type", "search_context_size", "filters", "user_location", "external_web_access":
				default:
					return nil, budgetFailure("unsupported_budget", "unverified web search tool option")
				}
			}
		}
		if strings.HasPrefix(kind, "web_search_preview") {
			return nil, budgetFailure("unsupported_budget", "strict budget requires a verified pricing contract for preview search")
		}
	}
	if !present {
		if body["max_tool_calls"] != nil {
			return nil, budgetFailure("unsupported_budget", "max_tool_calls requires a budgeted web search tool")
		}
		return nil, nil
	}
	profile, _ := dep.ModelInfo["web_search_budget_profile"].(string)
	source, _ := dep.ModelInfo["web_search_budget_source"].(string)
	input, ok := budgetUsageInteger(dep.ModelInfo["web_search_budget_input_tokens"])
	if protocol != "openai-responses" || profile != "openai_responses_search_actions" || strings.TrimSpace(source) == "" || !ok || input <= 0 {
		return nil, budgetFailure("unsupported_budget", "web search requires a verified call limit, cumulative input bound and pricing contract for this deployment")
	}
	maxCalls := 1
	if value, exists := body["max_tool_calls"]; exists {
		n, valid := budgetJSONInteger(value)
		if !valid || n < 1 || n > 16 {
			return nil, budgetFailure("invalid_request", "budgeted max_tool_calls must be an integer between 1 and 16")
		}
		maxCalls = n
	}
	// 累计输入上界必须针对管理员允许的最大工具次数声明，不能用单窗乘次数猜测供应商内部计费循环。
	configured, valid := budgetUsageInteger(dep.ModelInfo["web_search_budget_max_calls"])
	if !valid || configured < 1 || configured > 16 || maxCalls > configured {
		return nil, budgetFailure("unsupported_budget", "web search call limit exceeds the verified deployment contract")
	}
	// 平面搜索价与显式rate表混用会导致预算与目录展示选取不同价格，未完成统一契约前明确拒绝。
	if dep.LiteLLMParams["rates"] != nil {
		return nil, budgetFailure("unsupported_budget", "web search budget requires flat manual prices without a rates override")
	}
	price, valid := floatParam(dep.LiteLLMParams, "search_context_cost_per_query")
	if !valid || price <= 0 {
		return nil, budgetFailure("unsupported_budget", "web search requires an explicit positive per-search price")
	}
	if _, err := budgetRoundUp(price * float64(maxCalls)); err != nil {
		return nil, err
	}
	body["max_tool_calls"] = maxCalls
	return &budgetSearch{MaxCalls: maxCalls, InputBound: input, Source: source, Rate: catalog.Rate{Measure: "query", Side: "output", Window: "all", UnitSize: 1, USD: price, SourceKey: "search_context_cost_per_query"}}, nil
}

// budgetSearchUsage 验证终态提取的搜索计量及上游别名；参数为实测用量和冻结契约，返回矛盾、缺失或越界错误。
// ValidateStrictBudgetUsage 调用；不从缺失字段猜零，失败保留预占等待核对。
func budgetSearchUsage(raw map[string]any, search *budgetSearch) error {
	calls, present := raw["xhub_web_search_calls"]
	count, valid := budgetUsageInteger(calls)
	searches, validSearch := budgetUsageInteger(raw["xhub_web_search_searches"])
	if search == nil {
		if present && (!valid || count != 0) {
			return budgetFailure("invalid_usage", "measured web search calls have no reserved price")
		}
		return nil
	}
	if !present || !valid || !validSearch || count > search.MaxCalls || searches > count {
		return budgetFailure("invalid_usage", "web search usage is missing or exceeds the reserved call limit")
	}
	// 另一供应商的计费类别不是此契约的同义字段；显式非零工具明细不能被忽略后低价结算。
	if value, exists := raw["web_search_calls"]; exists {
		n, ok := budgetUsageInteger(value)
		if !ok || n != 0 {
			return budgetFailure("invalid_usage", "web_search_calls requires a different pricing contract")
		}
	}
	if details, exists := raw["server_side_tool_usage_details"]; exists {
		values, ok := details.(map[string]any)
		if !ok {
			return budgetFailure("invalid_usage", "unsupported server tool usage details")
		}
		for _, value := range values {
			n, ok := budgetUsageInteger(value)
			if !ok || n != 0 {
				return budgetFailure("invalid_usage", "server tool usage requires a different pricing contract")
			}
		}
	}
	if value, exists := raw["num_server_side_tools_used"]; exists {
		n, ok := budgetUsageInteger(value)
		if !ok || n != count {
			return budgetFailure("invalid_usage", "server tool count contradicts response output")
		}
	}
	for _, key := range []string{"searches", "search_count", "web_search_requests"} {
		if value, exists := raw[key]; exists {
			n, ok := budgetUsageInteger(value)
			if !ok || n != searches {
				return budgetFailure("invalid_usage", fmt.Sprintf("measured %s contradicts search actions", key))
			}
		}
	}
	return nil
}
