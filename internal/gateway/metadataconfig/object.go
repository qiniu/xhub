package metadataconfig

import (
	"github.com/sunqirui1987/xhub/internal/util"
	"strings"
	"time"
)

// Supports 返回kind是否属于标签/终端客户配置；HTTP适配器用它限定委派范围，不开放其他目录。
func Supports(kind string) bool {
	return kind == "tag" || kind == "tags" || kind == "customer" || kind == "end_user"
}

// prefix 保留标签复数的旧自动ID规则；内部Kind已限定，无存储或授权副作用。
func prefix(kind string) string { return strings.TrimSuffix(kind, "s") }

// identityField 返回各空间的旧业务标识字段；服务建立/更新对象时使用，不合并账号身份。
func identityField(kind string) string {
	if kind == "tag" || kind == "tags" {
		return "name"
	}
	return "user_id"
}

// aliasField 返回旧显示别名字段；复数tags用name，单数tag仍用tag_alias，不修正历史命名。
func aliasField(kind string) string {
	if kind == "tags" {
		return "name"
	}
	return prefix(kind) + "_alias"
}

// Normalize 补齐标签/客户配置旧字段；kind限定四个旧空间，obj为待规范化对象，无返回值。
// 服务与兼容Freeze调用；只补nil，保留零值/未知字段，去掉顶层密码，不写数据库，不解释预算或封禁。
func Normalize(kind string, obj map[string]any) {
	delete(obj, "password")
	if obj["id"] == nil {
		obj["id"] = prefix(kind) + "_" + util.CallID()[:12]
	}
	id, _ := obj["id"].(string)
	if obj["created_at"] == nil {
		obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if obj[identityField(kind)] == nil {
		obj[identityField(kind)] = id
	}
	if kind == "tag" || kind == "tags" {
		if n, _ := obj["tag_name"].(string); n != "" {
			obj["name"] = n
		}
		if obj["tag_name"] == nil {
			obj["tag_name"] = obj["name"]
		}
	} else {
		if obj["blocked"] == nil {
			obj["blocked"] = false
		}
		if _, ok := obj["max_budget"]; !ok {
			obj["max_budget"] = nil
		}
	}
	if obj["spend"] == nil {
		obj["spend"] = 0
	}
}
