package family

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
)

// serverOAuthHost仅提供管理身份；其余Host方法为空，若拒绝路径误访问存储则测试立即失败。
type serverOAuthHost struct {
	Host
	allowed bool
}

// RequireManage按allowed返回测试身份或403；用于验证拒绝授权流程不能绕过原管理权限，无外部数据。
func (s serverOAuthHost) RequireManage(w http.ResponseWriter, r *http.Request) *auth.Principal {
	if !s.allowed {
		w.WriteHeader(http.StatusForbidden)
		return nil
	}
	return &auth.Principal{UserID: "local-admin"}
}

// TestServerOAuthBoundary验证七种请求、尾斜杠、损坏/表单正文均明确失败且无秘密回显、令牌或重定向。
// 前置无数据库的Host；走真实ServeMgmt，授权拒绝不得访问存储，无数据需要清理。
func TestServerOAuthBoundary(t *testing.T) {
	for _, op := range []struct{ method, path string }{
		{"POST", "/server/oauth/session"}, {"GET", "/server/oauth/id/authorize"},
		{"POST", "/server/oauth/id/register"}, {"POST", "/server/oauth/id/token"},
		{"POST", "/server/id/oauth-user-credential"}, {"DELETE", "/server/id/oauth-user-credential"},
		{"GET", "/server/id/oauth-user-credential/status"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			for _, suffix := range []string{"", "/"} {
				for _, body := range []string{"", `{"access_token":"private-test-secret"}`, `{"code":`, "code=private-test-secret"} {
					w := httptest.NewRecorder()
					ServeMgmt(serverOAuthHost{allowed: true}, w, httptest.NewRequest(op.method, op.path+suffix, strings.NewReader(body)))
					var data struct {
						Error struct {
							Type string `json:"type"`
						} `json:"error"`
					}
					err := json.Unmarshal(w.Body.Bytes(), &data)
					if w.Code != 501 || err != nil || data.Error.Type != "unsupported_operation" || strings.Contains(w.Body.String(), "private-test") || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Location") != "" || w.Header().Get("Set-Cookie") != "" {
						t.Fatalf("未实现授权不得伪造成功/泄露秘密: %d %s", w.Code, w.Body)
					}
				}
			}
			w := httptest.NewRecorder()
			ServeMgmt(serverOAuthHost{}, w, httptest.NewRequest(op.method, op.path, nil))
			if w.Code != 403 {
				t.Fatal("管理鉴权必须先于能力拒绝", w.Code)
			}
		})
	}
}

// TestServerOAuthMatcherPreservesOtherRoutes验证名称碰撞、错误方法、相似路径及企业SSO不被扩大拦截。
// 纯路径判断无外部依赖；false时响应保持未写入，无数据清理。
func TestServerOAuthMatcherPreservesOtherRoutes(t *testing.T) {
	for _, op := range []struct{ method, path string }{
		{"GET", "/server/token"}, {"POST", "/server"}, {"GET", "/server/oauth"},
		{"PUT", "/server/oauth/session"}, {"GET", "/server/oauth/id/token"},
		{"POST", "/server/oauth/id/authorize"}, {"POST", "/server/id/oauth-user-credential/status"},
		{"GET", "/server/id/user-credential"}, {"GET", "/auth/enterprise/saml/login"},
		{"POST", "/v1/mcp/server/oauth/id/token"}, {"POST", "/credentials/token"},
		{"POST", "/server/oauth//token"}, {"POST", "/server//oauth-user-credential"},
		{"POST", "/server/oauth/id/token/extra"}, {"GET", "/server/id/oauth-user-credential/other"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			if rejectServerOAuth(w, httptest.NewRequest(op.method, op.path, nil)) || w.Body.Len() != 0 || len(w.Header()) != 0 {
				t.Fatal("不相关入口被误拦截", op)
			}
		})
	}
}
