package gateway

import (
	"compress/flate"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestEnterpriseSAMLURL 验证 HTTPS 默认及显式 HTTP 只放宽协议；覆盖危险组件和空查询边界，无网络或持久化需清理。
func TestEnterpriseSAMLURL(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		allowHTTP bool
		wantOK    bool
	}{
		{"https", "https://console.example/ui/login", false, true},
		{"https_with_port", "https://console.example:8443/ui/login", false, true},
		{"http_default_rejected", "http://console.example/ui/login", false, false},
		{"http_explicit", "http://127.0.0.1:3000/ui/login", true, true},
		{"ftp_never_allowed", "ftp://console.example/ui/login", true, false},
		{"relative", "/ui/login", true, false},
		{"protocol_relative", "//console.example/ui/login", true, false},
		{"opaque", "https:console.example", false, false},
		{"empty_host", "https:///ui/login", false, false},
		{"credentials", "https://user:password@console.example/ui/login", false, false},
		{"empty_userinfo", "https://@console.example/ui/login", false, false},
		{"query", "https://console.example/ui/login?next=evil", false, false},
		{"empty_query", "https://console.example/ui/login?", false, false},
		{"fragment", "https://console.example/ui/login#evil", false, false},
		{"empty_fragment", "https://console.example/ui/login#", false, false},
		{"bad_escape", "https://console.example/%zz", false, false},
		{"http_still_rejects_query", "http://console.example/ui/login?q=1", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := enterpriseSAMLURL(tc.raw, tc.allowHTTP)
			if (err == nil) != tc.wantOK || (err == nil && got == nil) {
				t.Fatalf("SAML URL 放行结果错误：允许=%t，错误=%v", tc.wantOK, err)
			}
		})
	}
}

// enterpriseSAMLTestMaterial 为确定性协议测试生成一次真实签名材料，返回配置、私钥和证书；仅使用内存，无清理副作用。
func enterpriseSAMLTestMaterial(t *testing.T) (iam.EnterpriseOIDC, *rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	master := strings.Repeat("s", 32)
	encrypted, certificate, err := enterpriseSAMLMaterial(master)
	if err != nil {
		t.Fatal("生成 SAML 测试材料失败", err)
	}
	plain, err := enterpriseSecret(master, encrypted, true)
	if err != nil {
		t.Fatal("读取 SAML 测试私钥失败", err)
	}
	pair, err := tls.X509KeyPair([]byte(certificate), []byte(plain))
	if err != nil {
		t.Fatal("SAML 测试证书与私钥不匹配", err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal("SAML 测试证书不可解析", err)
	}
	return iam.EnterpriseOIDC{
		Protocol: "saml", ConnectionID: "connection123", ClientID: "urn:xhub:test:sp",
		Issuer: "https://idp.example/entity", RedirectURI: "https://console.example/auth/enterprise/saml/acs",
		ReturnURI: "https://console.example/ui/login", Secret: encrypted, Certificate: certificate,
	}, pair.PrivateKey.(*rsa.PrivateKey), cert
}

// enterpriseSAMLTestMetadata 构造符合协议的单实体 IdP 元数据；cert 为测试签名证书，返回 XML，无下载或状态写入。
func enterpriseSAMLTestMetadata(cert *x509.Certificate) string {
	return `<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example/entity"><IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"><KeyDescriptor use="signing"><KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#"><X509Data><X509Certificate>` +
		base64.StdEncoding.EncodeToString(cert.Raw) +
		`</X509Certificate></X509Data></KeyInfo></KeyDescriptor><SingleSignOnService Binding="` +
		saml.HTTPRedirectBinding + `" Location="https://idp.example/sso"/></IDPSSODescriptor></EntityDescriptor>`
}

// TestEnterpriseSAMLMaterial 验证 RSA2048、自签名有效期及 AES-GCM 密文边界；生成内容只在内存中，不需要清理。
func TestEnterpriseSAMLMaterial(t *testing.T) {
	cfg, key, cert := enterpriseSAMLTestMaterial(t)
	t.Run("encrypted_matching_self_signed_key", func(t *testing.T) {
		if key.N.BitLen() != 2048 || strings.Contains(cfg.Secret, "PRIVATE KEY") {
			t.Fatal("SAML 私钥位数或加密保存不符合要求")
		}
		if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
			t.Fatal("SAML 证书不是有效自签名", err)
		}
		if !enterpriseSAMLCertificateCurrent(cert, time.Now()) || cert.NotAfter.Sub(cert.NotBefore) < 364*24*time.Hour {
			t.Fatal("SAML 证书有效期不正确")
		}
	})
	t.Run("short_master_rejected_without_partial_material", func(t *testing.T) {
		key, certificate, err := enterpriseSAMLMaterial("short")
		if err == nil || key != "" || certificate != "" {
			t.Fatal("弱主密钥未拒绝或泄漏部分材料")
		}
	})
	t.Run("wrong_master_rejected", func(t *testing.T) {
		if _, err := enterpriseSecret(strings.Repeat("x", 32), cfg.Secret, true); err == nil {
			t.Fatal("不同主密钥能够解密 SAML 私钥")
		}
	})
}

// TestEnterpriseSAMLMetadata 验证实体、有效期、证书、绑定与完整 XML 的正常/错误边界；仅用内存证书，无网络和持久化。
func TestEnterpriseSAMLMetadata(t *testing.T) {
	cfg, key, cert := enterpriseSAMLTestMaterial(t)
	valid := enterpriseSAMLTestMetadata(cert)
	now := time.Now()
	// IdP 固定验签公钥不按证书日历拒绝；这些样本与过期 metadata 的拒绝结果形成明确对照。
	expired := enterpriseSAMLTestCertificate(t, key, now.Add(-2*time.Hour), now.Add(-time.Hour), x509.KeyUsageDigitalSignature)
	future := enterpriseSAMLTestCertificate(t, key, now.Add(time.Hour), now.Add(2*time.Hour), x509.KeyUsageDigitalSignature)
	noSigning := enterpriseSAMLTestCertificate(t, key, now.Add(-time.Hour), now.Add(time.Hour), x509.KeyUsageKeyEncipherment)
	for _, tc := range []struct {
		name, metadata, issuer string
		wantOK                 bool
	}{
		{"valid_without_expiry", valid, cfg.Issuer, true},
		{"valid_zero_expiry", strings.Replace(valid, `entityID=`, `validUntil="0001-01-01T00:00:00Z" entityID=`, 1), cfg.Issuer, true},
		{"valid_future_expiry", strings.Replace(valid, `entityID=`, `validUntil="`+now.Add(time.Hour).UTC().Format(time.RFC3339)+`" entityID=`, 1), cfg.Issuer, true},
		{"expiry_elapsed", strings.Replace(valid, `entityID=`, `validUntil="`+now.Add(-time.Minute).UTC().Format(time.RFC3339)+`" entityID=`, 1), cfg.Issuer, false},
		{"role_expiry_elapsed", strings.Replace(valid, `<IDPSSODescriptor `, `<IDPSSODescriptor validUntil="`+now.Add(-time.Minute).UTC().Format(time.RFC3339)+`" `, 1), cfg.Issuer, false},
		{"wrong_issuer", valid, "https://other.example/entity", false},
		{"missing_issuer", valid, "", false},
		{"empty", "", cfg.Issuer, false},
		{"exact_size_limit", valid + strings.Repeat(" ", enterpriseSAMLMetadataLimit-len(valid)), cfg.Issuer, true},
		{"over_size_limit", valid + strings.Repeat(" ", enterpriseSAMLMetadataLimit-len(valid)+1), cfg.Issuer, false},
		{"truncated", valid[:len(valid)-10], cfg.Issuer, false},
		{"trailing_root", valid + valid, cfg.Issuer, false},
		{"trailing_text", valid + "untrusted", cfg.Issuer, false},
		{"aggregated_entities", `<EntitiesDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata">` + valid + `</EntitiesDescriptor>`, cfg.Issuer, false},
		{"wrong_namespace", strings.Replace(valid, `xmlns="urn:oasis:names:tc:SAML:2.0:metadata"`, `xmlns="urn:untrusted"`, 1), cfg.Issuer, false},
		{"nested_entity", strings.Replace(valid, `</EntityDescriptor>`, valid+`</EntityDescriptor>`, 1), cfg.Issuer, false},
		{"doctype", `<!DOCTYPE EntityDescriptor SYSTEM "https://untrusted.example/entity.dtd">` + valid, cfg.Issuer, false},
		{"http_sso", strings.Replace(valid, "https://idp.example/sso", "http://idp.example/sso", 1), cfg.Issuer, false},
		{"sso_credentials", strings.Replace(valid, "https://idp.example/sso", "https://secret@idp.example/sso", 1), cfg.Issuer, false},
		{"sso_query", strings.Replace(valid, "https://idp.example/sso", "https://idp.example/sso?q=1", 1), cfg.Issuer, false},
		{"only_post_binding", strings.Replace(valid, saml.HTTPRedirectBinding, saml.HTTPPostBinding, 1), cfg.Issuer, false},
		{"unsupported_protocol", strings.Replace(valid, `protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"`, `protocolSupportEnumeration="urn:unsupported"`, 1), cfg.Issuer, false},
		{"missing_signing_key", strings.Replace(valid, `use="signing"`, `use="encryption"`, 1), cfg.Issuer, false},
		{"unspecified_key_use", strings.Replace(valid, `use="signing"`, `use=""`, 1), cfg.Issuer, true},
		{"bad_certificate_base64", strings.Replace(valid, base64.StdEncoding.EncodeToString(cert.Raw), "not-valid-base64", 1), cfg.Issuer, false},
		{"bad_certificate_der", strings.Replace(valid, base64.StdEncoding.EncodeToString(cert.Raw), "aW52YWxpZA==", 1), cfg.Issuer, false},
		{"expired_pinned_certificate", enterpriseSAMLTestMetadata(expired), cfg.Issuer, true},
		{"future_pinned_certificate", enterpriseSAMLTestMetadata(future), cfg.Issuer, true},
		{"certificate_disallows_signing", enterpriseSAMLTestMetadata(noSigning), cfg.Issuer, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := enterpriseSAMLMetadata(tc.metadata, tc.issuer, now, false)
			if (err == nil) != tc.wantOK || (err == nil && got.EntityID != cfg.Issuer) {
				t.Fatalf("IdP 元数据验证结果错误：允许=%t，错误=%v", tc.wantOK, err)
			}
		})
	}
}

// enterpriseSAMLTestCertificate 用已有测试密钥签发指定时效和用途的证书，返回解析对象；单元边界测试调用，无外部状态。
func enterpriseSAMLTestCertificate(t *testing.T, key *rsa.PrivateKey, before, after time.Time, usage x509.KeyUsage) *x509.Certificate {
	t.Helper()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: before, NotAfter: after, KeyUsage: usage}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal("构造边界证书失败", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal("解析边界证书失败", err)
	}
	return cert
}

// TestEnterpriseSAMLProvider 验证配置形成真实可验的 RSA-SHA256 Redirect 请求，并覆盖材料/地址失败；无 HTTP 或数据库状态需清理。
func TestEnterpriseSAMLProvider(t *testing.T) {
	cfg, key, cert := enterpriseSAMLTestMaterial(t)
	master, metadata := strings.Repeat("s", 32), enterpriseSAMLTestMetadata(cert)
	t.Run("metadata_valid_until_matches_new_certificate", func(t *testing.T) {
		sp, err := enterpriseSAMLProvider(&cfg, master, metadata)
		if err != nil {
			t.Fatal("合法 SAML 配置未形成提供方", err)
		}
		exported := sp.Metadata()
		if remaining := time.Until(exported.ValidUntil); remaining < 300*24*time.Hour {
			t.Fatalf("新 SP 静态元数据有效期意外缩短：%s", remaining)
		}
		// 库在导出时再取当前时间，允许构造和导出之间最多一秒的调用时差，不能越过证书期限形成额外天数。
		if delta := exported.ValidUntil.Sub(cert.NotAfter); delta < -time.Second || delta > time.Second {
			t.Fatalf("SP 元数据没有跟随证书到期时间：偏差=%s", delta)
		}
	})
	t.Run("metadata_valid_until_matches_remaining_certificate_lifetime", func(t *testing.T) {
		copy := cfg
		short := enterpriseSAMLTestCertificate(t, key, time.Now().Add(-time.Hour), time.Now().Add(30*time.Minute), x509.KeyUsageDigitalSignature)
		copy.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: short.Raw}))
		sp, err := enterpriseSAMLProvider(&copy, master, metadata)
		if err != nil {
			t.Fatal("仍有效的短期 SP 证书未被接受", err)
		}
		if delta := sp.Metadata().ValidUntil.Sub(short.NotAfter); delta < -time.Second || delta > time.Second {
			t.Fatalf("SP 元数据未使用证书剩余寿命，可能仍使用两天默认值：偏差=%s", delta)
		}
	})
	t.Run("signed_redirect_request", func(t *testing.T) {
		sp, err := enterpriseSAMLProvider(&cfg, master, metadata)
		if err != nil {
			t.Fatal("合法 SAML 配置未形成提供方", err)
		}
		if sp.AllowIDPInitiated || sp.SignatureMethod != dsig.RSASHA256SignatureMethod ||
			sp.MetadataURL.String() != "https://console.example/auth/enterprise/connection123/saml/metadata" ||
			sp.AcsURL.String() != cfg.RedirectURI || sp.EntityID != cfg.ClientID {
			t.Fatal("提供方安全配置或地址不正确")
		}
		redirect, err := sp.MakeRedirectAuthenticationRequest("browser-bound-state")
		if err != nil {
			t.Fatal("签名认证请求生成失败", err)
		}
		query := redirect.Query()
		signature, err := base64.StdEncoding.DecodeString(query.Get("Signature"))
		if err != nil || query.Get("SigAlg") != dsig.RSASHA256SignatureMethod {
			t.Fatal("认证请求缺少 RSA-SHA256 签名", err)
		}
		signed, _, ok := strings.Cut(redirect.RawQuery, "&Signature=")
		hash := sha256.Sum256([]byte(signed))
		if !ok || rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature) != nil {
			t.Fatal("认证请求实际签名不可验证")
		}
		compressed, err := base64.StdEncoding.DecodeString(query.Get("SAMLRequest"))
		if err != nil {
			t.Fatal(err)
		}
		reader := flate.NewReader(strings.NewReader(string(compressed)))
		raw, err := io.ReadAll(reader)
		reader.Close()
		var request saml.AuthnRequest
		if err != nil || xml.Unmarshal(raw, &request) != nil ||
			request.AssertionConsumerServiceURL != cfg.RedirectURI || request.Issuer.Value != cfg.ClientID ||
			request.ProtocolBinding != saml.HTTPPostBinding {
			t.Fatalf("认证请求未绑定 SP/ACS/POST 回调：%s %v", raw, err)
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*iam.EnterpriseOIDC)
		wantOK bool
	}{
		{"precreate_empty_connection", func(c *iam.EnterpriseOIDC) { c.ConnectionID = "" }, true},
		{"wrong_protocol", func(c *iam.EnterpriseOIDC) { c.Protocol = "oidc" }, false},
		{"empty_entity", func(c *iam.EnterpriseOIDC) { c.ClientID = "" }, false},
		{"relative_entity", func(c *iam.EnterpriseOIDC) { c.ClientID = "relative" }, false},
		{"bad_connection_path", func(c *iam.EnterpriseOIDC) { c.ConnectionID = "../other" }, false},
		{"bad_connection_escape", func(c *iam.EnterpriseOIDC) { c.ConnectionID = "%2fother" }, false},
		{"acs_wrong_path", func(c *iam.EnterpriseOIDC) { c.RedirectURI = "https://console.example/other" }, false},
		{"acs_encoded_path", func(c *iam.EnterpriseOIDC) { c.RedirectURI = "https://console.example/%61uth/enterprise/saml/acs" }, false},
		{"return_wrong_path", func(c *iam.EnterpriseOIDC) { c.ReturnURI = "https://console.example/other" }, false},
		{"return_cross_origin", func(c *iam.EnterpriseOIDC) { c.ReturnURI = "https://other.example/ui/login" }, false},
		{"http_disabled", func(c *iam.EnterpriseOIDC) {
			c.ReturnURI = "http://console.example/ui/login"
			c.RedirectURI = "http://console.example/auth/enterprise/saml/acs"
		}, false},
		{"http_explicit", func(c *iam.EnterpriseOIDC) {
			c.AllowHTTP = true
			c.ReturnURI = "http://console.example/ui/login"
			c.RedirectURI = "http://console.example/auth/enterprise/saml/acs"
		}, true},
		{"invalid_ciphertext", func(c *iam.EnterpriseOIDC) { c.Secret = "invalid" }, false},
		{"invalid_certificate", func(c *iam.EnterpriseOIDC) { c.Certificate = "invalid" }, false},
		{"wrong_issuer", func(c *iam.EnterpriseOIDC) { c.Issuer = "https://other.example/entity" }, false},
		{"expired_sp_certificate", func(c *iam.EnterpriseOIDC) {
			old := enterpriseSAMLTestCertificate(t, key, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour), x509.KeyUsageDigitalSignature)
			c.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: old.Raw}))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := cfg
			tc.change(&copy)
			if _, err := enterpriseSAMLProvider(&copy, master, metadata); (err == nil) != tc.wantOK {
				t.Fatalf("SAML 提供方配置结果错误：允许=%t，错误=%v", tc.wantOK, err)
			}
		})
	}
	t.Run("nil_configuration", func(t *testing.T) {
		if _, err := enterpriseSAMLProvider(nil, master, metadata); err == nil {
			t.Fatal("空配置未拒绝")
		}
	})
	// 显式 HTTP 开关只允许手工 XML 的浏览器跳转；关闭开关或远程来源仍拒绝。
	for _, tc := range []struct {
		name     string
		allow    bool
		remote   string
		endpoint string
		ok       bool
	}{
		{"manual_http_enabled", true, "", "http://idp.example/sso", true},
		{"manual_http_default_rejected", false, "", "http://idp.example/sso", false},
		{"remote_http_endpoint_rejected", true, "https://idp.example/meta", "http://idp.example/sso", false},
		{"http_credentials_rejected", true, "", "http://user@idp.example/sso", false},
		{"http_query_rejected", true, "", "http://idp.example/sso?a=1", false},
		{"http_fragment_rejected", true, "", "http://idp.example/sso#fragment", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := cfg
			copy.AllowHTTP, copy.IDPMetadataURL = tc.allow, tc.remote
			_, err := enterpriseSAMLProvider(&copy, master, strings.Replace(metadata, "https://idp.example/sso", tc.endpoint, 1))
			if (err == nil) != tc.ok {
				t.Fatalf("手工 HTTP IdP 策略不符，允许=%t，错误=%v", tc.ok, err)
			}
		})
	}
	t.Run("decrypted_non_key_rejected", func(t *testing.T) {
		copy := cfg
		var err error
		copy.Secret, err = enterpriseSecret(master, "not a private key", false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enterpriseSAMLProvider(&copy, master, metadata); err == nil {
			t.Fatal("可解密但无效的 SAML 私钥未拒绝")
		}
	})
	t.Run("mismatched_key_pair_rejected", func(t *testing.T) {
		copy, _, _ := enterpriseSAMLTestMaterial(t)
		copy.Secret = cfg.Secret
		if _, err := enterpriseSAMLProvider(&copy, master, metadata); err == nil {
			t.Fatal("不匹配的 SAML 证书和私钥未拒绝")
		}
	})
	t.Run("weak_rsa_rejected", func(t *testing.T) {
		weak, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			t.Fatal(err)
		}
		copy := cfg
		copy.Secret, copy.Certificate = enterpriseSAMLTestEncodedKeyPair(t, weak, master)
		if _, err := enterpriseSAMLProvider(&copy, master, metadata); err == nil {
			t.Fatal("RSA1024 私钥未拒绝")
		}
	})
	t.Run("non_rsa_rejected", func(t *testing.T) {
		other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		copy := cfg
		copy.Secret, copy.Certificate = enterpriseSAMLTestEncodedKeyPair(t, other, master)
		if _, err := enterpriseSAMLProvider(&copy, master, metadata); err == nil {
			t.Fatal("不支持的非 RSA 私钥未拒绝")
		}
	})
}

// enterpriseSAMLTestEncodedKeyPair 将指定算法的测试密钥编码成真实加密配置，返回密文及匹配证书，用于排除 DER 损坏掩盖算法边界。
// key 为内存签名密钥、master 为测试主密钥；无网络或持久化，错误直接终止当前测试。
func enterpriseSAMLTestEncodedKeyPair(t *testing.T, key crypto.Signer, master string) (string, string) {
	t.Helper()
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := enterpriseSecret(master, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), false)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// TestEnterpriseSAMLResponseCertificateValidity 以真实 RSA-SHA256 签名响应核验底层证书到期行为。
// 前置内存 SP/IdP 材料；当前公钥成功、过期及未来公钥虽可配置但登录必须失败，不注入时钟或绕过验签，无外部状态。
func TestEnterpriseSAMLResponseCertificateValidity(t *testing.T) {
	cfg, key, current := enterpriseSAMLTestMaterial(t)
	now := time.Now()
	for _, tc := range []struct {
		name string
		cert *x509.Certificate
		ok   bool
	}{
		{"current_idp_certificate", current, true},
		{"expired_idp_certificate", enterpriseSAMLTestCertificate(t, key, now.Add(-2*time.Hour), now.Add(-time.Hour), x509.KeyUsageDigitalSignature), false},
		{"future_idp_certificate", enterpriseSAMLTestCertificate(t, key, now.Add(time.Hour), now.Add(2*time.Hour), x509.KeyUsageDigitalSignature), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp, err := enterpriseSAMLProvider(&cfg, strings.Repeat("s", 32), enterpriseSAMLTestMetadata(tc.cert))
			if err != nil {
				t.Fatal("可解析的固定 IdP 公钥应允许配置和元数据导出", err)
			}
			raw := enterpriseSAMLTestSignedResponse(t, cfg, key, tc.cert)
			assertion, err := sp.ParseXMLResponse(raw, []string{"request-test"}, sp.AcsURL)
			if tc.ok {
				if err != nil || assertion == nil {
					t.Fatal("当前有效 IdP 证书的真实签名响应未通过", err)
				}
			} else {
				var invalid *saml.InvalidResponseError
				if !errors.As(err, &invalid) || invalid.PrivateErr == nil || !strings.Contains(invalid.PrivateErr.Error(), "Cert is not valid at this time") {
					t.Fatalf("到期公钥未由默认验签器拒绝或失败原因不正确：%v", err)
				}
			}
		})
	}
}

// enterpriseSAMLTestSignedResponse 用指定 IdP 密钥与证书生成完整已签名 POST 响应，供真实 ParseXMLResponse 单元验证。
// cfg 只含合成测试实体/ACS；返回 XML 字节，不绕过验签，不存储断言和私钥。
func enterpriseSAMLTestSignedResponse(t *testing.T, cfg iam.EnterpriseOIDC, key *rsa.PrivateKey, cert *x509.Certificate) []byte {
	t.Helper()
	now := time.Now().UTC()
	raw := fmt.Sprintf(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="response-test" Version="2.0" IssueInstant="%s" Destination="%s" InResponseTo="request-test"><saml:Issuer>%s</saml:Issuer><samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/></samlp:Status><saml:Assertion ID="assertion-test" Version="2.0" IssueInstant="%s"><saml:Issuer>%s</saml:Issuer><saml:Subject><saml:NameID>untrusted-transient-name</saml:NameID><saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData InResponseTo="request-test" Recipient="%s" NotOnOrAfter="%s"/></saml:SubjectConfirmation></saml:Subject><saml:Conditions NotBefore="%s" NotOnOrAfter="%s"><saml:AudienceRestriction><saml:Audience>%s</saml:Audience></saml:AudienceRestriction></saml:Conditions><saml:AttributeStatement><saml:Attribute Name="external_id"><saml:AttributeValue>stable-user</saml:AttributeValue></saml:Attribute></saml:AttributeStatement></saml:Assertion></samlp:Response>`,
		now.Format(time.RFC3339Nano), cfg.RedirectURI, cfg.Issuer, now.Format(time.RFC3339Nano), cfg.Issuer,
		cfg.RedirectURI, now.Add(time.Minute).Format(time.RFC3339Nano), now.Add(-time.Minute).Format(time.RFC3339Nano),
		now.Add(time.Minute).Format(time.RFC3339Nano), cfg.ClientID)
	doc := etree.NewDocument()
	if err := doc.ReadFromString(raw); err != nil {
		t.Fatal("构造测试 SAML 响应失败", err)
	}
	signing, err := dsig.NewSigningContext(key, [][]byte{cert.Raw})
	if err != nil {
		t.Fatal(err)
	}
	signing.IdAttribute = "ID"
	signing.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	if err := signing.SetSignatureMethod(dsig.RSASHA256SignatureMethod); err != nil {
		t.Fatal(err)
	}
	signed, err := signing.SignEnveloped(doc.Root())
	if err != nil {
		t.Fatal("测试 SAML 响应签名失败", err)
	}
	doc.SetRoot(signed)
	encoded, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// TestEnterpriseSAMLCertificateCurrent 验证证书时效的闭开边界与用途；固定时钟和内存证书，无随机、网络或清理需求。
func TestEnterpriseSAMLCertificateCurrent(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		cert *x509.Certificate
		ok   bool
	}{
		{"nil", nil, false},
		{"begins_now", &x509.Certificate{NotBefore: now, NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}, true},
		{"ends_now", &x509.Certificate{NotBefore: now.Add(-time.Hour), NotAfter: now}, false},
		{"begins_future", &x509.Certificate{NotBefore: now.Add(time.Second), NotAfter: now.Add(time.Hour)}, false},
		{"no_key_usage_extension", &x509.Certificate{NotBefore: now, NotAfter: now.Add(time.Hour)}, true},
		{"encryption_only", &x509.Certificate{NotBefore: now, NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageKeyEncipherment}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if enterpriseSAMLCertificateCurrent(tc.cert, now) != tc.ok {
				t.Fatal("SAML 签名证书时效/用途结果错误")
			}
		})
	}
}

// TestEnterpriseSAMLAudience 验证认证目标必须显式匹配且满足全部限制组；无需验签前置，专测已验签后回调，无持久化。
func TestEnterpriseSAMLAudience(t *testing.T) {
	entity := "urn:xhub:test:sp"
	for _, tc := range []struct {
		name      string
		assertion *saml.Assertion
		ok        bool
	}{
		{"nil", nil, false},
		{"missing_conditions", &saml.Assertion{}, false},
		{"missing_audience", &saml.Assertion{Conditions: &saml.Conditions{}}, false},
		{"matching", &saml.Assertion{Conditions: &saml.Conditions{AudienceRestrictions: []saml.AudienceRestriction{{Audience: saml.Audience{Value: entity}}}}}, true},
		{"wrong_sp", &saml.Assertion{Conditions: &saml.Conditions{AudienceRestrictions: []saml.AudienceRestriction{{Audience: saml.Audience{Value: "urn:other"}}}}}, false},
		{"mixed_restrictions", &saml.Assertion{Conditions: &saml.Conditions{AudienceRestrictions: []saml.AudienceRestriction{{Audience: saml.Audience{Value: entity}}, {Audience: saml.Audience{Value: "urn:other"}}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := enterpriseSAMLAudience(entity)(tc.assertion); (err == nil) != tc.ok {
				t.Fatalf("SAML audience 限制结果错误：允许=%t，错误=%v", tc.ok, err)
			}
		})
	}
}

// TestEnterpriseSAMLIdentity 验证稳定属性唯一单值且不从 NameID/FriendlyName 回退；输入均为内存断言，无外部状态需清理。
func TestEnterpriseSAMLIdentity(t *testing.T) {
	one := saml.Attribute{Name: "external_id", Values: []saml.AttributeValue{{Value: "stable-user"}}}
	for _, tc := range []struct {
		name       string
		claim      string
		attributes []saml.Attribute
		want       string
	}{
		{"single_stable_attribute", "external_id", []saml.Attribute{one}, "stable-user"},
		{"preserve_exact_value", "external_id", []saml.Attribute{{Name: "external_id", Values: []saml.AttributeValue{{Value: " stable-user "}}}}, " stable-user "},
		{"missing_attribute", "external_id", nil, ""},
		{"blank_claim", " ", []saml.Attribute{one}, ""},
		{"different_attribute", "other", []saml.Attribute{one}, ""},
		{"friendly_name_not_identity", "external_id", []saml.Attribute{{Name: "other", FriendlyName: "external_id", Values: one.Values}}, ""},
		{"duplicate_same_value", "external_id", []saml.Attribute{one, one}, ""},
		{"multiple_values", "external_id", []saml.Attribute{{Name: "external_id", Values: []saml.AttributeValue{{Value: "a"}, {Value: "b"}}}}, ""},
		{"zero_values", "external_id", []saml.Attribute{{Name: "external_id"}}, ""},
		{"blank_value", "external_id", []saml.Attribute{{Name: "external_id", Values: []saml.AttributeValue{{Value: " \n\t"}}}}, ""},
		{"nested_nameid", "external_id", []saml.Attribute{{Name: "external_id", Values: []saml.AttributeValue{{Value: "text", NameID: &saml.NameID{Value: "transient"}}}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertion := &saml.Assertion{
				Subject:             &saml.Subject{NameID: &saml.NameID{Value: "transient", Format: string(saml.TransientNameIDFormat)}},
				AttributeStatements: []saml.AttributeStatement{{Attributes: tc.attributes}},
			}
			got, err := enterpriseSAMLIdentity(assertion, tc.claim)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("SAML 稳定身份提取错误：期望=%q 实际=%q 错误=%v", tc.want, got, err)
			}
		})
	}
	t.Run("duplicate_across_statements", func(t *testing.T) {
		assertion := &saml.Assertion{AttributeStatements: []saml.AttributeStatement{{Attributes: []saml.Attribute{one}}, {Attributes: []saml.Attribute{one}}}}
		if _, err := enterpriseSAMLIdentity(assertion, "external_id"); err == nil {
			t.Fatal("不同属性声明中的重复身份未拒绝")
		}
	})
	t.Run("nil_assertion", func(t *testing.T) {
		if _, err := enterpriseSAMLIdentity(nil, "external_id"); err == nil {
			t.Fatal("空断言未拒绝")
		}
	})
}
