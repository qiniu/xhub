package affinity

import "strings"

// hasExternalCompaction 判断完整 Responses 输入是否包含供应商生成的压缩历史。
// 参数 body 为客户端正文；返回是否存在非空原厂密文，不解密、不改正文或读取其他会话。
// PlanRoute 用它允许无本地首轮记录的旧会话原生续聊；previous_response_id 和私有引用仍单独授权。
func hasExternalCompaction(body map[string]any) bool {
	items, _ := body["input"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		cipher, _ := item["encrypted_content"].(string)
		if item["type"] == "compaction" && strings.TrimSpace(cipher) != "" && !strings.HasPrefix(cipher, "xhub_messages_") {
			return true
		}
	}
	return false
}
