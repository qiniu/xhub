package family

import "strings"

// unsupportedOperationalPath 判断没有实际执行器的旧运维目录入口；path为请求路径，返回是否应明确拒绝。
// 只由ServeMgmt在原鉴权之后调用，不改模块路由注册，也不匹配真正的存活、就绪或模型连接探测。
// 采用明确路径表防止把整个health/debug前缀误判为不支持；忽略末尾斜线，无存储或运行时副作用。
func unsupportedOperationalPath(path string) bool {
	switch strings.TrimSuffix(path, "/") {
	case "/coordination_redis/settings", "/coordination_redis/settings/test",
		"/debug/asyncio-tasks", "/debug/memory/details", "/debug/memory/summary", "/debug/memory/gc/configure",
		"/memory-usage", "/memory-usage-in-mem-cache", "/memory-usage-in-mem-cache-items",
		"/health/backlog", "/health/drain", "/health/history", "/health/license", "/health/shared-status":
		return true
	default:
		return false
	}
}
