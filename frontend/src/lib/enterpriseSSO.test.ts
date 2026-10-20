import { describe, expect, it } from "vitest";
import { enterpriseSSOOrigin, samlConfigurationIssue, samlFailureKey, ssoSaveConfiguration } from "./enterpriseSSO";
import { ApiError } from "./http/client";
import type { EnterpriseConfiguration } from "./http/enterprise";

/** 创建纯内存 SAML 配置；无参数，返回完整可信地址 fixture，调用方可改边界字段，无外部资源需清理。 */
function configuration(): EnterpriseConfiguration {
  return {
    protocol: "saml", name: "原始 SSO", idp_metadata_url: "https://sso.example.com/meta",
    client_id: "urn:xhub:test", identity_claim: "uid", allow_http: false,
    redirect_uri: "https://console.example.com/auth/enterprise/saml/acs",
    return_uri: "https://console.example.com/ui/login",
  };
}

describe("SAML form contracts", () => {
  // 验证到期证书例外必须显式 HTTP 与手工 XML；不修改原始输入，无资源清理。
  it("restricts expired certificate testing to explicit HTTP and pasted XML", () => {
    const value = {...configuration(), allow_expired_idp_certificate:true, allow_http:true, idp_metadata_url:"", idp_metadata:"<EntityDescriptor/>"};
    expect(samlConfigurationIssue(value)).toBeNull();
    for (const invalid of [{...value,allow_http:false},{...value,idp_metadata_url:"https://idp.example/meta"}]) expect(samlConfigurationIssue(invalid)).toBe("enterprise.samlExpiredTestingRequires");
    expect(ssoSaveConfiguration(value,true).allow_expired_idp_certificate).toBe(true);
    expect(ssoSaveConfiguration(value,false)).not.toHaveProperty("allow_expired_idp_certificate");
  });
  // 前置结构化证书错误、未知错误及缺正文；验证只翻译已确认类型，不暴露原始错误，纯内存无需清理。
  it("recognizes only the structured IdP certificate error", () => {
    expect(samlFailureKey(new ApiError("private detail", 400, {error:{type:"saml_certificate_expired"}}))).toBe("enterprise.samlCertificateExpired");
    for (const error of [null, new Error("saml_certificate_expired"), new ApiError("private", 400, null), new ApiError("private", 500, {error:{type:"other"}}), new ApiError("private", 400, "saml_certificate_expired")]) {
      expect(samlFailureKey(error)).toBeNull();
    }
  });

  // 前置 HTTPS/HTTP/非法源；验证仅 SAML 显式开关可接受 HTTP，OIDC 规则不变，纯函数无需清理。
  it("HTTP requires an explicit SAML setting and does not weaken OIDC", () => {
    expect(enterpriseSSOOrigin("http://111.62.156.153:3000", true)).toBe("");
    expect(enterpriseSSOOrigin("http://111.62.156.153:3000/", true, true)).toBe("http://111.62.156.153:3000");
    expect(enterpriseSSOOrigin("http://localhost:3000", true, true)).toBe("http://localhost:3000");
    expect(enterpriseSSOOrigin("http://localhost:3000", false, true)).toBe("");
    expect(enterpriseSSOOrigin("https://console.example.com", false)).toBe("https://console.example.com");
    for (const raw of ["", "bad", "javascript:alert(1)", "https://user:secret@example.com", "https://example.com/path", "https://example.com?x=1", "https://example.com#x"]) {
      expect(enterpriseSSOOrigin(raw, true, true), raw).toBe("");
    }
  });

  // 前置完整配置；验证 HTTPS URL、原始 XML 和绝对 URI 实体标识正常输入，原始配置不修改。
  it("accepts metadata URL or XML without rewriting the source", () => {
    const value = configuration();
    expect(samlConfigurationIssue(value)).toBeNull();
    const xml = "<EntityDescriptor>\n<!-- 原始内容 -->\n</EntityDescriptor>";
    expect(samlConfigurationIssue({ ...value, idp_metadata_url: "", idp_metadata: xml })).toBeNull();
    expect(samlConfigurationIssue({ ...value, client_id: "http://console.example.com/entity" })).toBeNull();
    expect(value.idp_metadata_url).toBe("https://sso.example.com/meta");
  });

  // 前置各元数据来源失败；验证 HTTP 开关不能放宽 IdP HTTPS，空来源和非法 URL 明确拒绝，无网络。
  it("rejects missing or unsafe identity-provider metadata URLs", () => {
    expect(samlConfigurationIssue({ ...configuration(), idp_metadata_url: "" })).toBe("enterprise.samlMetadataRequired");
    for (const url of ["http://idp.example.com/meta", "bad", "https://user:secret@idp.example.com/meta", "https://idp.example.com/meta#fragment"]) {
      expect(samlConfigurationIssue({ ...configuration(), allow_http: true, idp_metadata_url: url })).toBe("enterprise.samlMetadataHTTPS");
    }
  });

  // 前置回调/返回边界；验证 HTTP 显式授权、固定路径及同源，不将实体 URI 当作必须 HTTPS 的请求地址。
  it("checks callback and return boundaries", () => {
    const http = { ...configuration(), redirect_uri: "http://111.62.156.153:3000/auth/enterprise/saml/acs", return_uri: "http://111.62.156.153:3000/ui/login" };
    expect(samlConfigurationIssue(http)).toBe("enterprise.samlAddressError");
    expect(samlConfigurationIssue({ ...http, allow_http: true })).toBeNull();
    for (const patch of [
      { return_uri: "https://other.example.com/ui/login" },
      { redirect_uri: "https://console.example.com/wrong" },
      { return_uri: "https://console.example.com/ui/login?redirect=bad" },
      { redirect_uri: "invalid" },
      { redirect_uri: "https://user:secret@console.example.com/auth/enterprise/saml/acs" },
    ]) expect(samlConfigurationIssue({ ...configuration(), ...patch })).toBe("enterprise.samlAddressError");
    expect(samlConfigurationIssue({ ...configuration(), client_id: "not absolute" })).toBe("enterprise.samlEntityError");
    expect(samlConfigurationIssue({ ...configuration(), identity_claim: "" })).toBe("enterprise.samlIdentityRequired");
  });

  // 前置切换协议后的混合字段；验证请求只含当前协议字段、秘密不回送，用户名称/XML原文保持，纯内存清理。
  it("serializes only the selected protocol and preserves user input", () => {
    const mixed = { ...configuration(), idp_metadata: "<原始 XML/>", issuer: "https://issuer.example.com", client_secret: "secret", private_key: "must-not-send" };
    const saml = ssoSaveConfiguration(mixed, true);
    expect(saml).toMatchObject({ protocol: "saml", idp_metadata: "<原始 XML/>", name: "原始 SSO", allow_http: false });
    for (const field of ["issuer", "client_secret", "private_key"]) expect(saml).not.toHaveProperty(field);
    const oidc = ssoSaveConfiguration(mixed, false);
    expect(oidc).toMatchObject({ protocol: "oidc", issuer: "https://issuer.example.com", client_secret: "secret" });
    for (const field of ["idp_metadata", "idp_metadata_url", "allow_http", "private_key"]) expect(oidc).not.toHaveProperty(field);
    expect(mixed.private_key).toBe("must-not-send");
  });
});
