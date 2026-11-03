package family

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
)

// serverRuntimeHost仅提供管理身份，其余方法为空；调用存储即panic，证明拒绝在存储边界之前完成。
type serverRuntimeHost struct {
	Host
	allowed bool
}

// RequireManage按allowed返回身份或403；参数是当前测试请求与响应，无账号数据和外部依赖。
func (s serverRuntimeHost) RequireManage(w http.ResponseWriter, _ *http.Request) *auth.Principal {
	if !s.allowed {
		w.WriteHeader(http.StatusForbidden)
		return nil
	}
	return &auth.Principal{UserID: "runtime-test-admin"}
}

// TestServerRuntimeBoundary验证有效/空对象、尾斜杠、原鉴权与非法JSON，不接触真实存储。
// 前置仅身份的Host；期望501不回显秘密或发送Cookie，非法正文仍400，权限先行；无数据需清理。
func TestServerRuntimeBoundary(t *testing.T) {
	for _, op := range []struct{ method, path string }{
		{"GET", "/server/health"}, {"POST", "/server/id/user-credential"}, {"DELETE", "/server/id/user-credential"},
		{"GET", "/server/id/user-env-vars"}, {"POST", "/server/id/user-env-vars"}, {"DELETE", "/server/id/user-env-vars"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			for _, suffix := range []string{"", "/"} {
				for _, body := range []string{"", `{}`, `{"credentials":{"api_key":"private-test"},"env_vars":{"TOKEN":"private-test"}}`} {
					w := httptest.NewRecorder()
					ServeMgmt(serverRuntimeHost{allowed: true}, w, httptest.NewRequest(op.method, op.path+suffix, strings.NewReader(body)))
					var data struct {
						Error struct {
							Type string `json:"type"`
						} `json:"error"`
					}
					err := json.Unmarshal(w.Body.Bytes(), &data)
					if w.Code != 501 || err != nil || data.Error.Type != "unsupported_operation" || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private-test") || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Location") != "" {
						t.Fatalf("未实现能力拒绝失败: %d %s", w.Code, w.Body)
					}
				}
			}
			for _, tc := range []struct {
				allowed bool
				body    string
				status  int
			}{{false, `{`, 403}, {true, `{`, 400}, {true, `null`, 400}, {true, `{} {}`, 400}} {
				w := httptest.NewRecorder()
				ServeMgmt(serverRuntimeHost{allowed: tc.allowed}, w, httptest.NewRequest(op.method, op.path, strings.NewReader(tc.body)))
				if w.Code != tc.status {
					t.Fatal("权限或JSON边界变化", w.Code, w.Body)
				}
			}
		})
	}
}

// TestServerRuntimePreservesOtherRoutes验证名称碰撞、方法、空ID和相似子路径不能扩大匹配。
// 纯匹配与未处理响应测试，无HTTP服务或存储；返回false且不得写响应，无需清理数据。
func TestServerRuntimePreservesOtherRoutes(t *testing.T) {
	for _, op := range []struct{ method, path string }{
		{"GET", "/server/user-credential"}, {"GET", "/server/user-env-vars"}, {"POST", "/server"},
		{"POST", "/server/health"}, {"PUT", "/server/id/user-credential"}, {"GET", "/server/id/user-credential"},
		{"PUT", "/server/id/user-env-vars"}, {"POST", "/server//user-env-vars"}, {"POST", "/other/id/user-env-vars"},
		{"POST", "/server/id/user-env-vars/extra"}, {"POST", "/server/id/oauth-user-credential"},
		{"GET", "/auth/enterprise/saml/login"}, {"GET", "/v1/skills"}, {"GET", "/v1/mcp/server/health"},
		{"GET", "/server/id/unknown"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			if rejectServerRuntime(w, httptest.NewRequest(op.method, op.path, nil)) || w.Body.Len() != 0 || len(w.Header()) != 0 {
				t.Fatal("不相关请求被误拦截", op)
			}
		})
	}
}
