package promptconfig

import (
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
)

// Normalize 补齐提示词配置的旧默认字段；obj 为可修改 JSON 对象，供服务及旧 Freeze 适配器调用。
// 删除顶层 password，仅为 nil 字段补默认，不修改零版本或空ID，不写存储；version 不代表实现了历史版本功能。
func Normalize(obj map[string]any) {
	delete(obj, "password")
	if obj["id"] == nil {
		obj["id"] = "prompt_" + util.CallID()[:12]
	}
	if obj["created_at"] == nil {
		obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if obj["prompt_id"] == nil {
		id, _ := obj["id"].(string)
		obj["prompt_id"] = id
	}
	if obj["version"] == nil {
		obj["version"] = 1
	}
}
