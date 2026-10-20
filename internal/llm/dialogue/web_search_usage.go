package dialogue

// ValidWebSearchHistory 校验公开搜索历史的已知结构；参数为一条历史项，返回是否可作为纯文字历史传递。
// 预算检查调用，不执行搜索、不重计历史费用；未知字段和未完成状态不能夹带未建模的收费能力。
func ValidWebSearchHistory(item map[string]any) bool {
	if item["type"] != "web_search_call" || dialogueString(item["id"]) == "" || item["status"] != "completed" {
		return false
	}
	for k := range item {
		if k != "type" && k != "id" && k != "status" && k != "action" {
			return false
		}
	}
	action, ok := item["action"].(map[string]any)
	if !ok {
		return false
	}
	switch action["type"] {
	case "search", "open_page", "find_in_page":
	default:
		return false
	}
	for k, v := range action {
		switch k {
		case "type", "query", "url", "pattern":
			if _, ok := v.(string); !ok {
				return false
			}
		case "queries":
			values, ok := v.([]any)
			if !ok {
				return false
			}
			for _, value := range values {
				if _, ok := value.(string); !ok {
					return false
				}
			}
		case "sources":
			sources, ok := v.([]any)
			if !ok {
				return false
			}
			for _, value := range sources {
				source, ok := value.(map[string]any)
				if !ok {
					return false
				}
				for key, value := range source {
					if key != "type" && key != "url" {
						return false
					}
					if _, ok := value.(string); !ok {
						return false
					}
				}
			}
		default:
			return false
		}
	}
	return true
}

// ResponsesSearchUsage 从完整 Responses 输出快照提取搜索事实；参数为终态和已有usage，返回独立用量对象。
// 同步/流/原生旁路共用，只统计唯一已完成项；不累加中间事件，不覆盖供应商计量，不把缺失output猜成零。
func ResponsesSearchUsage(doc, usage map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range usage {
		out[k] = v
	}
	// 这些字段只能由已验证终态重新产生，上游不能伪造内部计量标记。
	delete(out, "xhub_web_search_calls")
	delete(out, "xhub_web_search_searches")
	if doc["status"] != "completed" && doc["status"] != "incomplete" {
		return out
	}
	list, ok := doc["output"].([]any)
	if !ok {
		return out
	}
	calls, searches := 0, 0
	seen := map[string]bool{}
	for _, v := range list {
		item, ok := v.(map[string]any)
		if !ok {
			return out
		}
		if item["type"] != "web_search_call" {
			switch item["type"] {
			case "message", "reasoning", "function_call", "custom_tool_call":
				continue
			case "tool_search_call":
				if item["execution"] == "client" {
					continue
				}
			}
			// 其他服务端工具不在搜索契约内；不生成可用于预算退款的完整搜索事实。
			return out
		}
		if !ValidWebSearchHistory(item) || seen[dialogueString(item["id"])] {
			out["pricing_blocked"] = "web_search_usage_invalid"
			return out
		}
		seen[dialogueString(item["id"])] = true
		calls++
		if dialogueMap(item["action"])["type"] == "search" {
			searches++
		}
	}
	out["xhub_web_search_calls"] = calls
	out["xhub_web_search_searches"] = searches
	return out
}
