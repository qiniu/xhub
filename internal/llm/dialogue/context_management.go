package dialogue

import "fmt"

// ValidateKeepAllContextManagement 验证不改变历史的 Messages 上下文管理提示。
// 参数 value 为已解析 JSON 对象；返回 nil 表示 edits 为空或仅要求保留全部推理，否则返回固定格式错误。
// 协议桥接和严格预算共同调用；不清理历史、不修改输入、不发请求，不接受真实压缩或未知清理动作。
func ValidateKeepAllContextManagement(value any) error {
	config, ok := value.(map[string]any)
	if !ok || len(config) != 1 {
		return fmt.Errorf("context_management requires an edits array")
	}
	edits, ok := config["edits"].([]any)
	if !ok {
		return fmt.Errorf("context_management.edits must be an array")
	}
	for _, value := range edits {
		edit, ok := value.(map[string]any)
		if !ok || len(edit) != 2 || edit["type"] != "clear_thinking_20251015" || edit["keep"] != "all" {
			return fmt.Errorf("context_management requires keep-all edits")
		}
	}
	return nil
}
