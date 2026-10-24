package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOnboardingRejectsUnimplementedOAuth 验证没有授权执行器的保留入口不能签发随机token或宣称已连接。
// 参数t为测试上下文；零Server证明不需要或触碰存储，覆盖空、正常形状、错误及表单输入，无外部资源。
func TestOnboardingRejectsUnimplementedOAuth(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		name, method, path string
		handle             http.HandlerFunc
	}{
		{"token", "POST", "/v1/mcp/server/oauth/missing/token", s.mcpOAuthToken},
		{"flow", "GET", "/authorize/flow?flow=e2e-fixture", s.authorizeFlow},
		{"complete", "POST", "/authorize/complete", s.authorizeComplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, body := range []string{"", `{"code":"not-real","state":"not-real"}`, "grant_type=authorization_code&code=private-code&code_verifier=private-verifier", `{"flow":`} {
				w := httptest.NewRecorder()
				tc.handle(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(body)))
				if w.Code != 501 || !strings.Contains(w.Body.String(), `"type":"not_implemented"`) {
					t.Errorf("未执行授权不能成功: %d %s", w.Code, w.Body.String())
				}
				for _, bad := range []string{`"access_token"`, `"connected":true`, `"status":"ok"`, "private-code", "private-verifier"} {
					if strings.Contains(w.Body.String(), bad) {
						t.Errorf("响应伪造成功或泄漏授权数据: %s", w.Body.String())
					}
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Error("授权响应缺少禁止缓存")
				}
			}
		})
	}
}
