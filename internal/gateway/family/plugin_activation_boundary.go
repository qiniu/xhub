package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// rejectPluginActivation 在已鉴权且JSON已解析的管理请求中拒绝未实现的插件启停；w/r为响应与请求。
// 命中两个精确操作时写501并返回true，否则返回false且不写响应；存储之前调用，避免enable/disable误作公共记录ID。
// 当前没有插件加载或发布执行器，不能仅修改enabled字段并宣称完成启停。
func rejectPluginActivation(w http.ResponseWriter, r *http.Request) bool {
	if !isPluginActivationOperation(r.Method, r.URL.Path) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "Claude Code plugin activation is not implemented; no plugin state was changed")
	return true
}

// isPluginActivationOperation 按方法、完整路径和非空插件名识别启停动作，供管理适配器使用。
// 参数来自当前HTTP请求；返回是否应拒绝，允许一个尾斜杠；不拦截同名插件普通CRUD、其他前缀或未知子操作，无副作用。
func isPluginActivationOperation(method, path string) bool {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	return method == http.MethodPost && len(parts) == 5 && parts[0] == "" && parts[1] == "claude-code" && parts[2] == "plugins" && parts[3] != "" && (parts[4] == "enable" || parts[4] == "disable")
}
