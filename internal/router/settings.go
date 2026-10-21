package router

import (
	"fmt"
	"math"
	"strings"

	"github.com/sunqirui1987/xhub/internal/config"
)

// RouteSettings 表示一次请求使用的模板、路由组和模型管理默认分配。
// 策略优先级为模板模型规则、模板路由组、ModelDefaults；模板回退按主模型和类别覆盖全局默认。
type RouteSettings struct {
	// 完整模板文档；按 key→team→organization 选第一份，不逐字段拼接，不包含实时运行状态。
	Settings map[string]any
	// 模型管理的默认部署权重映射，traffic-split 且无显式分配时读取，组使用自己的分配。
	ModelDefaults map[string]any
	// 编译后的三类别回退图，结合模型默认与模板覆盖，每轮偏好必须验证该图。
	ModelFallbacks map[string]FallbackPolicy
	// 当前模板的显式组及成员目录，组名请求展开成员，成员请求只匹配自身部署。
	RoutingGroups []Group
	// ForModel 得到的本模型/组策略和权重，Schedule 使用；不改变模型目录。
	Policy Policy
	// 模板或模型策略的解析、目录校验错误；非 nil 时执行器在发出上游前拒绝，不使用部分设置。
	Err error
	// 生效模板的数据库 ID，无绑定使用 builtin 时为空。
	TemplateID string
	// 生效模板显示名，供控制台解释选路来源，不参与调度。
	TemplateName string
	// 模板选择来源：key/team/organization、selected/draft（预览）或 builtin（无绑定）。
	Source string
	// 策略命中来源：template-model/routing-group/model-default，解释策略优先级。
	RuleSource string
}

// BuiltinSource 表示未绑定模板时使用模型管理默认分配。
const BuiltinSource = "builtin"

// BuiltinDocument 返回新模板的公共执行参数。模型规则默认为空，不自动复制模型目录。
func BuiltinDocument() map[string]any {
	return map[string]any{
		"model_routes": []any{},
		"retry_policy": map[string]any{
			"max_attempts": 1, "timeout_seconds": 600, "failure_threshold": 3, "cooldown_seconds": 0,
		},
	}
}

// BuiltinSettings 返回未绑定模板时的设置；所有模型均走模型管理里的默认分配。
func BuiltinSettings() RouteSettings {
	return RouteSettings{Settings: BuiltinDocument(), Source: BuiltinSource, RuleSource: "model-default"}
}

// Strategy 返回已解析策略。调用 ForModel 前使用模型管理默认的按权重随机。
func (r RouteSettings) Strategy() string {
	if r.Policy.Strategy != "" {
		return r.Policy.Strategy
	}
	return "traffic-split"
}

// ForModel 解析公开模型或组名的策略；参数为请求 model，返回独立设置或包含解析错误的设置。
// 请求及预览共用：模板模型规则优先于路由组和模型权重；组内 traffic-split
// 使用组权重，未设置部署默认为 1；模板显式权重优先于实时模型默认，不修改原始文档。
func (r RouteSettings) ForModel(modelName string) RouteSettings {
	if r.Err != nil {
		return r
	}
	if _, exists := r.Settings["routing_strategy"]; exists {
		r.Err = fmt.Errorf("unsupported route template field %q", "routing_strategy")
		return r
	}
	rules, err := modelRouteRules(r.Settings)
	if err != nil {
		r.Err = err
		return r
	}
	strategy := "traffic-split"
	r.RuleSource = "model-default"

	for _, group := range r.RoutingGroups {
		matched := group.Name == modelName
		for _, member := range group.Models {
			matched = matched || member == modelName
		}
		if matched {
			strategy = group.Strategy
			r.RuleSource = "routing-group"
			break
		}
	}
	var custom *Policy
	for _, rule := range rules {
		if strings.TrimSpace(rule["model"].(string)) == modelName {
			strategy = strings.TrimSpace(rule["strategy"].(string))
			r.RuleSource = "template-model"
			if _, exists := rule["allocations"]; exists {
				// modelRouteRules 已严格校验相同字段；这里仅构造请求的独立策略，不读写默认权重。
				p, _ := ParsePolicy(map[string]any{"strategy": strategy, "allocations": rule["allocations"]})
				custom = &p
			}
			break
		}
	}
	r.Policy = Policy{Strategy: strategy}
	if custom != nil {
		r.Policy = *custom
	} else if strategy == "traffic-split" && r.RuleSource != "routing-group" {
		if raw, ok := r.ModelDefaults[modelName]; ok {
			r.Policy, r.Err = ParseDefaultWeights(raw)
		}
	}
	if r.RuleSource == "routing-group" {
		for _, group := range r.RoutingGroups {
			matched := group.Name == modelName
			for _, member := range group.Models {
				matched = matched || member == modelName
			}
			if matched && group.Args != nil {
				r.Policy.Allocations = group.Args.Allocations
				break
			}
		}
	}
	return r
}

// Candidates 返回请求模型或显式组的真实部署，供预览使用；不排序、不改变模型身份。
func (r RouteSettings) Candidates(list []config.ModelEntry, name string) []config.ModelEntry {
	for _, group := range r.RoutingGroups {
		if group.Name == name {
			return GroupCandidates(list, group.Models)
		}
	}
	return All(list, name)
}

// Schedule 为数据面统一展开显式组并使用解析后的策略与权重；成员名请求只匹配该成员。
// 参数为兼容候选、请求名、运行状态及会话部署；返回尝试顺序，保留权限和计费身份。
func (r RouteSettings) Schedule(list []config.ModelEntry, name string, state State, pinned string) []config.ModelEntry {
	state.Allocations = r.Policy.Shares()
	for _, group := range r.RoutingGroups {
		if group.Name == name {
			state.ModelNames = group.Models
			break
		}
	}
	return Schedule(list, name, r.Strategy(), state, pinned)
}

// ValidateRouteTemplateDocument 校验保存或预览的模板文档，返回首个字段错误，无写入副作用。
// 策略仅配置在模型规则或模板内路由组；模板顶层不接受默认策略。
// 模型规则可保存独立部署权重；不支持执行的字段必须拒绝，防止保存成功却无效。
func ValidateRouteTemplateDocument(settings map[string]any) error {
	allowed := map[string]bool{"model_routes": true, "retry_policy": true, "routing_groups": true, "fallbacks": true, "context_window_fallbacks": true, "content_policy_fallbacks": true}
	for key := range settings {
		if !allowed[key] {
			return fmt.Errorf("unsupported route template field %q", key)
		}
	}

	if _, err := modelRouteRules(settings); err != nil {
		return err
	}
	policy, err := retryPolicy(settings)
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name              string
		integer, positive bool
	}{{"max_attempts", true, true}, {"timeout_seconds", false, true}, {"failure_threshold", true, false}, {"cooldown_seconds", false, false}} {
		number, valid := numberIn(policy[field.name])
		if !valid || number < 0 || (field.positive && number == 0) || (field.integer && math.Trunc(number) != number) {
			return fmt.Errorf("invalid retry_policy.%s", field.name)
		}
	}
	_, _, err = TemplateRouting(settings)
	return err
}

// modelRouteRules 严格解析公开模型覆盖表；模型名必须唯一，规则包含 model、strategy 和可选 allocations；省略继承默认，空数组为均等权重。
func modelRouteRules(settings map[string]any) ([]map[string]any, error) {
	raw, exists := settings["model_routes"]
	if !exists {
		return nil, fmt.Errorf("model_routes is required")
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("model_routes must be an array")
	}
	rows := make([]map[string]any, 0, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		rule, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("model_routes[%d] must be an object", i)
		}
		for key := range rule {
			if key != "model" && key != "strategy" && key != "allocations" {
				return nil, fmt.Errorf("model_routes[%d] contains unsupported field %q", i, key)
			}
		}
		name, nameOK := rule["model"].(string)
		name = strings.TrimSpace(name)
		if !nameOK || name == "" {
			return nil, fmt.Errorf("model_routes[%d].model is required", i)
		}
		if seen[name] {
			return nil, fmt.Errorf("model_routes has duplicate model %q", name)
		}
		seen[name] = true
		strategy, strategyOK := rule["strategy"].(string)
		strategy = strings.TrimSpace(strategy)
		if !strategyOK || strategy == "" {
			return nil, fmt.Errorf("model_routes[%d].strategy is required", i)
		}
		if err := ValidateStrategy(strategy); err != nil {
			return nil, fmt.Errorf("model_routes[%d].strategy: %w", i, err)
		}
		normalized := map[string]any{"model": name, "strategy": strategy}
		if raw, exists := rule["allocations"]; exists {
			if _, ok := raw.([]any); !ok || strategy != "traffic-split" {
				return nil, fmt.Errorf("model_routes[%d].allocations requires traffic-split and an array", i)
			}
			for _, entry := range raw.([]any) {
				allocation, ok := entry.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("model_routes[%d].allocations must contain objects", i)
				}
				if _, valid := numberIn(allocation["weight"]); !valid {
					return nil, fmt.Errorf("model_routes[%d].allocations.weight must be a number", i)
				}
			}
			if _, err := ParsePolicy(map[string]any{"strategy": strategy, "allocations": raw}); err != nil {
				return nil, fmt.Errorf("model_routes[%d]: %w", i, err)
			}
			normalized["allocations"] = raw
		}
		rows = append(rows, normalized)
	}
	return rows, nil
}

// retryPolicy 返回严格的失败重试对象；四个字段必须完整存在，避免模板语义依赖隐式合并。
func retryPolicy(settings map[string]any) (map[string]any, error) {
	raw, exists := settings["retry_policy"]
	if !exists {
		return nil, fmt.Errorf("retry_policy is required")
	}
	policy, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("retry_policy must be an object")
	}
	allowed := map[string]bool{"max_attempts": true, "timeout_seconds": true, "failure_threshold": true, "cooldown_seconds": true}
	for key := range policy {
		if !allowed[key] {
			return nil, fmt.Errorf("retry_policy contains unsupported field %q", key)
		}
	}
	for key := range allowed {
		if _, exists := policy[key]; !exists {
			return nil, fmt.Errorf("retry_policy.%s is required", key)
		}
	}
	return policy, nil
}

// Retries 返回每条部署的总尝试次数；缺失或小于一时使用 1。
func (r RouteSettings) Retries() int {
	policy, _ := r.Settings["retry_policy"].(map[string]any)
	if n, ok := numberIn(policy["max_attempts"]); ok && n >= 1 {
		return int(n)
	}
	return 1
}

// TimeoutSeconds 返回单次上游调用超时秒数；无效值使用 600。包含首帧等待和完整响应读取，由执行器创建每次尝试的 deadline。
func (r RouteSettings) TimeoutSeconds() float64 {
	policy, _ := r.Settings["retry_policy"].(map[string]any)
	if n, ok := numberIn(policy["timeout_seconds"]); ok && n > 0 {
		return n
	}
	return 600
}

// AllowedFails 返回部署进入冷静期前允许的失败次数；缺失时使用 3。
func (r RouteSettings) AllowedFails() int {
	policy, _ := r.Settings["retry_policy"].(map[string]any)
	if n, ok := numberIn(policy["failure_threshold"]); ok {
		return int(n)
	}
	return 3
}

// CooldownSeconds 返回部署冷静期秒数；缺失时使用 0。
func (r RouteSettings) CooldownSeconds() float64 {
	policy, _ := r.Settings["retry_policy"].(map[string]any)
	n, _ := numberIn(policy["cooldown_seconds"])
	return n
}

// numberIn 读取 JSON 或 Go 数字，拒绝字符串及非有限浮点数。
func numberIn(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case float32:
		v := float64(n)
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
