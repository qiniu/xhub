package policyconfig

import (
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
)

// prefix 保留旧通用目录的单数规则；kind 来自适配器，返回 ID/别名前缀。
// policies 的旧前缀是 policie，不能在职责迁移中悄悄改成 policy；不产生副作用。
func prefix(kind string) string { return strings.TrimSuffix(kind, "s") }

// Normalize 补齐策略配置默认字段；kind 为旧资源种类，obj 为待规范化对象。
// 服务及 Freeze 调用，无返回值；仅替换 nil，保留零/空值，移除顶层密码且不写数据库。
// status/version 是管理元数据，不表示新增策略执行或版本历史功能。
func Normalize(kind string, obj map[string]any) {
	delete(obj, "password")
	if obj["id"] == nil {
		obj["id"] = prefix(kind) + "_" + util.CallID()[:12]
	}
	if obj["created_at"] == nil {
		obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if obj["policy_id"] == nil {
		id, _ := obj["id"].(string)
		obj["policy_id"] = id
	}
	for k, v := range map[string]any{"policy_name": "", "version": 1, "status": "active"} {
		if obj[k] == nil {
			obj[k] = v
		}
	}
}
