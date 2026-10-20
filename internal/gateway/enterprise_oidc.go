package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// enterpriseProvider 通过安全客户端发现 OIDC 并核验提供方 URL；cfg 为受控配置，返回提供方或错误。
// ctx 限制请求生命周期，避免未受信任 discovery 将浏览器重定向到内网或非 HTTPS。
func enterpriseProvider(ctx context.Context, cfg *iam.EnterpriseOIDC) (*oidc.Provider, error) {
	if _, err := enterpriseURL(cfg.Issuer); err != nil {
		return nil, err
	}
	p, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	if _, err := enterpriseURL(p.Endpoint().AuthURL); err != nil {
		return nil, err
	}
	if _, err := enterpriseURL(p.Endpoint().TokenURL); err != nil {
		return nil, err
	}
	return p, nil
}

// enterpriseContext 为 OIDC 库提供受限客户端；ctx 为请求上下文，返回协议上下文，无存储副作用。
// 测试可注入本地 TLS 提供方，生产默认禁止内网和非 HTTPS 请求。
func (s *Server) enterpriseContext(ctx context.Context) context.Context {
	client := s.enterpriseClient
	if client == nil {
		client = enterpriseHTTPClient()
	}
	return oidc.ClientContext(ctx, client)
}

// enterpriseOIDCAdmin 独立创建、列出、读取、保存或删除 SSO；必须是平台管理会话，不回显密文或明文 secret。
// 写入在启用时校验 discovery，固定回调/返回 URL，不改变本地登录策略或自动开通用户。
func (s *Server) enterpriseOIDCAdmin(w http.ResponseWriter, r *http.Request) {
	p := s.RequireManage(w, r)
	if p == nil {
		return
	}
	id := r.PathValue("connection_id")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodDelete {
		if err := s.Identity().DeleteEnterpriseOIDC(r.Context(), iam.Actor{ID: p.UserID, Kind: "session"}, id); err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet {
		if id == "" {
			rows, err := s.Identity().EnterpriseOIDCConfigurations(r.Context())
			if err != nil {
				s.WriteIAMError(w, r, err)
				return
			}
			for i := range rows {
				if rows[i].Protocol == "saml" {
					rows[i].IDPCertificateExpired = enterpriseSAMLExpiredCertificate(rows[i].IDPMetadata)
				}
			}
			util.WriteJSON(w, 200, rows)
			return
		}
		out, err := s.Identity().EnterpriseOIDCByID(r.Context(), id)
		if err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
		if out.Protocol == "saml" {
			out.IDPCertificateExpired = enterpriseSAMLExpiredCertificate(out.IDPMetadata)
		}
		util.WriteJSON(w, 200, out)
		return
	}
	var in struct {
		iam.EnterpriseOIDC
		ClientSecret   string `json:"client_secret"`
		OrganizationID string `json:"organization_id"`
	}
	if !directoryDecode(r, &in) {
		util.WriteError(w, 400, "invalid_request", "Invalid JSON")
		return
	}
	in.ConnectionID = id
	if in.Protocol == "" {
		in.Protocol = "oidc"
	}
	if in.Protocol == "saml" {
		if err := s.prepareEnterpriseSAML(r.Context(), &in.EnterpriseOIDC); err != nil {
			if errors.Is(err, errSAMLExpiredCertificate) {
				util.WriteError(w, 400, "saml_certificate_expired", "IdP signing certificate is expired or not yet valid")
				return
			}
			util.WriteError(w, 400, "invalid_saml_configuration", "Invalid SAML configuration")
			return
		}
	} else if in.Protocol == "oidc" {
		if in.AllowHTTP || in.AllowExpiredIDPCertificate {
			util.WriteError(w, 400, "invalid_request", "HTTP is only supported for SAML testing")
			return
		}
		if id != "" {
			old, err := s.Identity().EnterpriseOIDCByID(r.Context(), id)
			if err == nil && old.Protocol == "saml" {
				util.WriteError(w, 400, "invalid_request", "Create a new connection to change protocol")
				return
			}
		}
		redirect, err := enterpriseURL(in.RedirectURI)
		if err != nil || redirect.RawQuery != "" || redirect.Path != "/auth/enterprise/callback" {
			util.WriteError(w, 400, "invalid_request", "Invalid OIDC callback URL")
			return
		}
		ret, err := enterpriseURL(in.ReturnURI)
		if err != nil || ret.RawQuery != "" || ret.Path != "/ui/login" || ret.Host != redirect.Host || ret.Scheme != redirect.Scheme {
			util.WriteError(w, 400, "invalid_request", "Invalid console return URL")
			return
		}
		if _, err := enterpriseURL(in.Issuer); err != nil || in.ClientID == "" || in.IdentityClaim == "" || strings.ContainsAny(in.IdentityClaim, " .[]") {
			util.WriteError(w, 400, "invalid_request", "Invalid OIDC configuration")
			return
		}
		if in.ClientSecret == "" {
			old, err := s.Identity().EnterpriseOIDCByID(r.Context(), id)
			if err == nil {
				in.Secret = old.Secret
			}
		} else {
			in.Secret, err = enterpriseSecret(s.Cfg.GeneralSettings.MasterKey, in.ClientSecret, false)
			if err != nil {
				util.WriteError(w, 400, "invalid_request", "A stable encryption key is required")
				return
			}
		}
		if in.Enabled {
			ctx := s.enterpriseContext(r.Context())
			if _, err := enterpriseProvider(ctx, &in.EnterpriseOIDC); err != nil {
				util.WriteError(w, 400, "oidc_discovery_failed", "OIDC discovery failed")
				return
			}
		}
	} else {
		util.WriteError(w, 400, "invalid_request", "Unsupported login protocol")
		return
	}

	if r.Method == http.MethodPost {
		out, err := s.Identity().CreateEnterpriseOIDC(r.Context(), iam.Actor{ID: p.UserID, Kind: "session"}, in.OrganizationID, in.EnterpriseOIDC)
		if err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
		util.WriteJSON(w, http.StatusCreated, out)
		return
	}
	if err := s.Identity().SaveEnterpriseOIDC(r.Context(), iam.Actor{ID: p.UserID, Kind: "session"}, in.EnterpriseOIDC); err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	util.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// enterpriseOIDCOptions 返回公开登录入口；不返回 issuer、secret、成员或组织详情，无写副作用。
func (s *Server) enterpriseOIDCOptions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Identity().EnterpriseOIDCOptions(r.Context())
	if err != nil {
		util.WriteError(w, 503, "unavailable", "Enterprise login unavailable")
		return
	}
	out := []map[string]string{}
	for _, c := range rows {
		out = append(out, map[string]string{"id": c.ConnectionID, "name": c.Name, "url": "/gw/auth/enterprise/" + c.ConnectionID + "/start"})
	}
	util.WriteJSON(w, 200, out)
}

// enterpriseConfigured 读取可用登录入口判断配置状态；请求提供上下文，返回布尔值，存储失败关闭入口。
func (s *Server) enterpriseConfigured(r *http.Request) bool {
	rows, err := s.Identity().EnterpriseOIDCOptions(r.Context())
	return err == nil && len(rows) > 0
}

// enterpriseOIDCStart 创建 state/nonce/PKCE 和浏览器绑定事务并跳转提供方；不接受客户端返回地址。
// 参数为公开登录请求，返回重定向或固定错误；强制安全 cookie，事务有效期五分钟。
func (s *Server) enterpriseOIDCStart(w http.ResponseWriter, r *http.Request) {
	c, err := s.Identity().EnterpriseOIDCByID(r.Context(), r.PathValue("connection_id"))
	if err != nil || !c.Enabled {
		util.WriteError(w, 401, "auth_error", "Enterprise login unavailable")
		return
	}
	if c.Protocol == "saml" {
		s.enterpriseSAMLStart(w, r, c)
		return
	}
	ctx := s.enterpriseContext(r.Context())
	provider, err := enterpriseProvider(ctx, c)
	if err != nil {
		util.WriteError(w, 503, "oidc_unavailable", "Enterprise login unavailable")
		return
	}
	values := make([]string, 4)
	for i := range values {
		values[i], err = enterpriseRandom()
		if err != nil {
			util.WriteError(w, 500, "internal", "Could not start login")
			return
		}
	}
	state, nonce, verifier, browser := values[0], values[1], values[2], values[3]
	tx := iam.EnterpriseLoginTransaction{StateHash: iam.HashKey(state), ConnectionID: c.ConnectionID, Revision: c.Revision, BrowserHash: iam.HashKey(browser), Nonce: nonce, Verifier: verifier, ExpiresAt: time.Now().Add(5 * time.Minute)}
	if err := s.Identity().StartEnterpriseLogin(r.Context(), tx); err != nil {
		util.WriteError(w, 429, "auth_error", "Could not start login")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "__Host-xhub-enterprise-" + state, Value: browser, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	sum := sha256.Sum256([]byte(verifier))
	o := oauth2.Config{ClientID: c.ClientID, RedirectURL: c.RedirectURI, Endpoint: provider.Endpoint(), Scopes: []string{oidc.ScopeOpenID}}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, o.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:])), oauth2.SetAuthURLParam("code_challenge_method", "S256")), 302)
}

// enterpriseOIDCCallback 单次兑换授权码并校验令牌和身份；参数为提供方回调，无未同步开户路径。
// 成功复用既有会话签发并设置控制台 token cookie，失败不返回令牌详情或敏感 claim。
func (s *Server) enterpriseOIDCCallback(w http.ResponseWriter, r *http.Request) {
	reject := func() { util.WriteError(w, 401, "auth_error", "Enterprise authentication failed") }
	state := r.URL.Query().Get("state")
	if len(state) != 43 {
		reject()
		return
	}
	cookie, err := r.Cookie("__Host-xhub-enterprise-" + state)
	if err != nil {
		reject()
		return
	}
	tx, err := s.Identity().ConsumeEnterpriseLogin(r.Context(), state, cookie.Value)
	if err != nil {
		reject()
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookie.Name, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	c, err := s.Identity().EnterpriseOIDCByID(r.Context(), tx.ConnectionID)
	if err != nil || !c.Enabled || c.Protocol == "saml" || c.Revision != tx.Revision || r.URL.Query().Get("error") != "" {
		reject()
		return
	}
	ctx := s.enterpriseContext(r.Context())
	provider, err := enterpriseProvider(ctx, c)
	if err != nil {
		reject()
		return
	}
	secret := ""
	if c.Secret != "" {
		secret, err = enterpriseSecret(s.Cfg.GeneralSettings.MasterKey, c.Secret, true)
		if err != nil {
			reject()
			return
		}
	}
	o := oauth2.Config{ClientID: c.ClientID, ClientSecret: secret, RedirectURL: c.RedirectURI, Endpoint: provider.Endpoint()}
	token, err := o.Exchange(ctx, r.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", tx.Verifier))
	if err != nil {
		reject()
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		reject()
		return
	}
	id, err := provider.Verifier(&oidc.Config{ClientID: c.ClientID}).Verify(ctx, raw)
	if err != nil || id.Nonce != tx.Nonce {
		reject()
		return
	}
	claims := map[string]any{}
	if id.Claims(&claims) != nil {
		reject()
		return
	}
	if azp, ok := claims["azp"].(string); (len(id.Audience) > 1 && !ok) || (ok && azp != c.ClientID) {
		reject()
		return
	}
	external, _ := claims[c.IdentityClaim].(string)
	u, err := s.Identity().EnterpriseLoginUser(r.Context(), c.ConnectionID, c.Issuer, id.Subject, external, c.Revision)
	if err != nil {
		reject()
		return
	}
	// 复用原有会话创建，不把 token 放进 URL；控制台既有鉴权需要可读 token cookie。
	rec := httptest.NewRecorder()
	s.loginSuccess(rec, u)
	if rec.Code != 200 {
		reject()
		return
	}
	var result struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Token == "" {
		reject()
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "token", Value: result.Token, Path: "/", Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, c.ReturnURI, 302)
}
