package billing

import (
	"encoding/json"
	"strings"

	"github.com/sunqirui1987/xhub/internal/catalog"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// SearchTokenOnlyRequest 识别原生 Responses 联网搜索请求；参数为实际出站协议和正文，返回是否采用仅 token 软预算及校验错误。
// 网关准入调用；保留全部搜索参数和客户端工具，不设置工具/输出上限，不放开其他服务器收费工具。
func SearchTokenOnlyRequest(protocol string, raw []byte) (bool, error) {
	if protocol != "openai-responses" {
		return false, nil
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return false, nil
	}
	tools, _ := body["tools"].([]any)
	search := false
	for _, v := range tools {
		tool, _ := v.(map[string]any)
		kind, _ := tool["type"].(string)
		if isSearchTool(kind) {
			search = true
		}
	}
	if !search {
		return false, nil
	}
	if err := validateSearchCompanionTools(tools, true); err != nil {
		return false, err
	}
	return true, nil
}

// isSearchTool 判断已知 Responses 搜索声明；参数为工具类型，返回分类，不按任意前缀放开未知收费能力。
func isSearchTool(kind string) bool {
	switch kind {
	case "web_search", "web_search_2025_08_26", "web_search_preview", "web_search_preview_2025_03_11":
		return true
	}
	return false
}

// validateSearchCompanionTools 校验搜索旁的工具；参数为声明及根层标志，返回错误，递归仅允许客户端 namespace/function/custom/tool_search。
// 搜索必须处于根层；未知或其他服务器工具不能借仅 token 模式绕过原有硬预算约束，无持久副作用。
func validateSearchCompanionTools(tools []any, root bool) error {
	for _, v := range tools {
		tool, ok := v.(map[string]any)
		if !ok {
			return budgetFailure("invalid_request", "tool definitions must be objects")
		}
		kind, _ := tool["type"].(string)
		if root && isSearchTool(kind) {
			continue
		}
		switch kind {
		case "function", "custom":
		case "namespace":
			nested, ok := tool["tools"].([]any)
			if !ok {
				return budgetFailure("invalid_request", "namespace tools must be an array")
			}
			if err := validateSearchCompanionTools(nested, false); err != nil {
				return err
			}
		case "tool_search":
			if tool["execution"] != "client" {
				return budgetFailure("unsupported_budget", "token-only search does not support server tool search")
			}
		default:
			return budgetFailure("unsupported_budget", "token-only search does not support hosted tool "+strings.TrimSpace(kind))
		}
	}
	return nil
}

// SearchTokenUsage 提取完整上游 token 用量并排除工具费用；参数为原始实测对象，返回目录计价用量，不改变原始日志。
// recordSpend 在搜索软预算使用；保留缺失/冲突标记与缓存 token，非 token 计量不转成工具价格，也不推测缺失 token。
func SearchTokenUsage(raw map[string]any) catalog.Usage {
	normalized := llmdialogue.NormalizeDialogueUsage(raw)
	// 目录的模态分桶读取 Responses 名称；对话归一化改为 prompt 别名后须保留此事实，不能退化成文本通价。
	if details, ok := normalized["prompt_tokens_details"]; ok {
		normalized["input_tokens_details"] = details
	}
	u := catalog.NormalizeUsage(normalized)
	// 保留图片/文字 token 分桶及输出费率变体；仅移除按次数、张数、时长计量的非token费用。
	u.Searches, u.Images, u.InputImages = 0, 0, 0
	u.InputSeconds, u.Seconds = 0, 0
	return u
}
