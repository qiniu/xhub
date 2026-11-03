package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// rejectProviderBatch 拒绝落入旧管理目录的供应商批处理操作；w/r 为已通过管理鉴权和JSON校验的响应与请求。
// 返回是否已写501；仅匹配四个已登记模板，不读取配置或调用上游。标准批处理及OpenAI数据面由原处理链负责。
func rejectProviderBatch(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/"), "/")
	if len(parts) < 3 || len(parts) > 5 || parts[0] == "" || parts[1] != "v1" || parts[2] != "batches" {
		return false
	}
	matched := false
	switch len(parts) {
	case 3:
		matched = r.Method == http.MethodGet || r.Method == http.MethodPost
	case 4:
		matched = parts[3] != "" && r.Method == http.MethodGet
	case 5:
		matched = parts[3] != "" && parts[4] == "cancel" && r.Method == http.MethodPost
	}
	if !matched {
		return false
	}
	// 供应商前缀只是路由参数，不是可供任务读写的公共KV命名空间；不能伪造任务状态。
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "provider batch operations are not implemented by this legacy endpoint")
	return true
}
