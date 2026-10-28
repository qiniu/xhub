import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider, translate, useI18n } from "@/i18n";
import * as api from "@/lib/http/enterprise";
import { ApiError } from "@/lib/http/client";
import { SAML_IDENTITY_CLAIM, SAML_METADATA_URL } from "@/lib/enterpriseSSO";
import EnterpriseConfiguration from "./EnterpriseConfiguration";

// 独立刷新子表单由 SAMLRefresh.test.tsx 验证，避免父表单测试混入调度读取。
vi.mock("./SAMLRefresh", () => ({default: () => null}));
vi.mock("@/lib/http/enterprise");

/** 创建已保存 SAML 的脱密快照；无参数，返回独立对象，测试可修改边界，无外部服务或持久资源。 */
function savedConfiguration(): api.EnterpriseConfiguration {
  return {
    connection_id: "saml-saved", name: "原始 Identity", protocol: "saml", enabled: false,
    idp_metadata_url: SAML_METADATA_URL, identity_claim: SAML_IDENTITY_CLAIM, allow_http: false,
    client_id: "urn:xhub:original", redirect_uri: "https://console.example.com/auth/enterprise/saml/acs",
    return_uri: "https://console.example.com/ui/login",
  };
}

/** 测试用语言切换按钮；调用真实 i18n 状态，无业务写入，随组件清理。 */
function LocaleControl() {
  const { setLocale } = useI18n();
  return <button onClick={() => setLocale("en")}>Switch test locale</button>;
}

describe("SAML enterprise configuration", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
    document.cookie = "xhub_locale=; max-age=0; path=/";
  });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  // 前置中英文新建表单；验证默认值、协议字段、原始XML/名称保持及成功后才开放下载，清理DOM/mock。
  it.each(["en", "zh-CN"] as const)("%s creates SAML with explicit protocol and original input", async (locale) => {
    const t = (key: string) => translate(locale, key);
    vi.mocked(api.createSSO).mockImplementation(async (body) => ({ ...body, connection_id: "new-saml" }));
    render(<I18nProvider initialLocale={locale}><EnterpriseConfiguration sso publicOrigin="https://console.example.com" organizations={[{organization_id:"org",organization_alias:"Original Org"}]} /></I18nProvider>);
    fireEvent.change(screen.getByLabelText(t("enterprise.provider")), {target:{value:"saml"}});
    expect(screen.getByRole("heading")).toHaveTextContent(locale === "en" ? "Enterprise SSO (SAML)" : "企业单点登录（SAML）");
    expect(screen.getByLabelText(t("enterprise.idp_metadata_url"))).toHaveValue(SAML_METADATA_URL);
    expect(screen.getByLabelText(t("enterprise.identity_claim"))).toHaveValue(SAML_IDENTITY_CLAIM);
    expect(screen.getByLabelText(t("enterprise.samlEntityID"))).toHaveValue("https://console.example.com/auth/enterprise/saml/metadata");
    expect(screen.getByLabelText(t("enterprise.redirect_uri"))).toHaveValue("https://console.example.com/auth/enterprise/saml/acs");
    expect(screen.getByLabelText(t("enterprise.redirect_uri"))).toHaveAttribute("readonly");
    expect(screen.getByRole("switch", {name:t("enterprise.allowHTTP")})).not.toBeChecked();
    expect(screen.queryByLabelText(t("enterprise.issuer"))).not.toBeInTheDocument();
    expect(screen.queryByLabelText(t("enterprise.client_secret"))).not.toBeInTheDocument();
    expect(screen.getByRole("button", {name:t("enterprise.downloadMetadata")})).toBeDisabled();
    fireEvent.change(screen.getByLabelText(t("directory.organization")), {target:{value:"org"}});
    fireEvent.change(screen.getByLabelText(t("enterprise.ssoName")), {target:{value:"原始 Identity"}});
    fireEvent.change(screen.getByLabelText(t("enterprise.idp_metadata_url")), {target:{value:""}});
    const xml = "<EntityDescriptor>\n<!-- Original 原文 -->\n</EntityDescriptor>";
    fireEvent.change(screen.getByLabelText(t("enterprise.idp_metadata")), {target:{value:xml}});
    fireEvent.click(screen.getByRole("button", {name:t("enterprise.save")}));
    await waitFor(() => expect(api.createSSO).toHaveBeenCalledWith(expect.objectContaining({
      protocol:"saml", organization_id:"org", name:"原始 Identity", idp_metadata:xml,
      idp_metadata_url:"", identity_claim:SAML_IDENTITY_CLAIM, allow_http:false,
    })));
    await waitFor(() => expect(screen.getByRole("button", {name:t("enterprise.downloadMetadata")})).toBeEnabled());
    expect(screen.getByLabelText(t("enterprise.idp_metadata"))).toHaveValue(xml);
    expect(screen.getByRole("status")).toHaveTextContent(t("enterprise.success"));
    expect(document.body).not.toHaveTextContent("enterprise.saml");
  });

  // 前置公开IP HTTP；验证默认拒绝、明确开启后允许回调并展示 IdP 手工 XML 联调说明，但远程元数据 HTTP 仍拒绝；两语言错误可见，清理组件。
  it.each(["en", "zh-CN"] as const)("%s requires HTTP opt-in and retains HTTPS metadata downloads", async (locale) => {
    const t = (key: string) => translate(locale, key);
    vi.mocked(api.saveEnterprise).mockResolvedValue();
    vi.mocked(api.readEnterprise).mockResolvedValue({...savedConfiguration(), allow_http:true});
    render(<I18nProvider initialLocale={locale}><EnterpriseConfiguration id="saved" publicOrigin="http://111.62.156.153:3000" /></I18nProvider>);
    fireEvent.change(screen.getByLabelText(t("enterprise.ssoName")), {target:{value:"HTTP Identity"}});
    fireEvent.change(screen.getByLabelText(t("enterprise.provider")), {target:{value:"saml"}});
    expect(screen.getByText(t("enterprise.samlHTTPHint"))).toBeVisible();
    expect(screen.getByText(t("enterprise.samlExpiredTestingHint"))).toBeVisible();
    expect(screen.getByRole("switch", {name:t("enterprise.allowExpiredIDPCertificate")})).not.toBeChecked();
    expect(screen.getByRole("button", {name:t("enterprise.generateSAMLURLs")})).toBeDisabled();
    fireEvent.click(screen.getByRole("button", {name:t("enterprise.save")}));
    expect(await screen.findByRole("alert")).toHaveTextContent(t("enterprise.samlAddressError"));
    expect(api.saveEnterprise).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("switch", {name:t("enterprise.allowHTTP")}));
    fireEvent.click(screen.getByRole("button", {name:t("enterprise.generateSAMLURLs")}));
    fireEvent.change(screen.getByLabelText(t("enterprise.idp_metadata_url")), {target:{value:"http://identity.example.com/meta"}});
    fireEvent.click(screen.getByRole("button", {name:t("enterprise.save")}));
    expect(screen.getByRole("alert")).toHaveTextContent(t("enterprise.samlMetadataHTTPS"));
    expect(api.saveEnterprise).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText(t("enterprise.idp_metadata_url")), {target:{value:SAML_METADATA_URL}});
    fireEvent.click(screen.getByRole("button", {name:t("enterprise.save")}));
    await waitFor(() => expect(api.saveEnterprise).toHaveBeenCalledWith("/admin/enterprise/sso/saved", expect.objectContaining({
      protocol:"saml", allow_http:true, redirect_uri:"http://111.62.156.153:3000/auth/enterprise/saml/acs",
      return_uri:"http://111.62.156.153:3000/ui/login",
    })));
  });

  // 前置禁用配置证书过期；保存后回读警告，不提交服务端派生字段，仍允许下载；启用拒绝同样翻译，清理DOM/mock。
  it.each(["en", "zh-CN"] as const)("%s preserves disabled certificate warning and identifies enable errors", async (locale) => {
    const t = (key: string) => translate(locale, key);
    const expired = {...savedConfiguration(), idp_certificate_expired:true, issuer:"https://idp.example.com"};
    vi.mocked(api.saveEnterprise).mockResolvedValueOnce().mockRejectedValueOnce(new ApiError("private certificate detail", 400, {error:{type:"saml_certificate_expired"}}));
    vi.mocked(api.readEnterprise).mockResolvedValue(expired);
    render(<I18nProvider initialLocale={locale}><EnterpriseConfiguration id="saml-saved" initialConfiguration={savedConfiguration()} /></I18nProvider>);
    fireEvent.click(screen.getByRole("button", {name:t("enterprise.save")}));
    expect(await screen.findByRole("alert")).toHaveTextContent(t("enterprise.samlCertificateExpired"));
    expect(api.readEnterprise).toHaveBeenCalledWith("/admin/enterprise/sso/saml-saved");
    expect(vi.mocked(api.saveEnterprise).mock.calls[0][1]).not.toHaveProperty("idp_certificate_expired");
    expect(screen.getByRole("button", {name:t("enterprise.downloadMetadata")})).toBeEnabled();
    fireEvent.click(screen.getByRole("switch", {name:t("enterprise.enabled")}));
    fireEvent.click(screen.getByRole("button", {name:t("enterprise.save")}));
    await waitFor(() => expect(api.saveEnterprise).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByRole("button", {name:t("enterprise.save")})).toBeEnabled());
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    expect(screen.getByRole("alert")).toHaveTextContent(t("enterprise.samlCertificateExpired"));
    expect(screen.queryByText("private certificate detail")).not.toBeInTheDocument();
  });

  // 前置原配置未知证书状态，启用被服务端拒绝；验证接口类型也可产生提示，而不是依赖已有警告，清理DOM/mock。
  it("translates certificate rejection without a preloaded warning", async () => {
    vi.mocked(api.saveEnterprise).mockRejectedValue(new ApiError("private", 400, {error:{type:"saml_certificate_expired"}}));
    render(<I18nProvider initialLocale="en"><EnterpriseConfiguration id="saml-saved" initialConfiguration={savedConfiguration()} /></I18nProvider>);
    fireEvent.click(screen.getByRole("switch", {name:"Enable integration"}));
    fireEvent.click(screen.getByRole("button", {name:"Save integration"}));
    expect(await screen.findByRole("alert")).toHaveTextContent(translate("en", "enterprise.samlCertificateExpired"));
    expect(api.readEnterprise).not.toHaveBeenCalled();
  });

  // 前置已保存SAML及下载接口；先失败保留配置，再成功下载后台XML，验证临时URL和DOM清理，两语言无秘密回显。
  it.each(["en", "zh-CN"] as const)("%s downloads saved metadata and handles failure", async (locale) => {
    const t = (key: string) => translate(locale, key);
    const createURL = vi.fn().mockReturnValue("blob:metadata-test");
    const revokeURL = vi.fn();
    vi.stubGlobal("URL", class extends URL { static createObjectURL = createURL; static revokeObjectURL = revokeURL; });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const xml = '<EntityDescriptor entityID="urn:xhub:original"><!-- Original XML --></EntityDescriptor>';
    vi.mocked(api.readSSOMetadata).mockRejectedValueOnce(new Error("private certificate error")).mockResolvedValueOnce(xml);
    render(<I18nProvider initialLocale={locale}><EnterpriseConfiguration id="saml-saved" initialConfiguration={savedConfiguration()} /></I18nProvider>);
    const button = screen.getByRole("button", {name:t("enterprise.downloadMetadata")});
    fireEvent.click(button);
    expect(await screen.findByRole("alert")).toHaveTextContent(t("enterprise.error"));
    expect(screen.queryByText("private certificate error")).not.toBeInTheDocument();
    expect(createURL).not.toHaveBeenCalled();
    fireEvent.click(button);
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent(t("enterprise.metadataDownloaded")));
    expect(api.readSSOMetadata).toHaveBeenLastCalledWith("saml-saved");
    const blob = createURL.mock.calls[0][0] as Blob;
    expect(blob.type).toBe("application/samlmetadata+xml;charset=utf-8");
    const content = await new Promise((resolve) => {const reader = new FileReader(); reader.onload = () => resolve(reader.result); reader.readAsText(blob);});
    expect(content).toBe(xml);
    expect(click.mock.instances[0]).toHaveAttribute("download", "xhub-saml-saml-saved.xml");
    expect(revokeURL).toHaveBeenCalledWith("blob:metadata-test");
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(api.saveEnterprise).not.toHaveBeenCalled();
  });

  // 前置后台XML校验失败；验证原文保留、已保存状态不伪造、语言切换后文案和错误更新且输入不翻译，清理DOM/mock。
  it("keeps failed input and translates the same form when locale changes", async () => {
    vi.mocked(api.saveEnterprise).mockRejectedValue(new Error("private XML parser detail"));
    render(<I18nProvider initialLocale="zh-CN"><LocaleControl /><EnterpriseConfiguration id="saml-saved" initialConfiguration={savedConfiguration()} /></I18nProvider>);
    fireEvent.change(screen.getByLabelText("身份平台元数据地址"), {target:{value:""}});
    fireEvent.change(screen.getByLabelText("身份平台元数据 XML"), {target:{value:"<broken 原文"}});
    fireEvent.click(screen.getByRole("button", {name:"保存集成配置"}));
    await screen.findByRole("alert");
    expect(screen.queryByText("private XML parser detail")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", {name:"Switch test locale"}));
    expect(screen.getByLabelText("IdP metadata XML")).toHaveValue("<broken 原文");
    expect(screen.getByLabelText("SSO name")).toHaveValue("原始 Identity");
    expect(screen.getByRole("alert")).toHaveTextContent("Operation failed");
    expect(screen.queryByLabelText("身份平台元数据 XML")).not.toBeInTheDocument();
  });

  // 前置重载返回SAML；验证按持久协议呈现且禁止原地切换协议，重载失败不覆盖输入，无额外写入并清理DOM。
  it("loads and locks the saved protocol while retaining input on reload failure", async () => {
    vi.mocked(api.readEnterprise).mockResolvedValueOnce(savedConfiguration()).mockRejectedValueOnce(new Error("read failed"));
    vi.mocked(api.saveEnterprise).mockResolvedValue();
    render(<I18nProvider initialLocale="en"><EnterpriseConfiguration id="saml-saved" publicOrigin="https://console.example.com" /></I18nProvider>);
    fireEvent.click(screen.getByRole("button", {name:"Load configuration and status"}));
    await waitFor(() => expect(screen.getByLabelText("Identity provider setup template")).toHaveValue("saml"));
    expect(screen.getByLabelText("SP Entity ID")).toHaveValue("urn:xhub:original");
    fireEvent.click(screen.getByRole("button", {name:"Load configuration and status"}));
    await screen.findByRole("alert");
    expect(screen.getByLabelText("SP Entity ID")).toHaveValue("urn:xhub:original");
    expect(screen.getByLabelText("Identity provider setup template")).toBeDisabled();
    expect(api.saveEnterprise).not.toHaveBeenCalled();
  });

  // 前置尚未保存的SAML新建表单；验证可切回OIDC且请求不含隐藏SAML字段，创建成功协议锁定，清理DOM/mock。
  it("changes an unsaved template back to OIDC without submitting hidden SAML fields", async () => {
    vi.mocked(api.createSSO).mockImplementation(async (body) => ({...body,connection_id:"new-oidc"}));
    render(<I18nProvider initialLocale="en"><EnterpriseConfiguration sso organizations={[{organization_id:"org"}]} publicOrigin="https://console.example.com" /></I18nProvider>);
    fireEvent.change(screen.getByLabelText("Organization ID"), {target:{value:"org"}});
    fireEvent.change(screen.getByLabelText("SSO name"), {target:{value:"Original Identity"}});
    fireEvent.change(screen.getByLabelText("Identity provider setup template"), {target:{value:"saml"}});
    fireEvent.change(screen.getByLabelText("Identity provider setup template"), {target:{value:"entra"}});
    expect(screen.getByLabelText("Stable identity claim")).toHaveValue("oid");
    expect(screen.getByLabelText("Callback URL")).toHaveValue("https://console.example.com/auth/enterprise/callback");
    fireEvent.change(screen.getByLabelText("Client ID"), {target:{value:"original-oidc-client"}});
    fireEvent.change(screen.getByLabelText("OIDC issuer"), {target:{value:"https://id.example.com"}});
    fireEvent.click(screen.getByRole("button", {name:"Save integration"}));
    await waitFor(() => expect(api.createSSO).toHaveBeenCalled());
    const submitted = vi.mocked(api.createSSO).mock.calls[0][0];
    expect(submitted).toMatchObject({protocol:"oidc",client_id:"original-oidc-client",identity_claim:"oid"});
    for (const field of ["allow_http", "idp_metadata", "idp_metadata_url"]) expect(submitted).not.toHaveProperty(field);
    expect(screen.getByLabelText("Identity provider setup template")).toBeDisabled();
  });
});
