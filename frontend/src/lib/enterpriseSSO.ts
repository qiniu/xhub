import { enterprisePublicOrigin } from "@/lib/http/directory";
import { ApiError } from "@/lib/http/client";
import type { EnterpriseConfiguration } from "@/lib/http/enterprise";

export const SAML_METADATA_URL = "https://sso.qiniu.io/saml2/meta";
export const SAML_IDENTITY_CLAIM = "urn:oid:0.9.2342.19200300.100.1.1";

/** 识别后台明确的 IdP 证书错误；参数为接口异常，返回固定翻译键或 null，供表单和摘要开关显示。
 * 仅信任结构化错误类型，不把原始 XML、上游错误或证书细节显示给用户；未知异常沿用通用失败。
 */
export function samlFailureKey(error: unknown): string | null {
  if (!(error instanceof ApiError)) return null;
  const body = error.body as {error?: {type?: unknown}} | null;
  return body?.error?.type === "saml_certificate_expired" ? "enterprise.samlCertificateExpired" : null;
}

/** 校验 SSO 地址生成源；OIDC 沿用原公开 HTTPS 限制，SAML 仅在显式允许时接受 HTTP（包括公开 IP）。
 * 参数为管理员输入、协议和 HTTP 开关；返回规范化源或空串，不更改输入，不影响 IdP 元数据的 HTTPS 要求。
 */
export function enterpriseSSOOrigin(raw: string, saml: boolean, allowHTTP = false): string {
  if (!saml) return enterprisePublicOrigin(raw);
  try {
    const url = new URL(raw.trim());
    if ((url.protocol !== "https:" && !(allowHTTP && url.protocol === "http:")) ||
      !url.hostname || url.username || url.password || url.search || url.hash || url.pathname !== "/") return "";
    return url.origin;
  } catch {
    return "";
  }
}

/** 校验 SAML 表单中可在客户端确定的结构；参数为未保存配置，返回翻译键或 null。
 * 不下载或解析不可信 XML；签名、证书、元数据内容和身份映射仍由后台验证，错误时调用方禁止提交。
 */
export function samlConfigurationIssue(value: EnterpriseConfiguration): string | null {
  const metadataURL = String(value.idp_metadata_url ?? "").trim();
  if (!metadataURL && !String(value.idp_metadata ?? "").trim()) return "enterprise.samlMetadataRequired";
  if (metadataURL) {
    try {
      const url = new URL(metadataURL);
      if (url.protocol !== "https:" || !url.hostname || url.username || url.password || url.hash) return "enterprise.samlMetadataHTTPS";
    } catch {
      return "enterprise.samlMetadataHTTPS";
    }
  }
  if (value.allow_expired_idp_certificate === true && (value.allow_http !== true || String(value.idp_metadata_url ?? "").trim() || !String(value.idp_metadata ?? "").trim())) return "enterprise.samlExpiredTestingRequires";
  try {
    const callback = new URL(String(value.redirect_uri ?? ""));
    const back = new URL(String(value.return_uri ?? ""));
    if (!enterpriseSSOOrigin(callback.origin, true, value.allow_http === true) ||
      callback.username || callback.password || callback.search || callback.hash ||
      back.username || back.password || back.search || back.hash ||
      callback.origin !== back.origin || callback.pathname !== "/auth/enterprise/saml/acs" ||
      back.pathname !== "/ui/login") return "enterprise.samlAddressError";
  } catch {
    return "enterprise.samlAddressError";
  }
  try {
    const entityID = String(value.client_id ?? "").trim();
    if (!entityID || /\s/.test(entityID) || !new URL(entityID).protocol) return "enterprise.samlEntityError";
  } catch {
    return "enterprise.samlEntityError";
  }
  if (!String(value.identity_claim ?? "").trim()) return "enterprise.samlIdentityRequired";
  return null;
}

/** 生成管理员 SSO 写请求；参数为当前字段及选中协议，返回新对象，避免切换模板后提交隐藏字段或服务端签名密钥。
 * OIDC 保留 issuer/secret，SAML 只提交元数据及显式 HTTP 开关；不修改用户原始 XML、声明或名称。
 */
export function ssoSaveConfiguration(value: EnterpriseConfiguration, saml: boolean): EnterpriseConfiguration {
  const fields = [
    "connection_id", "organization_id", "name", "enabled", "client_id", "redirect_uri", "return_uri", "identity_claim",
    ...(saml ? ["idp_metadata_url", "idp_metadata", "allow_http", "allow_expired_idp_certificate"] : ["issuer", "client_secret"]),
  ];
  const result: EnterpriseConfiguration = { protocol: saml ? "saml" : "oidc" };
  for (const field of fields) if (value[field] !== undefined) result[field] = value[field];
  if (saml) result.allow_http = value.allow_http === true;
  return result;
}
