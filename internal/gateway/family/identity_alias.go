package family

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// POST /delete 已由缓存模块接管，不属于身份别名；其真实拒绝契约由缓存回归覆盖。
// identityAliases 只登记未接入身份执行器的旧目录操作；不能按路径前缀拒绝，否则会误伤真实登录和企业SSO。
var identityAliases = map[string]struct{}{
	"GET /authorize":               {},
	"GET /discover":                {},
	"GET /enabled":                 {},
	"GET /invitation/info":         {},
	"GET /jwt/key/mapping/info":    {},
	"GET /jwt/key/mapping/list":    {},
	"GET /onboarding/get_token":    {},
	"GET /token/generate":          {},
	"POST /introspect":             {},
	"POST /invitation/delete":      {},
	"POST /invitation/new":         {},
	"POST /invitation/update":      {},
	"POST /jwt/key/mapping/delete": {},
	"POST /jwt/key/mapping/new":    {},
	"POST /jwt/key/mapping/update": {},
	"POST /onboarding/claim_token": {},
	"POST /register":               {},
	"POST /revoke":                 {},
	"POST /test/connection":        {},
	"POST /token":                  {},
	"POST /v3/login/exchange":      {},
}

// rejectIdentityAlias 接收已鉴权且完成JSON校验的请求，仅拦截精确的旧身份操作。
// 由通用目录写入入口调用；命中时写入不缓存的501并返回true，不访问存储或签发凭据。
// 允许单个尾斜杠；方法不同、额外子路径和真实身份服务返回false，由原处理链决定结果。
func rejectIdentityAlias(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := identityAliases[r.Method+" "+strings.TrimSuffix(r.URL.Path, "/")]; !ok {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_operation", "This legacy identity operation has no executor; use the supported authentication endpoints")
	return true
}
