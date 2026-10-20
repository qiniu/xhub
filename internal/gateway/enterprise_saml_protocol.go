package gateway

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"io"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	xrv "github.com/mattermost/xml-roundtrip-validator"
	dsig "github.com/russellhaering/goxmldsig"
	"github.com/sunqirui1987/xhub/internal/iam"
)

const enterpriseSAMLMetadataLimit = 2 << 20

// enterpriseSAMLURL 校验 SAML 的浏览器端地址；raw 为绝对 URL，allowHTTP 只供显式开启的 SP/IdP HTTP 联调使用。
// 返回解析地址或错误；拒绝凭据、查询、片段和不完整地址，不执行 DNS 或网络访问；远程元数据下载仍由独立 HTTPS 策略约束。
func enterpriseSAMLURL(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Hostname() == "" || u.Opaque != "" ||
		(u.Scheme != "https" && !(allowHTTP && u.Scheme == "http")) ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return nil, errors.New("invalid enterprise SAML URL")
	}
	return u, nil
}

// enterpriseSAMLMaterial 生成单连接使用的 RSA2048 签名/解密材料；master 为现有企业 AES-GCM 主密钥。
// 返回已加密的 PKCS8 私钥 PEM 和公开自签名证书 PEM；配置创建调用，随机或加密失败时不返回部分材料，不写磁盘。
func enterpriseSAMLMaterial(master string) (encryptedKey, certificate string, err error) {
	if len(master) < 32 {
		return "", "", errors.New("enterprise encryption key requires at least 32 characters")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	serial.Add(serial, big.NewInt(1))
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "XHub SAML service provider"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	encryptedKey, err = enterpriseSecret(master, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})), false)
	if err != nil {
		return "", "", err
	}
	return encryptedKey, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

// enterpriseSAMLProvider 从持久化配置及已取得的 IdP metadata 构造严格 SP；master 仅用于解密该连接的私钥。
// 配置校验、登录发起与回调调用；返回提供方或错误，不下载 metadata、不写入状态，仅粘贴元数据可通过 HTTP 开关允许 IdP HTTP Redirect 登录。
func enterpriseSAMLProvider(c *iam.EnterpriseOIDC, master string, metadata string) (*saml.ServiceProvider, error) {
	if c == nil || c.Protocol != "saml" || strings.TrimSpace(c.ClientID) == "" ||
		strings.TrimSpace(c.Issuer) == "" || c.ConnectionID != strings.TrimSpace(c.ConnectionID) ||
		c.ConnectionID == "." || c.ConnectionID == ".." || strings.ContainsAny(c.ConnectionID, "/\\?#%") {
		return nil, errors.New("invalid enterprise SAML configuration")
	}
	if c.AllowExpiredIDPCertificate && (!c.AllowHTTP || strings.TrimSpace(c.IDPMetadataURL) != "" || strings.TrimSpace(c.IDPMetadata) == "") {
		return nil, errors.New("expired IdP certificate testing requires HTTP opt-in and pasted metadata")
	}
	entityURL, err := url.Parse(c.ClientID)
	if err != nil || !entityURL.IsAbs() {
		return nil, errors.New("enterprise SAML entity ID must be an absolute URI")
	}
	acs, err := enterpriseSAMLURL(c.RedirectURI, c.AllowHTTP)
	if err != nil || acs.Path != "/auth/enterprise/saml/acs" || acs.RawPath != "" {
		return nil, errors.New("invalid enterprise SAML ACS URL")
	}
	returnURL, err := enterpriseSAMLURL(c.ReturnURI, c.AllowHTTP)
	if err != nil || returnURL.Path != "/ui/login" || returnURL.RawPath != "" ||
		returnURL.Scheme != acs.Scheme || returnURL.Host != acs.Host {
		return nil, errors.New("invalid enterprise SAML return URL")
	}
	idp, err := enterpriseSAMLMetadata(metadata, c.Issuer, time.Now(), c.AllowHTTP && strings.TrimSpace(c.IDPMetadataURL) == "")
	if err != nil {
		return nil, err
	}
	plain, err := enterpriseSecret(master, c.Secret, true)
	if err != nil {
		return nil, errors.New("invalid enterprise SAML private key")
	}
	pair, err := tls.X509KeyPair([]byte(c.Certificate), []byte(plain))
	if err != nil {
		return nil, errors.New("invalid enterprise SAML key pair")
	}
	key, ok := pair.PrivateKey.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 || key.Validate() != nil {
		return nil, errors.New("enterprise SAML requires an RSA key of at least 2048 bits")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || !enterpriseSAMLCertificateCurrent(cert, time.Now()) {
		return nil, errors.New("enterprise SAML certificate is not currently valid for signing")
	}
	metadataURL := *returnURL
	metadataURL.Path = "/auth/enterprise/saml/metadata"
	// 创建前允许空连接 ID 做材料预校验；真正的登录和导出调用必须先取得持久化的连接 ID。
	if c.ConnectionID != "" {
		metadataURL.Path = "/auth/enterprise/" + c.ConnectionID + "/saml/metadata"
	}
	sp := &saml.ServiceProvider{
		EntityID:    c.ClientID,
		Key:         key,
		Certificate: cert,
		MetadataURL: metadataURL,
		// 静态导出供管理员注册时不使用库默认两天期限，元数据与本连接证书的剩余有效期保持一致。
		MetadataValidDuration:       time.Until(cert.NotAfter),
		AcsURL:                      *acs,
		IDPMetadata:                 idp,
		HTTPClient:                  enterpriseHTTPClient(),
		AuthnNameIDFormat:           saml.UnspecifiedNameIDFormat,
		AllowIDPInitiated:           false,
		SignatureMethod:             dsig.RSASHA256SignatureMethod,
		ValidateAudienceRestriction: enterpriseSAMLAudience(c.ClientID),
	}
	if c.AllowExpiredIDPCertificate {
		sp.SignatureVerifier = enterpriseSAMLExpiredVerifier{}
	}
	return sp, nil
}

// enterpriseSAMLExpiredVerifier 仅用于显式 HTTP 加手工 XML 的单连接联调；不修改全局时钟或断言时间。
// 依赖 goxmldsig v1.6.1 先以 Raw DER 精确匹配固定公钥、再检查受信副本日历的行为，升级库时需保留对应测试。
type enterpriseSAMLExpiredVerifier struct{}

// VerifySignature 包装 crewjam 为本次断言创建的验签上下文；返回原库完整签名验证结果。
// 只在内存副本忽略已过期的固定证书 NotAfter，保留 Raw、公钥、NotBefore 和所有签名检查；不改配置、XML 或时钟。
func (enterpriseSAMLExpiredVerifier) VerifySignature(ctx *dsig.ValidationContext, el *etree.Element) error {
	roots, err := ctx.CertificateStore.Certificates()
	if err != nil {
		return err
	}
	now := time.Now()
	copies := make([]*x509.Certificate, len(roots))
	for i, root := range roots {
		copy := *root
		if now.After(copy.NotAfter) && !now.Before(copy.NotBefore) {
			copy.NotAfter = now.Add(time.Hour)
		}
		copies[i] = &copy
	}
	local := *ctx
	local.CertificateStore = &dsig.MemoryX509CertificateStore{Roots: copies}
	_, err = local.Validate(el)
	return err
}

// enterpriseSAMLMetadata 解析一个受信任管理员提交的 IdP 实体；raw 限制为 2MiB，issuer 与 now 用于实体及有效期校验，allowHTTP 仅允许手工 XML 联调的 HTTP 登录端点。
// provider 构造调用；仅接受单一 EntityDescriptor、可用 Redirect SSO 和可解析签名证书，拒绝聚合/尾随 XML，不下载外部资源。
func enterpriseSAMLMetadata(raw, issuer string, now time.Time, allowHTTP bool) (*saml.EntityDescriptor, error) {
	fail := func() (*saml.EntityDescriptor, error) { return nil, errors.New("invalid enterprise SAML IdP metadata") }
	if len(raw) == 0 || len(raw) > enterpriseSAMLMetadataLimit || strings.TrimSpace(issuer) == "" {
		return fail()
	}
	if err := xrv.Validate(strings.NewReader(raw)); err != nil {
		return fail()
	}
	// 标准库 Unmarshal 会忽略后续根节点；先检查完整 token 流，防止多个实体或 DTD 被静默接受。
	decoder := xml.NewDecoder(strings.NewReader(raw))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail()
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || value.Name.Local != "EntityDescriptor" || value.Name.Space != "urn:oasis:names:tc:SAML:2.0:metadata" {
					return fail()
				}
			} else if value.Name.Local == "EntityDescriptor" || value.Name.Local == "EntitiesDescriptor" {
				return fail()
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return fail()
			}
		case xml.Directive:
			return fail()
		}
	}
	var entity saml.EntityDescriptor
	if roots != 1 || depth != 0 || xml.Unmarshal([]byte(raw), &entity) != nil ||
		entity.EntityID != issuer || (!entity.ValidUntil.IsZero() && !now.Before(entity.ValidUntil)) ||
		len(entity.IDPSSODescriptors) == 0 {
		return fail()
	}
	signingCerts, redirectEndpoints := 0, 0
	for _, descriptor := range entity.IDPSSODescriptors {
		if descriptor.ValidUntil != nil && !descriptor.ValidUntil.IsZero() && !now.Before(*descriptor.ValidUntil) {
			return fail()
		}
		protocolSupported := false
		for _, protocol := range strings.Fields(descriptor.ProtocolSupportEnumeration) {
			protocolSupported = protocolSupported || protocol == "urn:oasis:names:tc:SAML:2.0:protocol"
		}
		if !protocolSupported {
			return fail()
		}
		for _, endpoint := range descriptor.SingleSignOnServices {
			if endpoint.Binding == saml.HTTPRedirectBinding {
				if _, err := enterpriseSAMLURL(endpoint.Location, allowHTTP); err != nil {
					return fail()
				}
				redirectEndpoints++
			}
		}
		for _, descriptor := range descriptor.KeyDescriptors {
			if descriptor.Use != "" && descriptor.Use != "signing" {
				continue
			}
			for _, encoded := range descriptor.KeyInfo.X509Data.X509Certificates {
				der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded.Data), ""))
				if err != nil {
					return fail()
				}
				cert, err := x509.ParseCertificate(der)
				// IdP 证书是可信元数据固定的验签公钥，不是 TLS PKI；部分 IdP 长期保留过期自签名证书。
				// 配置/导出允许保留该材料；登录仍由默认验签库检查证书日历、签名和断言时效，绝不绕过到期错误。
				if err != nil || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageDigitalSignature == 0) {
					return fail()
				}
				signingCerts++
			}
		}
	}
	if signingCerts == 0 || redirectEndpoints == 0 {
		return fail()
	}
	return &entity, nil
}

// enterpriseSAMLCertificateCurrent 检查管理员固定信任的 SAML 证书是否在 now 有效且允许签名；cert 可为 nil。
// 仅 SP 自有材料校验调用，返回允许与否；IdP 元数据固定公钥不按证书日历拒绝，不要求公共 CA 链。
func enterpriseSAMLCertificateCurrent(cert *x509.Certificate, now time.Time) bool {
	return cert != nil && !now.Before(cert.NotBefore) && now.Before(cert.NotAfter) &&
		(cert.KeyUsage == 0 || cert.KeyUsage&x509.KeyUsageDigitalSignature != 0)
}

// enterpriseSAMLAudience 构造固定 SP 实体的断言 audience 校验器；entityID 为配置中的 SP 标识。
// SAML 验签后调用返回的函数，缺失 audience 或任一限制组不包含该 SP 均拒绝，不放宽库的签名与时效校验。
func enterpriseSAMLAudience(entityID string) func(*saml.Assertion) error {
	return func(assertion *saml.Assertion) error {
		if assertion == nil || assertion.Conditions == nil || len(assertion.Conditions.AudienceRestrictions) == 0 {
			return errors.New("enterprise SAML assertion requires an audience")
		}
		for _, restriction := range assertion.Conditions.AudienceRestrictions {
			if restriction.Audience.Value != entityID {
				return errors.New("enterprise SAML assertion audience does not match")
			}
		}
		return nil
	}
}

// enterpriseSAMLIdentity 从已验签断言中提取配置的稳定属性；claim 只匹配 Attribute.Name，不匹配 FriendlyName 或 NameID。
// 登录回调调用；返回原始非空单值或错误，全断言重复属性、多个值及嵌套 NameID 均拒绝，避免身份歧义和 transient 身份复用。
func enterpriseSAMLIdentity(assertion *saml.Assertion, claim string) (string, error) {
	if assertion == nil || strings.TrimSpace(claim) == "" {
		return "", errors.New("missing enterprise SAML identity attribute")
	}
	found, identity := false, ""
	for _, statement := range assertion.AttributeStatements {
		for _, attribute := range statement.Attributes {
			if attribute.Name != claim {
				continue
			}
			if found || len(attribute.Values) != 1 || attribute.Values[0].NameID != nil ||
				strings.TrimSpace(attribute.Values[0].Value) == "" {
				return "", errors.New("ambiguous enterprise SAML identity attribute")
			}
			found, identity = true, attribute.Values[0].Value
		}
	}
	if !found {
		return "", errors.New("missing enterprise SAML identity attribute")
	}
	return identity, nil
}
