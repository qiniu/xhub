package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// rejectProviderFile 拒绝落入旧管理目录的供应商文件操作；w/r 为已通过管理鉴权和JSON校验的响应与请求。
// 返回是否已写501；仅匹配五个已登记模板，不读取配置或调用上游。标准文件及OpenAI数据面由原处理链负责。
func rejectProviderFile(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/"), "/")
	if len(parts) < 3 || len(parts) > 5 || parts[0] == "" || parts[1] != "v1" || parts[2] != "files" {
		return false
	}
	matched := false
	switch len(parts) {
	case 3:
		matched = r.Method == http.MethodGet || r.Method == http.MethodPost
	case 4:
		matched = parts[3] != "" && (r.Method == http.MethodGet || r.Method == http.MethodDelete)
	case 5:
		matched = parts[3] != "" && parts[4] == "content" && r.Method == http.MethodGet
	}
	if !matched {
		return false
	}
	// 供应商前缀只是路由参数，不是可供文件读写的公共KV命名空间；不能把公共配置当作文件内容或删除目标。
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "provider file operations are not implemented by this legacy endpoint")
	return true
}
