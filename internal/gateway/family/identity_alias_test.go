package family

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIdentityAliasRejects 验证22条操作、尾斜杠和无存储环境下的失败契约；仅内存HTTP，无需清理外部数据。
func TestIdentityAliasRejects(t *testing.T) {
	for _, route := range []string{
		"GET /authorize",
		"GET /discover",
		"GET /enabled",
		"GET /invitation/info",
		"GET /jwt/key/mapping/info",
		"GET /jwt/key/mapping/list",
		"GET /onboarding/get_token",
		"GET /token/generate",
		"POST /delete",
		"POST /introspect",
		"POST /invitation/delete",
		"POST /invitation/new",
		"POST /invitation/update",
		"POST /jwt/key/mapping/delete",
		"POST /jwt/key/mapping/new",
		"POST /jwt/key/mapping/update",
		"POST /onboarding/claim_token",
		"POST /register",
		"POST /revoke",
		"POST /test/connection",
		"POST /token",
		"POST /v3/login/exchange",
	} {
		for _, suffix := range []string{"", "/"} {
			t.Run(route+suffix, func(t *testing.T) {
				method, path, _ := strings.Cut(route, " ")
				w := httptest.NewRecorder()
				writeCatalogStore(nil, w, httptest.NewRequest(method, path+suffix, nil), map[string]any{"token": "private-value"})
				if w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"type":"unsupported_operation"`) || strings.Contains(w.Body.String(), "private-value") {
					t.Fatalf("旧身份操作不得读写或返回秘密: status=%d body=%s", w.Code, w.Body.String())
				}
			})
		}
	}
}

// TestIdentityAliasBoundaries 验证真实身份路径、错误方法、相似名称不被扩大拒绝；JSON-RPC仍先返回400，测试不访问数据库。
func TestIdentityAliasBoundaries(t *testing.T) {
	for _, route := range []string{"POST /v2/login", "POST /v3/login", "GET /auth/me", "POST /auth/enterprise/saml/acs", "GET /authorize/flow", "POST /authorize/complete", "POST /v1/mcp/server/oauth/id/token", "POST /user/set_password", "GET /token", "POST /token/extra", "GET /jwt/key/mapping/list/extra", "POST /invitation/newer", "POST /register//"} {
		t.Run(route, func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			w := httptest.NewRecorder()
			if rejectIdentityAlias(w, httptest.NewRequest(method, path, nil)) || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "" {
				t.Fatal("误伤真实服务或相似路径", route)
			}
		})
	}
	w := httptest.NewRecorder()
	writeCatalogStore(nil, w, httptest.NewRequest("POST", "/token", nil), map[string]any{"jsonrpc": "2.0"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_protocol") {
		t.Fatal("RPC校验顺序改变", w.Body.String())
	}
}
