package gateway

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/crewjam/saml"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

// enterpriseSAMLMetadataXML 读取配置内 XML 或通过受限 HTTPS 客户端刷新元数据；ctx 限制请求时间。
// 返回有限大小 XML，不信任元数据内其他网络地址；网络失败关闭登录，不使用过期证书兜底。
func (s *Server) enterpriseSAMLMetadataXML(ctx context.Context, c *iam.EnterpriseOIDC) (string, error) {
	if c.IDPMetadataURL == "" {
		return c.IDPMetadata, nil
	}
	if _, err := enterpriseURL(c.IDPMetadataURL); err != nil {
		return "", err
	}
	client := s.enterpriseClient
	if client == nil {
		client = enterpriseHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.IDPMetadataURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("metadata unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return "", errors.New("invalid metadata size")
	}
	return string(raw), nil
}

// prepareEnterpriseSAML 校验管理表单并生成或保留服务端密钥；配置保存调用，返回固定错误供接口脱敏。
// 仅支持同一连接内更新；禁止 OIDC/SAML 原地互换造成旧身份绑定混用，私钥和证书不接受客户端覆盖。
func (s *Server) prepareEnterpriseSAML(ctx context.Context, c *iam.EnterpriseOIDC) error {
	// 在下载或生成密钥前拒绝不受控组合；运行时提供方再次检查，防止历史配置绕过管理入口。
	if c.AllowExpiredIDPCertificate && (!c.AllowHTTP || strings.TrimSpace(c.IDPMetadataURL) != "" || strings.TrimSpace(c.IDPMetadata) == "") {
		return iam.ErrInvalid
	}
	if c.IdentityClaim == "" {
		c.IdentityClaim = "urn:oid:0.9.2342.19200300.100.1.1"
	}
	if len(c.IdentityClaim) > 256 || strings.TrimSpace(c.IdentityClaim) != c.IdentityClaim {
		return iam.ErrInvalid
	}
	entity, err := url.Parse(c.ClientID)
	if err != nil || !entity.IsAbs() || entity.Fragment != "" || entity.User != nil {
		return iam.ErrInvalid
	}
	c.Secret, c.Certificate = "", ""
	if c.ConnectionID != "" {
		old, err := s.Identity().EnterpriseOIDCByID(ctx, c.ConnectionID)
		if err != nil {
			return err
		}
		if old.Protocol != "saml" {
			return iam.ErrInvalid
		}
		c.Secret, c.Certificate = old.Secret, old.Certificate
	}
	if c.Secret == "" {
		c.Secret, c.Certificate, err = enterpriseSAMLMaterial(s.Cfg.GeneralSettings.MasterKey)
		if err != nil {
			return err
		}
	}
	metadata, err := s.enterpriseSAMLMetadataXML(ctx, c)
	if err != nil {
		return err
	}
	if len(metadata) > 2<<20 {
		return iam.ErrInvalid
	}
	var descriptor saml.EntityDescriptor
	if err = xml.Unmarshal([]byte(metadata), &descriptor); err != nil || descriptor.EntityID == "" {
		return iam.ErrInvalid
	}
	c.Issuer = descriptor.EntityID
	c.IDPMetadata = metadata
	c.IDPCertificateExpired = enterpriseSAMLExpiredCertificate(metadata)
	if c.Enabled && c.IDPCertificateExpired && !c.AllowExpiredIDPCertificate {
		return errSAMLExpiredCertificate
	}
	_, err = enterpriseSAMLProvider(c, s.Cfg.GeneralSettings.MasterKey, metadata)
	return err
}

// enterpriseSAMLRuntime 构造验签提供者；每次刷新受限 HTTPS 元数据并要求已配置 issuer 不变。
// 返回提供者或错误，不更新配置版本或泄露身份断言，避免临时网络失败造成假登录。
func (s *Server) enterpriseSAMLRuntime(ctx context.Context, c *iam.EnterpriseOIDC) (*saml.ServiceProvider, error) {
	metadata, err := s.enterpriseSAMLMetadataXML(ctx, c)
	if err != nil {
		return nil, err
	}
	return enterpriseSAMLProvider(c, s.Cfg.GeneralSettings.MasterKey, metadata)
}

// enterpriseSAMLMetadata 导出当前连接的公开 SP 元数据；管理接口要求平台会话并包装 JSON，公开接口只返回 XML。
// 允许尚未启用的连接供 IdP 注册，拒绝非 SAML 配置；所有秘密仅用于服务端构造，不序列化私钥。
func (s *Server) enterpriseSAMLMetadata(w http.ResponseWriter, r *http.Request) {
	admin := strings.HasPrefix(r.URL.Path, "/admin/")
	if admin && s.RequireManage(w, r) == nil {
		return
	}
	c, err := s.Identity().EnterpriseOIDCByID(r.Context(), r.PathValue("connection_id"))
	if err != nil || c.Protocol != "saml" {
		util.WriteError(w, 404, "not_found", "SAML connection not found")
		return
	}
	sp, err := s.enterpriseSAMLRuntime(r.Context(), c)
	if err != nil {
		util.WriteError(w, 503, "saml_unavailable", "SAML metadata unavailable")
		return
	}
	raw, err := xml.MarshalIndent(sp.Metadata(), "", "  ")
	if err != nil {
		util.WriteError(w, 500, "internal", "SAML metadata unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	text := xml.Header + string(raw)
	if admin {
		util.WriteJSON(w, 200, map[string]string{"metadata": text})
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, text)
}

// enterpriseSAMLCookie 为配置选择浏览器事务 Cookie；HTTPS 使用 __Host 前缀，HTTP 联调采用独立名称。
// state/value 是随机凭据，age 控制写入或删除；始终 HttpOnly/Lax，不为跨站 POST 放宽成无绑定登录。
func enterpriseSAMLCookie(c *iam.EnterpriseOIDC, state, value string, age int) *http.Cookie {
	secure := strings.HasPrefix(c.RedirectURI, "https://")
	name := "xhub-saml-" + state
	if secure {
		name = "__Host-" + name
	}
	return &http.Cookie{Name: name, Value: value, Path: "/", Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age}
}

// enterpriseSAMLStart 创建浏览器绑定请求并以签名 Redirect 请求跳转 IdP；c 是已保存启用配置。
// 五分钟事务记录 request ID，后续仅接受对应 POST 响应；失败不签发会话，忽略客户端自选重定向。
func (s *Server) enterpriseSAMLStart(w http.ResponseWriter, r *http.Request, c *iam.EnterpriseOIDC) {
	sp, err := s.enterpriseSAMLRuntime(r.Context(), c)
	if err != nil {
		util.WriteError(w, 503, "saml_unavailable", "Enterprise login unavailable")
		return
	}
	state, err := enterpriseRandom()
	if err != nil {
		util.WriteError(w, 500, "internal", "Could not start login")
		return
	}
	browser, err := enterpriseRandom()
	if err != nil {
		util.WriteError(w, 500, "internal", "Could not start login")
		return
	}
	request, err := sp.MakeAuthenticationRequest(sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		util.WriteError(w, 503, "saml_unavailable", "Could not start login")
		return
	}
	redirect, err := request.Redirect(state, sp)
	if err != nil {
		util.WriteError(w, 503, "saml_unavailable", "Could not start login")
		return
	}
	tx := iam.EnterpriseLoginTransaction{StateHash: iam.HashKey(state), ConnectionID: c.ConnectionID, Revision: c.Revision, BrowserHash: iam.HashKey(browser), Nonce: request.ID, ExpiresAt: time.Now().Add(5 * time.Minute)}
	if err = s.Identity().StartEnterpriseLogin(r.Context(), tx); err != nil {
		util.WriteError(w, 429, "auth_error", "Could not start login")
		return
	}
	http.SetCookie(w, enterpriseSAMLCookie(c, state, browser, 300))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

// enterpriseSAMLACS 验证签名、时效、受众和请求关联后仅暂存稳定身份；POST 不依赖跨站 Cookie。
// 输入限制 2MiB 且不接受 Artifact 或多值参数；303 转到固定本站 GET 后才验证原浏览器并消费事务。
func (s *Server) enterpriseSAMLACS(w http.ResponseWriter, r *http.Request) {
	reject := func() { util.WriteError(w, 401, "auth_error", "Enterprise authentication failed") }
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		reject()
		return
	}
	if len(r.PostForm["RelayState"]) != 1 || len(r.PostForm["SAMLResponse"]) != 1 || r.PostForm.Get("SAMLart") != "" {
		reject()
		return
	}
	state := r.PostForm.Get("RelayState")
	if len(state) != 43 {
		reject()
		return
	}
	tx, err := s.Identity().EnterpriseSAMLTransaction(r.Context(), state)
	if err != nil || tx.SAMLSubject != "" {
		reject()
		return
	}
	c, err := s.Identity().EnterpriseOIDCByID(r.Context(), tx.ConnectionID)
	if err != nil || !c.Enabled || c.Protocol != "saml" || c.Revision != tx.Revision {
		reject()
		return
	}
	sp, err := s.enterpriseSAMLRuntime(r.Context(), c)
	if err != nil {
		reject()
		return
	}
	raw, err := base64.StdEncoding.DecodeString(r.PostForm.Get("SAMLResponse"))
	if err != nil {
		reject()
		return
	}
	assertion, err := enterpriseSAMLResponse(sp, raw, tx.Nonce)
	if err != nil {
		reject()
		return
	}
	subject, err := enterpriseSAMLIdentity(assertion, c.IdentityClaim)
	if err != nil {
		reject()
		return
	}
	if err = s.Identity().StageEnterpriseSAML(r.Context(), state, c.Revision, subject); err != nil {
		reject()
		return
	}
	target, _ := url.Parse(c.ReturnURI)
	target.Path = "/auth/enterprise/saml/finish"
	target.RawQuery = "state=" + url.QueryEscape(state)
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

// enterpriseSAMLFinish 在本站 GET 检查原浏览器后原子消费事务，绑定预先配置的普通用户并签发既有会话。
// 无 Cookie、重放、配置变更、停用及未登记身份均拒绝；返回地址来自配置，token 从不放入 URL。
func (s *Server) enterpriseSAMLFinish(w http.ResponseWriter, r *http.Request) {
	reject := func() { util.WriteError(w, 401, "auth_error", "Enterprise authentication failed") }
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	state := r.URL.Query().Get("state")
	if len(state) != 43 {
		reject()
		return
	}
	tx, err := s.Identity().EnterpriseSAMLTransaction(r.Context(), state)
	if err != nil || tx.SAMLSubject == "" {
		reject()
		return
	}
	c, err := s.Identity().EnterpriseOIDCByID(r.Context(), tx.ConnectionID)
	if err != nil || !c.Enabled || c.Protocol != "saml" || c.Revision != tx.Revision {
		reject()
		return
	}
	cookie, err := r.Cookie(enterpriseSAMLCookie(c, state, "", 0).Name)
	if err != nil {
		reject()
		return
	}
	tx, err = s.Identity().ConsumeEnterpriseLogin(r.Context(), state, cookie.Value)
	if err != nil {
		reject()
		return
	}
	http.SetCookie(w, enterpriseSAMLCookie(c, state, "", -1))
	u, err := s.Identity().EnterpriseLoginUser(r.Context(), c.ConnectionID, c.Issuer, tx.SAMLSubject, tx.SAMLSubject, c.Revision)
	if err != nil {
		reject()
		return
	}
	rec := httptest.NewRecorder()
	s.loginSuccess(rec, u)
	var result struct {
		Token string `json:"token"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Token == "" {
		reject()
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "token", Value: result.Token, Path: "/", Secure: strings.HasPrefix(c.RedirectURI, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(w, r, c.ReturnURI, http.StatusFound)
}

// errSAMLExpiredCertificate 标记配置内没有当前有效的 IdP 验签证书，供管理接口返回可翻译的明确错误。
var errSAMLExpiredCertificate = errors.New("SAML signing certificate is not current")

// enterpriseSAMLExpiredCertificate 检查元数据中是否没有当前可用的验签证书；metadata 为配置快照。
// 管理页面提示及启用校验调用，返回全过期/未来证书状态；保留过期配置用于导出 SP 元数据，不修改库的验签时钟。
func enterpriseSAMLExpiredCertificate(metadata string) bool {
	var entity saml.EntityDescriptor
	if xml.Unmarshal([]byte(metadata), &entity) != nil {
		return false
	}
	found := false
	for _, descriptor := range entity.IDPSSODescriptors {
		for _, key := range descriptor.KeyDescriptors {
			if key.Use != "" && key.Use != "signing" {
				continue
			}
			for _, encoded := range key.KeyInfo.X509Data.X509Certificates {
				der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded.Data), ""))
				if err != nil {
					continue
				}
				cert, err := x509.ParseCertificate(der)
				if err != nil {
					continue
				}
				found = true
				if enterpriseSAMLCertificateCurrent(cert, time.Now()) {
					return false
				}
			}
		}
	}
	return found
}
