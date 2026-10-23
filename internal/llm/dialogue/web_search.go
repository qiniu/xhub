package dialogue

import "errors"

// ErrWebSearchUnsupported 标识当前部署不能兑现请求中的托管搜索能力；选路层据此返回具体能力错误，不暴露连接秘密。
var ErrWebSearchUnsupported = errors.New("web_search is not supported by this deployment; disable web search or enable optional-search fallback; required search cannot be dropped")

// PrepareWebSearch 按部署能力处理 Responses 搜索声明；参数 d 为已解析请求，native 表示上游可原样承接，drop 表示管理员允许降级。
// 返回独立请求、是否移除了搜索及错误；部署筛选和执行共用，不修改原请求，不执行搜索。
// 仅移除可选搜索声明；强制工具选择、搜索历史及其他未知能力仍按原有严格协议规则校验。
func PrepareWebSearch(d Dialogue, native, drop bool) (Dialogue, bool, error) {
	if d.Source != "openai-responses" || d.Native == nil {
		return d, false, nil
	}
	// visitTools 遍历顶层及 additional_tools 声明；参数为正文和只读/过滤回调，无返回。
	// 原生及无搜索请求只扫描，不克隆大段 Codex 历史；只有显式降级才复制并修改工具数组。
	visitTools := func(body map[string]any, visit func(map[string]any)) {
		visit(body)
		if input, ok := body["input"].([]any); ok {
			for _, value := range input {
				item := dialogueMap(value)
				if item["type"] == "additional_tools" {
					visit(item)
				}
			}
		}
	}
	found := false
	// filter 搜索声明扫描/过滤器；只在 drop 副本上写入，普通工具与用户输入原样保留。
	filter := func(container map[string]any, remove bool) {
		tools, ok := container["tools"].([]any)
		if !ok {
			return
		}
		var kept []any
		if remove {
			kept = make([]any, 0, len(tools))
		}
		for _, value := range tools {
			kind := dialogueString(dialogueMap(value)["type"])
			if kind == "web_search" || kind == "web_search_preview" {
				found = true
			} else if remove {
				kept = append(kept, value)
			}
		}
		if remove {
			container["tools"] = kept
		}
	}
	visitTools(d.Native, func(container map[string]any) { filter(container, false) })
	body := d.Native
	choice := dialogueMap(body["tool_choice"])
	forced := choice["type"] == "web_search" || choice["type"] == "web_search_preview"
	// allowed_tools 等复合选择可能要求搜索；当前不改写其约束，避免删除声明后仍暗含强制搜索。
	choiceType := dialogueString(choice["type"])
	constrained := choiceType != "" && choiceType != "function" && choiceType != "custom"
	if !found && !forced {
		return d, false, nil
	}
	if native {
		return d, false, nil
	}
	// required 对整个工具集合有执行要求，保守拒绝降级；不能把“必须搜索”悄悄改成普通回答。
	if !drop || forced || constrained || body["tool_choice"] == "required" {
		return d, false, ErrWebSearchUnsupported
	}
	body, err := cloneObject(d.Native)
	if err != nil {
		return d, false, err
	}
	visitTools(body, func(container map[string]any) { filter(container, true) })
	var prepared Dialogue
	if d.NativeResponses {
		prepared, err = ParseNativeResponses(body)
		prepared.NativeResponses = true
	} else {
		prepared, err = ParseDialogue(d.Source, body)
	}
	if err != nil {
		return d, false, err
	}
	return prepared, true, nil
}
