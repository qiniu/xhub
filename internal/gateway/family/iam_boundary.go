package family

import "strings"

// isIAMFallbackPath 判断已落入通用管理处理器的请求是否属于组织/团队；path 为原请求路径。
// ServeMgmt 在原管理鉴权之后调用；true 表示缺少 IAM 执行器，必须拒绝，不能写 KV 假装完成。
// 主接口由 identity 模块优先挂载，不经过此处；仅跳过旧目录认可的版本前缀，无存储副作用。
func isIAMFallbackPath(path string) bool {
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		switch part {
		case "v1", "v2", "v1beta", "api":
			continue
		default:
			return part == "organization" || part == "team"
		}
	}
	return false
}
