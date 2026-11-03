package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// rejectLegacyInference 阻止没有执行器的旧管理别名落入公共KV；w/r来自已完成原管理鉴权及JSON解析的请求。
// 仅匹配六个POST操作并写501/no-store，返回是否已处理；不消费正文、不访问存储或上游，不接管版本化与原生协议入口。
func rejectLegacyInference(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	matched := path == "/rag/ingest" || path == "/rag/query" || path == "/ocr"
	if !matched && strings.HasPrefix(path, "/models/") {
		name := strings.TrimPrefix(path, "/models/")
		// 模型标识可能含冒号，操作名以最后一个冒号为界；不接管未注册的多层路径。
		if separator := strings.LastIndex(name, ":"); separator > 0 && !strings.Contains(name[:separator], "/") {
			operation := name[separator+1:]
			matched = operation == "generateContent" || operation == "streamGenerateContent" || operation == "countTokens"
		}
	}
	if !matched {
		return false
	}
	// 不能把识别/检索/生成请求保存成配置后声称执行成功；现有可用原生入口由各自模块负责。
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "this legacy operation has no execution provider")
	return true
}
