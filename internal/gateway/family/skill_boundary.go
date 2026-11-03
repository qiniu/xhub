package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// skillResourceAction 按注册的Skill详情路径解析动作与ID；method/path来自已鉴权请求，返回命中标记供parseMgmt优先采用。
// GET只读、DELETE只删除路径资源；保留词也是合法ID，其他方法和路径继续原解析，不访问存储或改变权限。
func skillResourceAction(method, path string) (action, id string, matched bool) {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "v1" || parts[2] != "skills" || parts[3] == "" {
		return "", "", false
	}
	switch method {
	case http.MethodGet:
		return "info", parts[3], true
	case http.MethodDelete:
		return "delete", parts[3], true
	default:
		return "", "", false
	}
}

// rejectSkillArchive 明确拒绝尚未实现的Skill归档下载；w/r为已通过管理认证和JSON检查的请求，由管理适配器调用。
// 仅精确GET归档路径写501并返回true；不读取同名KV、不泄漏内容、不联网，其他路径返回false继续原流程。
func rejectSkillArchive(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	if r.Method != http.MethodGet || len(parts) != 5 || parts[0] != "" || parts[1] != "v1" || parts[2] != "skills" || parts[3] == "" || parts[4] != "archive" {
		return false
	}
	// 普通配置名也可为archive，下载不能误读它并伪装成成功；无归档生成器时不尝试拼造文件。
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "skill archive generation is not implemented")
	return true
}
