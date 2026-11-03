package regression

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestProtocolFalseSuccessOAuth 验证真实公开OAuth占位入口明确失败，既不签发令牌也不访问可观察上游。
// 参数t为测试上下文；前置隔离数据库与本地HTTP供应商，覆盖匿名/登录及表单/坏正文，正常会话仍有效，harness清理全部数据。
func TestProtocolFalseSuccessOAuth(t *testing.T) {
	h := newHarness(t, chatDeployment("protocol-proof"))
	admin := h.adminSession()
	for _, identity := range []struct{ name, key string }{{"anonymous", ""}, {"admin", admin}} {
		for _, tc := range []struct{ method, path, body string }{
			{"POST", "/v1/mcp/server/oauth/missing/token", "grant_type=authorization_code&code=private-code&code_verifier=private-verifier"},
			{"POST", "/v1/mcp/server/oauth/missing/token", `{"code":`},
			{"GET", "/authorize/flow?flow=e2e-fixture", ""},
			{"POST", "/authorize/complete", `{"flow":"e2e-fixture","decision":"complete"}`},
		} {
			t.Run(identity.name+tc.path, func(t *testing.T) {
				// 表单令牌交换使用真实 Content-Type；错误 JSON 原样发送，不能被测试 helper 重新编码。
				req, err := http.NewRequest(tc.method, h.server.URL+tc.path, strings.NewReader(tc.body))
				if err != nil {
					t.Fatal(err)
				}
				if identity.key != "" {
					req.Header.Set("Authorization", "Bearer "+identity.key)
				}
				req.Header.Set("Content-Type", "application/json")
				if strings.HasPrefix(tc.body, "grant_type=") {
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				resp, err := h.server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				r := reply{status: resp.StatusCode, body: raw, headers: resp.Header.Clone()}
				if r.status != 501 || r.json()["error"].(map[string]any)["type"] != "not_implemented" {
					t.Fatalf("未执行授权的端点伪报成功: %s", r.describe())
				}
				for _, forbidden := range []string{`"access_token"`, `"connected":true`, `"status":"ok"`, "private-code", "private-verifier"} {
					if strings.Contains(r.text(), forbidden) {
						t.Fatalf("响应包含假成功或敏感输入: %s", r.describe())
					}
				}
				if r.header("Cache-Control") != "no-store" {
					t.Fatal("授权失败不能缓存")
				}
			})
		}
	}
	if len(h.upstreamCalls()) != 0 {
		t.Fatal("未实现端点不应发送上游请求")
	}
	h.ok(http.MethodGet, "/auth/me", admin, nil)
	// 已移除的真实MCP入口保持404，不能被占位修复重新开放。
	for _, p := range []string{"/v1/mcp", "/v1/mcp/oauth/token"} {
		r := h.do(http.MethodPost, p, admin, map[string]any{})
		if r.status != 404 {
			t.Fatalf("移除的MCP入口被恢复: %s", r.describe())
		}
	}
}

// TestProtocolFalseSuccessCatalog 验证真实目录HTTP读写不能被RPC信封或token路径劫持。
// 参数t为测试上下文；覆盖权限、正常持久化、错误及嵌套元数据，读回验证无篡改，显式删除记录后harness回收schema。
func TestProtocolFalseSuccessCatalog(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	user := h.openScope(t, admin, "protocol-catalog")
	for _, route := range []struct{ base, read, method string }{{"/search_tools", "/search_tools/token", "PUT"}, {"/credentials", "/credentials/by_name/token", "PATCH"}} {
		t.Run(route.base, func(t *testing.T) {
			path := route.base + "/token"
			h.ok("POST", route.base, admin, map[string]any{"id": "token", "credential_name": "token", "search_tool_id": "token", "note": "original", "metadata": map[string]any{"jsonrpc": "2.0"}})
			before := h.ok("GET", route.read, admin, nil).json()
			for _, ident := range []struct {
				name, key string
				status    int
			}{{"anonymous", "", 401}, {"user", user.session, 403}, {"admin", admin, 400}} {
				t.Run(ident.name, func(t *testing.T) {
					r := h.do(route.method, path, ident.key, map[string]any{"jsonrpc": "2.0", "id": 123, "method": "tools/list", "note": "must-not-save"})
					if r.status != ident.status {
						t.Fatalf("RPC目录权限或失败契约: %s", r.describe())
					}
					if ident.status == 400 && r.json()["error"].(map[string]any)["type"] != "unsupported_protocol" {
						t.Fatalf("没有明确拒绝不支持协议: %s", r.describe())
					}
					if !reflect.DeepEqual(before, h.ok("GET", route.read, admin, nil).json()) {
						t.Fatal("失败RPC修改了记录")
					}
				})
			}
			h.ok(route.method, path, admin, map[string]any{"note": "saved"})
			if h.ok("GET", route.read, admin, nil).json()["note"] != "saved" {
				t.Fatal("token资源更新未持久化")
			}
			h.ok("DELETE", path, admin, nil)
			if r := h.do("GET", route.read, admin, nil); r.status != 404 {
				t.Fatalf("token资源删除假成功: %s", r.describe())
			}
		})
	}
}
