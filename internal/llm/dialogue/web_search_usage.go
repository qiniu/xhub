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
