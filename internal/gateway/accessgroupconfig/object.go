package accessgroupconfig

import (
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
)

// prefix 返回两个旧命名空间共用的 ID 前缀；kind 为适配器提供的资源种类。
// 服务生成默认 ID 时调用，不改变存储命名空间，无副作用。
func prefix(kind string) string { return strings.TrimSuffix(kind, "s") }

// Normalize 按旧 Freeze 规则规范化访问组；kind 为旧命名空间，obj 为可修改 JSON 对象。
// 服务和兼容适配器调用，无返回值；不落库，不赋予模型/团队/Key权限，也不处理预算。
// 名称空值或非字符串回退到 name/id，数组等仅在 nil 时补齐，updated_at 沿用创建时间而非自动刷新。
func Normalize(kind string, obj map[string]any) {
	delete(obj, "password")
	if obj["id"] == nil {
		obj["id"] = prefix(kind) + "_" + util.CallID()[:12]
	}
	id, _ := obj["id"].(string)
	if obj["created_at"] == nil {
		obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if obj["access_group_id"] == nil {
		obj["access_group_id"] = id
	}
	if name, _ := obj["access_group_name"].(string); name == "" {
		name, _ = obj["name"].(string)
		if name == "" {
			name = id
		}
		obj["access_group_name"] = name
	}
	if obj["description"] == nil {
		obj["description"] = ""
	}
	for _, field := range []string{"models", "access_model_names", "access_mcp_server_ids", "access_agent_ids", "assigned_key_ids", "assigned_team_ids", "access_mcp_servers", "access_agents", "assigned_keys", "assigned_teams"} {
		if obj[field] == nil {
			obj[field] = []any{}
		}
	}
	if obj["updated_at"] == nil {
		obj["updated_at"] = obj["created_at"]
	}
	for _, field := range []string{"created_by", "updated_by", "budget_id"} {
		if _, exists := obj[field]; !exists {
			obj[field] = nil
		}
	}
}
