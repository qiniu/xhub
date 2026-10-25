package family

import (
	"strings"

	"github.com/sunqirui1987/xhub/internal/credentialfields"
	"github.com/sunqirui1987/xhub/internal/gateway/suppliers"
)

// mergeSearchToolPatch 合并已保存搜索配置；dst 是存储读回对象，body 是更新正文，供通用目录更新调用。
// 无返回值，修改 dst；省略、留空或回传掩码的秘密保留原值，普通参数只覆盖明确提交的字段，避免编辑地址时丢失 Key。
func mergeSearchToolPatch(dst, body map[string]any) {
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
	mergeSearchParams(params, incoming)
	dst["litellm_params"] = params
}

// mergeSearchParams 递归合并搜索参数；dst为已保存字段，incoming为补丁，返回无并修改dst。
// 目录编辑回传嵌套headers的掩码时必须保留原秘密；数组含掩码时保留旧数组，避免按位置错误关联秘密。
func mergeSearchParams(dst, incoming map[string]any) {
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
			mergeSearchParams(current, nested)
			dst[key] = current
			continue
		}
		if _, array := value.([]any); array && searchValueHasMask(value) {
			continue
		}
		dst[key] = value
	}
}

// searchValueHasMask 判断JSON复合值是否含回传秘密掩码；仅用于保留旧数组，输入只读，无外部副作用。
func searchValueHasMask(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, "**")
	case map[string]any:
		for _, child := range typed {
			if searchValueHasMask(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if searchValueHasMask(child) {
				return true
			}
		}
	}
	return false
}

// redactCatalogObject 为目录响应选择秘密脱敏规则；kind/obj 定位资源和原始对象，showValues 沿用凭据契约。
// 返回响应副本，供创建/读取/更新调用；搜索配置递归遮盖秘密且不修改存储对象，其他资源保持既有行为。
func redactCatalogObject(kind string, obj map[string]any, showValues bool) map[string]any {
	if kind == "search_tools" || kind == "search_tool" || kind == "search-tools" {
		return redactSearchFields(obj).(map[string]any)
	}
	if kind == "credentials" || kind == "credential" {
		return suppliers.Redact(obj, showValues)
	}
	return obj
}

// redactCatalogList 对目录列表逐项应用响应脱敏；参数为资源种类、存储结果与凭据显示策略，返回独立列表。
// 列表路由调用，无持久化副作用；不把掩码写回原对象，也不改变其他种类的返回字段。
func redactCatalogList(kind string, list []map[string]any, showValues bool) []map[string]any {
	out := make([]map[string]any, len(list))
	for i, item := range list {
		out[i] = redactCatalogObject(kind, item, showValues)
	}
	return out
}

// redactSearchFields 复制搜索配置中的JSON值并递归遮盖秘密字段；value 来自已保存配置，返回可安全呈现的同形值。
// 搜索响应专用，非空秘密用固定掩码，空秘密保持空；数组和嵌套对象也处理，避免旧配置中的 headers 泄漏。
func redactSearchFields(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if credentialfields.Sensitive(key) && item != nil && item != "" {
				out[key] = "********"
			} else {
				out[key] = redactSearchFields(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactSearchFields(item)
		}
		return out
	default:
		return value
	}
}
