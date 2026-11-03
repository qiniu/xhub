package budgetconfig

import (
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
)

// prefix 保留旧通用目录的单数规则；kind 来自适配器，返回 ID/别名前缀。
// budget/budgets 使用相同 ID 前缀但保持两个存储命名空间；不产生副作用。
func prefix(kind string) string { return strings.TrimSuffix(kind, "s") }

// Normalize 补齐预算配置默认字段；kind 为旧资源种类，obj 为待规范化对象。
// 服务及 Freeze 调用，无返回值；仅替换 nil，保留零/空值，移除顶层密码且不写数据库。
// 这些默认字段仅为配置元数据，不接入账号预算、限流或刷新执行链。
func Normalize(kind string, obj map[string]any) {
	delete(obj, "password")
	if obj["id"] == nil {
		obj["id"] = prefix(kind) + "_" + util.CallID()[:12]
	}
	if obj["created_at"] == nil {
		obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if obj["budget_id"] == nil {
		id, _ := obj["id"].(string)
		obj["budget_id"] = id
	}
	for _, key := range []string{"max_budget", "soft_budget", "tpm_limit", "rpm_limit", "budget_duration", "budget_reset_at"} {
		if _, exists := obj[key]; !exists {
			obj[key] = nil
		}
	}
}
