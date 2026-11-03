package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// rejectMedicalAlias 拒绝没有分析执行器的旧入口；w/r来自已通过管理鉴权和JSON校验的请求。
// 返回是否已写501，仅匹配两个POST模板，不消费正文、不访问存储或上游；由通用目录适配器调用。
func rejectMedicalAlias(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	matched := path == "/comprehendmedical"
	if strings.HasPrefix(path, "/comprehendmedical/") {
		operation := strings.TrimPrefix(path, "/comprehendmedical/")
		matched = operation != "" && !strings.Contains(operation, "/")
	}
	if !matched {
		return false
	}
	// 操作名不能进入配置CRUD解释器，尤其info/delete不得读取或删除同名公共配置。
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "Comprehend Medical operations are not implemented by this legacy endpoint")
	return true
}
