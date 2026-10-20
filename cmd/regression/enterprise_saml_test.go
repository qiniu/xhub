package regression

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/crewjam/saml"
	"github.com/sunqirui1987/xhub/cmd/regression/testsupport/samltest"
)

// TestEnterpriseSAMLHTTPFlow 用独立 PostgreSQL schema、真实路由和本地签名 IdP 验证 HTTP/HTTPS 完整登录。
// 覆盖默认 HTTPS、显式 HTTP、元数据导出、稳定身份和普通权限、篡改/未知身份/重放/无 Cookie；结束删配置并由 harness 回收 schema。
func TestEnterpriseSAMLHTTPFlow(t *testing.T) {
	for _, variant := range []struct{ secure, expired bool }{{false, false}, {true, false}, {false, true}} {
		secure := variant.secure
		name := "http-explicit"
		if secure {
			name = "https-default"
		}
		if variant.expired {
			name = "http-expired-explicit"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.gw.Cfg.GeneralSettings.MasterKey = strings.Repeat("s", 32)
			if secure {
				h.server.Close()
				h.server = httptest.NewTLSServer(h.gw.Handler())
			}
			admin := h.adminSession()
			idp := samltest.New(t)
			if !secure {
				idp = samltest.NewHTTP(t)
			}
			if variant.expired {
				idp = samltest.NewExpired(t)
			}
			org := h.ok("POST", "/organization/new", admin, map[string]any{"organization_alias": "SAML regression"}).json()
			orgID := stringField(org, "organization_id")
			dir := h.ok("POST", "/admin/directory/connections", admin, map[string]any{"organization_id": orgID, "name": "SAML directory"}).json()
			connection := stringField(dir["connection"].(map[string]any), "id")
			h.ok("PUT", "/admin/directory/connections/"+connection, admin, map[string]any{"enabled": true})
			h.ok("POST", "/scim/v2/connections/"+connection+"/Users", stringField(dir, "token"), map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, "externalId": "employee-sub", "userName": "saml-regression@example.com", "active": true})
			detail := h.ok("GET", "/admin/directory/connections/"+connection, admin, nil).json()
			userID := stringField(detail["users"].([]any)[0].(map[string]any), "user_id")
			payload := map[string]any{"organization_id": orgID, "protocol": "saml", "name": "SAML test", "client_id": "urn:xhub:saml:regression:" + connection, "redirect_uri": h.server.URL + "/auth/enterprise/saml/acs", "return_uri": h.server.URL + "/ui/login", "idp_metadata": idp.Metadata(), "enabled": true}
			if !secure {
				// 先使用合法 HTTPS SP 地址，单独验证默认策略拒绝 HTTP IdP，避免被 SP 地址校验掩盖。
				payload["redirect_uri"], payload["return_uri"] = "https://console.example/auth/enterprise/saml/acs", "https://console.example/ui/login"
				if got := h.do("POST", "/admin/enterprise/sso", admin, payload); got.status != 400 {
					t.Fatalf("默认配置必须单独拒绝 HTTP IdP，得到 %d: %s", got.status, got.text())
				}
				payload["redirect_uri"], payload["return_uri"] = h.server.URL+"/auth/enterprise/saml/acs", h.server.URL+"/ui/login"
				if got := h.do("POST", "/admin/enterprise/sso", admin, payload); got.status != 400 {
					t.Fatalf("默认配置必须拒绝 HTTP，得到 %d: %s", got.status, got.text())
				}
				payload["allow_http"] = true
			}
			if variant.expired {
				payload["allow_expired_idp_certificate"] = true
			}
			created := h.ok("POST", "/admin/enterprise/sso", admin, payload)
			if strings.Contains(created.text(), "PRIVATE KEY") || strings.Contains(created.text(), `"secret"`) {
				t.Fatal("管理响应泄漏 SAML 私钥", created.text())
			}
			connection = stringField(created.json(), "connection_id")
			exported := h.ok("GET", "/admin/enterprise/sso/"+connection+"/metadata", admin, nil)
			metadata := stringField(exported.json(), "metadata")
			if err := idp.Register(metadata); err != nil {
				t.Fatal("真实元数据无法注册 IdP", err, metadata)
			}
			public := h.ok("GET", "/auth/enterprise/"+connection+"/saml/metadata", "", nil)
			var exportedSP, publicSP saml.EntityDescriptor
			errA, errB := xml.Unmarshal([]byte(metadata), &exportedSP), xml.Unmarshal(public.body, &publicSP)
			if errA != nil || errB != nil || exportedSP.EntityID != publicSP.EntityID || strings.Contains(metadata, "PRIVATE KEY") || !strings.Contains(metadata, h.server.URL+"/auth/enterprise/saml/acs") {
				t.Fatal("管理和公开元数据必须一致且仅暴露公开材料")
			}
			for _, mode := range []string{"valid", "unknown", "tamper", "missing-browser", "foreign-browser", "replay"} {
				t.Run(mode, func(t *testing.T) {
					start := samlRequest(t, h.server.Client(), "GET", h.server.URL+"/auth/enterprise/"+connection+"/start", nil, nil)
					if start.status != 302 {
						t.Fatal("发起 SAML 登录失败", start.status, start.text())
					}
					cookies := (&http.Response{Header: start.headers}).Cookies()
					if len(cookies) != 1 || cookies[0].Secure != secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
						t.Fatal("浏览器事务 Cookie 属性错误", cookies)
					}
					form, err := idp.Response(start.headers.Get("Location"), "employee-sub", mode)
					if err != nil {
						t.Fatal("AuthnRequest 签名或生成断言失败", err)
					}
					// ACS 跨站 POST 刻意不带 Cookie，必须只暂存身份，直到本站 GET 检验原浏览器。
					post := samlRequest(t, h.server.Client(), "POST", form.URL, url.Values{"SAMLResponse": {form.SAMLResponse}, "RelayState": {form.RelayState}}, nil)
					if mode == "tamper" {
						if post.status != 401 || hasSessionCookie(post) {
							t.Fatal("篡改签名必须拒绝且不签发会话", post.status, post.text())
						}
						return
					}
					if post.status != 303 || hasSessionCookie(post) {
						t.Fatal("ACS 必须无会话地跳转到固定 GET 收尾", post.status, post.text())
					}
					if mode == "missing-browser" {
						cookies = nil
					} else if mode == "foreign-browser" {
						// 另一浏览器即使已知 RelayState 和收尾地址，也没有原事务随机绑定值。
						cookies[0].Value = "another-browser-transaction-binding"
					}
					finish := samlRequest(t, h.server.Client(), "GET", post.headers.Get("Location"), nil, cookies)
					if mode == "unknown" || mode == "missing-browser" || mode == "foreign-browser" {
						if finish.status != 401 || hasSessionCookie(finish) {
							t.Fatal("未知身份或缺失浏览器绑定必须拒绝", finish.status, finish.text())
						}
						return
					}
					if finish.status != 302 || finish.headers.Get("Location") != h.server.URL+"/ui/login" || !hasSessionCookie(finish) {
						t.Fatal("合法 SAML 身份未完成会话登录", finish.status, finish.text())
					}
					var session string
					for _, cookie := range (&http.Response{Header: finish.headers}).Cookies() {
						if cookie.Name == "token" {
							if cookie.Secure != secure {
								t.Fatal("会话 Cookie 未遵守显式 HTTP 配置")
							}
							parts := strings.Split(cookie.Value, ".")
							if len(parts) != 3 {
								t.Fatal("会话不是既有 JWT 格式")
							}
							raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
							var claims map[string]any
							_ = json.Unmarshal(raw, &claims)
							session = stringField(claims, "key")
						}
					}
					info := h.ok("GET", "/user/info", session, nil).json()
					if stringField(info["user_info"].(map[string]any), "user_id") != userID {
						t.Fatal("SAML 稳定身份没有映射到预置 SCIM 用户", info)
					}
					if got := h.do("GET", "/audit/logs", session, nil); got.status != 403 {
						t.Fatal("普通 SAML 用户越权访问平台审计", got.status)
					}
					if mode == "replay" {
						again := samlRequest(t, h.server.Client(), "POST", form.URL, url.Values{"SAMLResponse": {form.SAMLResponse}, "RelayState": {form.RelayState}}, cookies)
						repeated := samlRequest(t, h.server.Client(), "GET", post.headers.Get("Location"), nil, cookies)
						if again.status != 401 || repeated.status != 401 || hasSessionCookie(again) || hasSessionCookie(repeated) {
							t.Fatal("断言或 finish 重放产生会话", again.status, repeated.status)
						}
					}
				})
			}
			h.ok("DELETE", "/admin/enterprise/sso/"+connection, admin, nil)
			if got := h.do("GET", "/auth/enterprise/"+connection+"/saml/metadata", "", nil); got.status != 404 {
				t.Fatal("已删除 SAML 连接仍导出元数据", got.status)
			}
			if got := samlRequest(t, h.server.Client(), "GET", h.server.URL+"/auth/enterprise/"+connection+"/start", nil, nil); got.status != 401 {
				t.Fatal("已删除 SAML 连接仍可登录", got.status)
			}
			h.ok("DELETE", "/admin/directory/connections/"+connection, admin, nil)
		})
	}
}

// TestEnterpriseSAMLExpiredSigningCertificate 使用真正过期但结构合法的 IdP 签名证书，验证导入/导出不等于登录成功。
// 私有 schema 内保存停用连接、导出元数据、启用失败和登录拒绝；所有服务和 schema 自动回收。
func TestEnterpriseSAMLExpiredSigningCertificate(t *testing.T) {
	h := newHarness(t)
	h.gw.Cfg.GeneralSettings.MasterKey = strings.Repeat("e", 32)
	admin := h.adminSession()
	idp := samltest.NewExpired(t)
	org := h.ok("POST", "/organization/new", admin, map[string]any{"organization_alias": "Expired SAML signer"}).json()
	config := map[string]any{"organization_id": stringField(org, "organization_id"), "protocol": "saml", "name": "Expired signer", "client_id": "urn:xhub:expired-signer", "redirect_uri": h.server.URL + "/auth/enterprise/saml/acs", "return_uri": h.server.URL + "/ui/login", "idp_metadata": idp.Metadata(), "enabled": false, "allow_http": true}
	// 不合法例外组合应在网络访问前明确拒绝；使用不可达元数据地址证明没有依赖真实服务。
	for _, mode := range []string{"http-disabled", "remote-metadata", "oidc"} {
		invalid := map[string]any{}
		for key, value := range config {
			invalid[key] = value
		}
		invalid["allow_expired_idp_certificate"] = true
		switch mode {
		case "http-disabled":
			invalid["allow_http"] = false
		case "remote-metadata":
			invalid["idp_metadata_url"] = "https://127.0.0.1:1/metadata"
		case "oidc":
			invalid["protocol"] = "oidc"
			invalid["allow_http"] = false
		}
		if got := h.do("POST", "/admin/enterprise/sso", admin, invalid); got.status != 400 {
			t.Fatalf("非法到期例外未拒绝 %s: %d", mode, got.status)
		}
	}
	created := h.ok("POST", "/admin/enterprise/sso", admin, config).json()
	if created["idp_certificate_expired"] != true {
		t.Fatal("过期 IdP 证书必须显式呈现管理状态", created)
	}
	connection := stringField(created, "connection_id")
	metadata := h.ok("GET", "/admin/enterprise/sso/"+connection+"/metadata", admin, nil).json()
	if err := idp.Register(stringField(metadata, "metadata")); err != nil {
		t.Fatal(err)
	}
	config["enabled"] = true
	enabled := h.do("PUT", "/admin/enterprise/sso/"+connection, admin, config)
	if enabled.status != 400 || !strings.Contains(enabled.text(), "saml_certificate_expired") {
		t.Fatal("过期 IdP 签名证书应明确拒绝启用", enabled.status, enabled.text())
	}
	start := samlRequest(t, h.server.Client(), "GET", h.server.URL+"/auth/enterprise/"+connection+"/start", nil, nil)
	if start.status != 401 || hasSessionCookie(start) {
		t.Fatal("过期证书配置不应建立登录事务或会话", start.status, start.text())
	}
	h.ok("DELETE", "/admin/enterprise/sso/"+connection, admin, nil)
}

// samlRequest 发送不跟随重定向的真实 HTTP 请求；form 可为空、cookies 显式模拟浏览器是否携带绑定。
// 返回状态正文响应头，网络错误终止当前子用例；始终关闭响应体，不使用真实账户。
func samlRequest(t *testing.T, transport *http.Client, method, target string, form url.Values, cookies []*http.Cookie) reply {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	client := *transport
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: resp.StatusCode, body: raw, headers: resp.Header}
}

// hasSessionCookie 检查响应是否签发登录 token；供所有拒绝路径验证无副作用，删除事务 Cookie 不算登录。
func hasSessionCookie(r reply) bool {
	for _, cookie := range (&http.Response{Header: r.headers}).Cookies() {
		if cookie.Name == "token" && cookie.Value != "" {
			return true
		}
	}
	return false
}
