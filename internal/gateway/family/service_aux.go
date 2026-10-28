package family

import (
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/util"
	"net/http"
	"strings"
)

// unsupportedServiceAuxRoutes 是已审计但无执行器的精确旧接口，不包含基本配置CRUD。
// 参数仅占一个非空路径段；策略配置服务不能代替解析/测试、版本历史或附件执行。
var unsupportedServiceAuxRoutes = []string{
	"DELETE /policies/attachments/{attachment_id}",
	"DELETE /policies/name/{policy_name}/all-versions",
	"GET /credentials/by_model/{model_id}",
	"GET /credentials/migrate-encryption/check",
	"GET /policies/attachments/list",
	"GET /policies/attachments/{attachment_id}",
	"GET /policies/compare",
	"GET /policies/name/{policy_name}/versions",
	"GET /policies/usage/overview",
	"GET /policies/{policy_id}/resolved-guardrails",
	"GET /policy/templates",
	"GET /prompts/{prompt_id}/versions",
	"POST /credentials/migrate-encryption",
	"POST /policies/attachments",
	"POST /policies/attachments/estimate-impact",
	"POST /policies/name/{policy_name}/versions",
	"POST /policies/resolve",
	"POST /policies/test-pipeline",
	"POST /policy/templates/enrich",
	"POST /policy/templates/enrich/stream",
	"POST /policy/templates/suggest",
	"POST /policy/templates/test",
	"POST /policy/test",
	"POST /policy/validate",
	"POST /utils/test_policies_and_guardrails",
	"PUT /policies/{policy_id}/status",
}

// rejectServiceAux 阻止旧辅助操作误入公共KV；w/r来自既有管理员鉴权及JSON/RPC校验之后。
// 返回是否处理；匹配时仅写501/no-store，不访问存储或上游，不改变正常CRUD、权限、预算与护栏运行时。
func rejectServiceAux(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.Contains(path, "//") {
		return false
	}
	for _, route := range unsupportedServiceAuxRoutes {
		method, pattern, _ := strings.Cut(route, " ")
		// PathMatch支持尾部通配，辅助入口不需要这种语义；先限制段数，避免把额外子路径误认为已注册操作。
		if r.Method != method || len(strings.Split(path, "/")) != len(strings.Split(pattern, "/")) || !catalog.PathMatch(pattern, path) {
			continue
		}
		w.Header().Set("Cache-Control", "no-store")
		util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "this service operation is not implemented; configuration CRUD remains available")
		return true
	}
	return false
}
