// Package samltest 为回归和浏览器测试提供独立 TLS SAML 身份平台，不访问外部凭据或真实用户。
package samltest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"html/template"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"
)

// Form 是 IdP 生成的真实 POST binding 内容；调用方可经 HTTP 提交或重放以验证 ACS。
type Form struct {
	URL          string `json:"url"`
	SAMLResponse string `json:"SAMLResponse"`
	RelayState   string `json:"RelayState"`
}

// IDP 保存一套临时签名证书和独立 SP 注册表；模式仅影响测试断言，实例由 testing.Cleanup 关闭。
type IDP struct {
	Server *httptest.Server
	idp    *saml.IdentityProvider
	mu     sync.Mutex
	sps    map[string]*saml.EntityDescriptor
	mode   string
	last   Form
}

// New 启动本地 TLS IdP 并生成短期 RSA 证书；t 接收初始化错误并登记关闭，不读写外部身份平台。
func New(t testing.TB) *IDP {
	return newIDP(t, false, false)
}

// NewExpired 提供真实过期签名证书的本地 IdP；仅供后台失败回归，验证验签库不会被配置导出绕过。
func NewExpired(t testing.TB) *IDP {
	return newIDP(t, true, false)
}

// NewHTTP 启动无 TLS 的独立联调 IdP；供显式 HTTP 登录测试，返回实例并通过 t 自动回收服务。
func NewHTTP(t testing.TB) *IDP { return newIDP(t, false, true) }

// newIDP 按 expired 生成有效或过期签名证书；plainHTTP 选择浏览器联调传输，t 接收错误并登记清理。
func newIDP(t testing.TB, expired, plainHTTP bool) *IDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: "Local SAML test IdP"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	if expired {
		cert.NotBefore, cert.NotAfter = now.Add(-48*time.Hour), now.Add(-24*time.Hour)
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	f := &IDP{sps: map[string]*saml.EntityDescriptor{}, mode: "valid"}
	f.idp = &saml.IdentityProvider{Key: key, Signer: key, Certificate: cert, Logger: log.New(io.Discard, "", 0), ServiceProviderProvider: f, SignatureMethod: dsig.RSASHA256SignatureMethod}
	if plainHTTP {
		f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	} else {
		f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	}
	metadataURL, _ := url.Parse(f.Server.URL + "/metadata")
	ssoURL, _ := url.Parse(f.Server.URL + "/sso")
	f.idp.MetadataURL, f.idp.SSOURL = *metadataURL, *ssoURL
	t.Cleanup(f.Server.Close)
	return f
}

// Metadata 返回本实例的公开元数据 XML；仅包含短期测试证书，供真实管理接口保存。
func (f *IDP) Metadata() string {
	raw, _ := xml.Marshal(f.idp.Metadata())
	return xml.Header + string(raw)
}

// Register 将真实 XHub 导出的 SP XML 加入独立注册表；返回格式错误，不替换响应中的协议字段。
func (f *IDP) Register(raw string) error {
	var sp saml.EntityDescriptor
	if err := xml.Unmarshal([]byte(raw), &sp); err != nil {
		return err
	}
	if sp.EntityID == "" || len(sp.SPSSODescriptors) == 0 {
		return errors.New("missing SP descriptor")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sps[sp.EntityID] = &sp
	return nil
}

// GetServiceProvider 按 AuthnRequest issuer 查找已注册实体；实现 crewjam 接口，未知实体返回不存在。
func (f *IDP) GetServiceProvider(_ *http.Request, id string) (*saml.EntityDescriptor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sp, ok := f.sps[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return sp, nil
}

// SetMode 选择下一次登录的有效、未知身份或篡改断言模式；仅供本地控制端点使用，不修改 XHub 数据。
func (f *IDP) SetMode(mode string) { f.mu.Lock(); defer f.mu.Unlock(); f.mode = mode }

// Last 返回最近一次已签名表单副本；用于浏览器重放断言，无额外网络访问或数据修改。
func (f *IDP) Last() Form { f.mu.Lock(); defer f.mu.Unlock(); return f.last }

// Response 验证真实 Redirect 请求签名并生成签名 POST 响应；employee 是稳定身份，mode 指定失败输入。
// 返回字段可直接提交 ACS；任何非法请求或未知 SP 均不签发断言。
func (f *IDP) Response(redirect, employee, mode string) (Form, error) {
	r, err := http.NewRequest(http.MethodGet, redirect, nil)
	if err != nil {
		return Form{}, err
	}
	req, err := saml.NewIdpAuthnRequest(f.idp, r)
	if err != nil {
		return Form{}, err
	}
	if err = req.Validate(); err != nil {
		return Form{}, err
	}
	if err = verifyRedirect(r.URL, req.ServiceProviderMetadata); err != nil {
		return Form{}, err
	}
	if mode == "unknown" {
		employee = "unknown-local-employee"
	}
	session := &saml.Session{ID: "test-session", Index: "test-session", CreateTime: time.Now(), ExpireTime: time.Now().Add(time.Hour), NameID: "unrelated-transient-name-id", CustomAttributes: []saml.Attribute{{Name: "urn:oid:0.9.2342.19200300.100.1.1", NameFormat: "urn:oasis:names:tc:SAML:2.0:attrname-format:uri", Values: []saml.AttributeValue{{Type: "xs:string", Value: employee}}}}}
	if err = (saml.DefaultAssertionMaker{}).MakeAssertion(req, session); err != nil {
		return Form{}, err
	}
	binding, err := req.PostBinding()
	if err != nil {
		return Form{}, err
	}
	form := Form{URL: binding.URL, SAMLResponse: binding.SAMLResponse, RelayState: binding.RelayState}
	if mode == "tamper" {
		// 保持良构 XML 和原签名，仅修改 Response ID；验签必须拒绝已签内容变化。
		raw, _ := base64.StdEncoding.DecodeString(form.SAMLResponse)
		raw = []byte(strings.Replace(string(raw), `ID="`, `ID="tampered-`, 1))
		form.SAMLResponse = base64.StdEncoding.EncodeToString(raw)
	}
	f.mu.Lock()
	f.last = form
	f.mu.Unlock()
	return form, nil
}

// verifyRedirect 补足测试 IdP 对 HTTP Redirect 签名的验证；u 必须携带 RSA-SHA256，sp 为真实导出元数据。
// 使用查询串原始编码计算摘要，避免把重新编码后的错误签名当作正确请求；失败返回错误。
func verifyRedirect(u *url.URL, sp *saml.EntityDescriptor) error {
	q := u.Query()
	if q.Get("SigAlg") != dsig.RSASHA256SignatureMethod {
		return errors.New("AuthnRequest must use RSA-SHA256")
	}
	var parts []string
	for _, name := range []string{"SAMLRequest", "RelayState", "SigAlg"} {
		for _, part := range strings.Split(u.RawQuery, "&") {
			if strings.HasPrefix(part, name+"=") {
				parts = append(parts, part)
				break
			}
		}
	}
	if len(parts) != 3 {
		return errors.New("missing signed AuthnRequest query")
	}
	sig, err := base64.StdEncoding.DecodeString(q.Get("Signature"))
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(strings.Join(parts, "&")))
	for _, descriptor := range sp.SPSSODescriptors {
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
				if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok && rsa.VerifyPKCS1v15(pub, crypto.SHA256, hash[:], sig) == nil {
					return nil
				}
			}
		}
	}
	return errors.New("invalid AuthnRequest signature")
}

// serve 提供浏览器可操作的身份表单和自动 POST 页面；只接受本地已注册 SP，错误响应不包含私钥。
func (f *IDP) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/metadata" {
		f.idp.ServeMetadata(w, r)
		return
	}
	if r.URL.Path != "/sso" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, `<html><body><form method="post"><label>Employee<input name="employee" value="employee-sub"></label><button>Sign in to identity provider</button></form></body></html>`)
		return
	}
	f.mu.Lock()
	mode := f.mode
	f.mu.Unlock()
	form, err := f.Response(f.Server.URL+r.URL.RequestURI(), r.FormValue("employee"), mode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tmpl := template.Must(template.New("post").Parse(`<html><body><form method="post" action="{{.URL}}"><input type="hidden" name="SAMLResponse" value="{{.SAMLResponse}}"><input type="hidden" name="RelayState" value="{{.RelayState}}"><button>Continue to XHub</button></form><script>document.forms[0].submit()</script></body></html>`))
	_ = tmpl.Execute(w, form)
}
