package searchconfig

import (
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/credentialfields"
	"github.com/sunqirui1987/xhub/internal/util"
)

// Normalize 为搜索工具配置补齐旧默认字段；kind 保留历史 ID 前缀，obj 为可修改对象。
// 服务及旧 Freeze 适配器调用；仅 nil 字段补默认，不覆盖零、空串和扩展字段，不写存储。
func Normalize(kind string, obj map[string]any) {
	delete(obj, "password")
	if obj["id"] == nil {
		obj["id"] = strings.TrimSuffix(kind, "s") + "_" + util.CallID()[:12]
	}
	if obj["created_at"] == nil {
		obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if obj["search_tool_id"] == nil {
		id, _ := obj["id"].(string)
		obj["search_tool_id"] = id
	}
	if obj["search_tool_name"] == nil {
		obj["search_tool_name"] = ""
	}
}

// mergePatch 合并已保存搜索配置；dst 是存储读回对象，body 是更新正文，供配置服务更新调用。
// 无返回值，修改 dst；省略、留空或回传掩码的秘密保留原值，普通参数只覆盖明确提交的字段，避免编辑地址时丢失 Key。
func mergePatch(dst, body map[string]any) {
	for key, value := range body {
		if key != "password" && key != "litellm_params" {
			dst[key] = value
		}
	}
	incoming, ok := body["litellm_params"].(map[string]any)
	if !ok {
		return
	}
	params, _ := dst["litellm_params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	mergeParams(params, incoming)
	dst["litellm_params"] = params
}

// mergeParams 递归合并搜索参数；dst为已保存字段，incoming为补丁，返回无并修改dst。
// 目录编辑回传嵌套headers的掩码时必须保留原秘密；数组含掩码时保留旧数组，避免按位置错误关联秘密。
func mergeParams(dst, incoming map[string]any) {
	for key, value := range incoming {
		if credentialfields.Sensitive(key) {
			secret, _ := value.(string)
			if strings.TrimSpace(secret) == "" || strings.Contains(secret, "**") {
				continue
			}
		}
		if nested, ok := value.(map[string]any); ok {
			current, _ := dst[key].(map[string]any)
			if current == nil {
				current = map[string]any{}
			}
			mergeParams(current, nested)
			dst[key] = current
			continue
		}
		if _, array := value.([]any); array && valueHasMask(value) {
			continue
		}
		dst[key] = value
	}
}

// valueHasMask 判断JSON复合值是否含回传秘密掩码；仅用于保留旧数组，输入只读，无外部副作用。
func valueHasMask(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, "**")
	case map[string]any:
		for _, child := range typed {
			if valueHasMask(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if valueHasMask(child) {
				return true
			}
		}
	}
	return false
}

// Redact 复制搜索配置中的JSON值并递归遮盖秘密字段；value 来自已保存配置，返回可安全呈现的同形值。
// 搜索响应专用，非空秘密用固定掩码，空秘密保持空；数组和嵌套对象也处理，避免旧配置中的 headers 泄漏。
func Redact(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if credentialfields.Sensitive(key) && item != nil && item != "" {
				out[key] = "********"
			} else {
				out[key] = Redact(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = Redact(item)
		}
		return out
	default:
		return value
	}
}
