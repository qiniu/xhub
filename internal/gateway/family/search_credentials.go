package family

import (
	"github.com/sunqirui1987/xhub/internal/gateway/searchconfig"
	"github.com/sunqirui1987/xhub/internal/gateway/suppliers"
)

// redactCatalogObject 为目录响应选择秘密脱敏规则；kind/obj 定位资源和原始对象，showValues 沿用凭据契约。
// 返回响应副本，供创建/读取/更新调用；搜索配置递归遮盖秘密且不修改存储对象，其他资源保持既有行为。
func redactCatalogObject(kind string, obj map[string]any, showValues bool) map[string]any {
	if kind == "search_tools" || kind == "search_tool" || kind == "search-tools" {
		return searchconfig.Redact(obj).(map[string]any)
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
