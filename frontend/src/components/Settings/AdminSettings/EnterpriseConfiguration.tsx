"use client";
import React, { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {type DirectoryOrganization} from "@/lib/http/directory";
import { enterpriseSSOOrigin, SAML_IDENTITY_CLAIM, SAML_METADATA_URL, samlConfigurationIssue, samlFailureKey, ssoSaveConfiguration } from "@/lib/enterpriseSSO";
import SettingsSelect from "./SettingsSelect";
import { useT } from "@/i18n";
import { archiveAction, createSSO, readEnterprise, readSSOMetadata, saveEnterprise, type ArchiveStatus, type EnterpriseConfiguration as Configuration } from "@/lib/http/enterprise";

const fieldTypes: Record<string, React.HTMLInputTypeAttribute> = {
 client_secret: "password", access_key: "password", secret_key: "password",
 issuer: "url", redirect_uri: "url", return_uri: "url", idp_metadata_url: "url",
};

/** 展示 SSO 或 Kodo 管理表单；id 为目录连接，省略时配置归档；返回国际化可访问界面。
 * sso 支持无 SCIM 的独立创建，organizations 为组织选项；publicOrigin 为初始公开源，initialConfiguration 为脱密快照。
 * embedded 去除嵌套卡片，onSaved 接收成功后的脱密配置，关闭弹窗卸载本表单并清除秘密。
 * 配置可显式重载，说明与队列明细默认折叠；SAML 的 HTTP 开关仅影响 XHub 回调/返回，IdP 始终要求 HTTPS。
 * 已持久化连接不允许原地切换协议；尚未配置的旧目录入口仍可选择协议，保存后的 Entity ID 以后台返回为准。
 * 不把秘密存入 localStorage，保存后清空凭据，失败保留用户输入；已保存 SAML 可下载后台生成的 SP 元数据。
 */
export default function EnterpriseConfiguration({ id, sso = false, organizations = [], publicOrigin = "", initialConfiguration, embedded = false, onSaved }: { id?: string; sso?: boolean; organizations?: DirectoryOrganization[]; publicOrigin?: string; initialConfiguration?: Configuration; embedded?: boolean; onSaved?: (configuration: Configuration) => void }) {
 const t = useT(); const oidc = sso || !!id;
 const [publicURL,setPublicURL]=useState(publicOrigin);
 const [provider, setProvider] = useState(initialConfiguration?.protocol === "saml" ? "saml" : "oidc");
 const saml = oidc && provider === "saml";
 const path = oidc ? "/admin/enterprise/sso/" + encodeURIComponent(id ?? "") : "/admin/archive/kodo";
 let fields = oidc ? ["issuer", "client_id", "client_secret", "redirect_uri", "return_uri", "identity_claim"] : ["bucket", "region", "prefix", "access_key", "secret_key"];
 if (saml) fields = ["idp_metadata_url", "client_id", "redirect_uri", "return_uri", "identity_claim"];
 const [value, setValue] = useState<Configuration>(initialConfiguration ?? (oidc ? { protocol: "oidc", identity_claim: "sub", enabled: false } : { region: "z0", prefix: "billing", interval_seconds: 2, batch_size: 20, enabled: false }));
 const origin = enterpriseSSOOrigin(publicURL, saml, value.allow_http === true);
 const [status, setStatus] = useState<ArchiveStatus | null>(null);
 const [busy, setBusy] = useState(false); const [result, setResult] = useState<"success" | "error" | "metadataDownloaded" | null>(null);
 const [validation, setValidation] = useState<string | null>(null);
 const [savedSAML, setSavedSAML] = useState(initialConfiguration?.protocol === "saml");
 const [loadedConfiguration, setLoadedConfiguration] = useState(!!initialConfiguration);
 const savedID = id || String(value.connection_id ?? "");
 const certificateExpired = saml && value.idp_certificate_expired === true;
 let titleKey = oidc ? "oidc" : "archive";
 if (saml) titleKey = "saml";

 /** 加载配置及归档状态；无参数，返回 Promise，首次未配置显示默认值，接口错误显示固定翻译。 */
 async function load() { setBusy(true); setResult(null); setValidation(null); try {
  if (oidc) { const configuration = await readEnterprise<Configuration>(path); setValue(configuration); setProvider(configuration.protocol === "saml" ? "saml" : "oidc"); setSavedSAML(configuration.protocol === "saml"); setLoadedConfiguration(true); }
  else { const snapshot = await readEnterprise<ArchiveStatus>(path); setStatus(snapshot); if (snapshot.configuration) setValue(snapshot.configuration); }
 } catch { setResult("error"); } finally { setBusy(false); } }
 /** 保存当前协议的原始输入；event 为表单事件，返回 Promise。SAML 先验证地址，不提交隐藏协议字段。
  * 成功后清空秘密并允许导出已保存元数据；SAML 更新后回读派生 issuer 和 IdP 证书状态。
  * 失败保留输入且不重放写入；服务端仍负责 XML 与证书验证，证书过期错误使用固定翻译。
  */
 async function save(event: React.FormEvent) { event.preventDefault(); setResult(null); const issue = saml ? samlConfigurationIssue(value) : null; setValidation(issue); if (issue) return; setBusy(true); try {
  const body = oidc ? ssoSaveConfiguration(value, saml) : value;
  let saved: Configuration;
  if (oidc && !id) saved = await createSSO(body);
  else {await saveEnterprise(path, body); saved = saml ? await readEnterprise<Configuration>(path) : body;}
  const copy = { ...saved }; for (const key of ["client_secret", "access_key", "secret_key", "private_key"]) delete copy[key]; setValue(copy); setSavedSAML(saml); setLoadedConfiguration(true); onSaved?.(copy); setResult("success");
 } catch (error) {const key = samlFailureKey(error); if (saml && key) setValidation(key); else setResult("error");} finally { setBusy(false); } }
 /** 执行归档操作；action 为探针/失败重试，返回 Promise，失败显示可观察提示，不泄露服务响应。 */
 async function action(action: "test" | "retry") { setBusy(true); setResult(null); try { await archiveAction(action); setResult("success"); } catch { setResult("error"); } finally { setBusy(false); } }
 /** 由管理员明确生成当前协议地址；使用已校验源，SAML 已填写实体 ID 保留，回调路径固定，不隐式授权 HTTP。 */
 function generateURLs() { setValidation(null); setValue((old) => ({ ...old, ...(saml ? {client_id: old.client_id || origin + "/auth/enterprise/saml/metadata"} : {}), redirect_uri: origin + (saml ? "/auth/enterprise/saml/acs" : "/auth/enterprise/callback"), return_uri: origin + "/ui/login" })); }
 /** 切换模板并设置该协议的默认值；next 为下拉值，保留名称等用户内容，OIDC 模板仍仅辅助稳定声明。
  * 新切入 SAML 可展示当前 XHub 源生成的地址，但 HTTP 默认不授权，保存仍受独立开关校验。
  */
 function changeProvider(next: string) {
  setProvider(next); setValidation(null); setResult(null);
  const source = publicURL || window.location.origin;
  if (next === "saml" && !publicURL) setPublicURL(source);
  const nextOrigin = enterpriseSSOOrigin(source, next === "saml", true);
  setValue((old) => next === "saml" ? {
   ...old, protocol: "saml", identity_claim: SAML_IDENTITY_CLAIM, idp_metadata_url: SAML_METADATA_URL, idp_metadata: "", allow_http: false,
   client_id: nextOrigin ? nextOrigin + "/auth/enterprise/saml/metadata" : "",
   redirect_uri: nextOrigin ? nextOrigin + "/auth/enterprise/saml/acs" : "", return_uri: nextOrigin ? nextOrigin + "/ui/login" : "",
  } : {...old, protocol: "oidc", identity_claim: next === "entra" ? "oid" : "sub", ...(saml ? {client_id: "", redirect_uri: nextOrigin ? nextOrigin + "/auth/enterprise/callback" : "", return_uri: nextOrigin ? nextOrigin + "/ui/login" : ""} : {})});
 }
 /** 下载当前已保存配置的元数据；无参数，读取管理员接口后生成本地 XML 文件，结束释放临时 Blob URL。
  * 不从表单拼装签名证书，不下载未保存配置；失败仅展示翻译文案，不泄露接口错误或秘密。
  */
 async function downloadMetadata() {
  if (!savedID || !savedSAML || !saml || busy) return;
  setBusy(true); setResult(null); setValidation(null);
  let url = "";
  try {
   const xml = await readSSOMetadata(savedID);
   url = URL.createObjectURL(new Blob([xml], {type: "application/samlmetadata+xml;charset=utf-8"}));
   const anchor = document.createElement("a"); anchor.href = url; anchor.download = "xhub-saml-" + encodeURIComponent(savedID) + ".xml";
   document.body.appendChild(anchor); try { anchor.click(); } finally { anchor.remove(); }
   setResult("metadataDownloaded");
  } catch {setResult("error");} finally {if (url) URL.revokeObjectURL(url); setBusy(false);}
 }
 return <section className={(oidc || embedded ? "" : "rounded-xl border bg-card p-6 ") + "[&_label]:flex [&_label]:flex-col [&_label]:gap-2 [&_label]:text-sm [&_summary]:cursor-pointer [&_summary]:text-sm [&_summary]:font-medium"} aria-label={embedded ? undefined : t("enterprise." + titleKey + "Title")}>
  <h3 className="text-base font-semibold">{t("enterprise." + titleKey + "Title")}</h3>
  <p className="mt-1 text-sm text-muted-foreground">{t("enterprise." + titleKey + "Brief")}</p>
  <details className="my-4 text-muted-foreground"><summary>{t("enterprise.setupHelp")}</summary><p className="mt-3 text-sm">{t("enterprise." + titleKey + "Hint")}</p><ol className="mt-3 space-y-2 text-sm">{[1,2,3].map((step) => <li key={step}>{t("enterprise." + titleKey + "Step" + step)}</li>)}</ol>{oidc && <p className="mt-3 text-sm">{t("enterprise.providerHint")}</p>}</details>
  <div className="flex flex-wrap gap-2">
  {oidc && <label className="w-full max-w-xl">{t("enterprise.publicURL")}<Input type="url" value={publicURL} placeholder="https://console.example.com" onChange={(event)=>setPublicURL(event.target.value)} /></label>}
  {oidc && <Button variant="outline" type="button" disabled={!origin || busy} onClick={generateURLs}>{t(saml ? "enterprise.generateSAMLURLs" : "enterprise.generateURLs")}</Button>}
  {(!oidc || id) && <Button variant="outline" type="button" disabled={busy} onClick={load}>{t("enterprise.load")}</Button>}
  {saml && <Button variant="outline" type="button" disabled={!savedID || !savedSAML || busy} onClick={downloadMetadata}>{t("enterprise.downloadMetadata")}</Button>}
  </div>
  <form onSubmit={save} className="my-5 grid max-w-3xl gap-4 sm:grid-cols-2">
   {oidc && !id && <label>{t("directory.organization")}<SettingsSelect required value={String(value.organization_id ?? "")} onChange={(event)=>setValue((old)=>({...old,organization_id:event.target.value}))}><option value="">{t("directory.choose")}</option>{organizations.map((org)=><option key={org.organization_id} value={org.organization_id}>{org.organization_alias || org.organization_id}</option>)}</SettingsSelect></label>}
   {oidc && <label>{t("enterprise.ssoName")}<Input required value={String(value.name ?? "")} onChange={(event)=>setValue((old)=>({...old,name:event.target.value}))} /></label>}
   {oidc && <label className="sm:col-span-2">{t("enterprise.provider")}<SettingsSelect disabled={!!savedID && loadedConfiguration} value={provider} onChange={(event) => changeProvider(event.target.value)}>{["oidc", "entra", "okta", "keycloak", "auth0", "saml"].map((id) => <option key={id} value={id}>{t("enterprise.providers." + id)}</option>)}</SettingsSelect></label>}
   {fields.map((field) => <label key={field}>{t("enterprise." + (saml && field === "client_id" ? "samlEntityID" : field))}{field === "region" ? <SettingsSelect value={String(value.region ?? "z0")} onChange={(event) => setValue((old) => ({ ...old, region: event.target.value }))}>{["z0", "cn-east-2", "z1", "z2", "na0", "as0"].map((region) => <option key={region} value={region}>{t("enterprise.regions." + region)}</option>)}</SettingsSelect> : <Input autoComplete="off" readOnly={saml && (field === "redirect_uri" || field === "return_uri")} type={fieldTypes[field] || "text"} value={String(value[field] ?? "")} onChange={(event) => setValue((old) => ({ ...old, [field]: event.target.value }))} />}</label>)}
   {saml && <>
    <label className="sm:col-span-2">{t("enterprise.idp_metadata")}<Textarea rows={6} value={String(value.idp_metadata ?? "")} onChange={(event) => setValue((old) => ({...old,idp_metadata:event.target.value}))} /></label>
    <p className="text-sm text-muted-foreground sm:col-span-2">{t("enterprise.samlMetadataHint")}</p>
    <p className="text-sm text-muted-foreground sm:col-span-2">{t("enterprise.samlEntityHint")}</p>
    <label className="!flex-row items-center sm:col-span-2"><Switch checked={value.allow_http === true} onCheckedChange={(checked) => {setValidation(null);setValue((old) => ({...old,allow_http:checked}));}} />{t("enterprise.allowHTTP")}</label>
    <p className="text-sm text-muted-foreground sm:col-span-2">{t("enterprise.samlHTTPHint")}</p>
    <label className="!flex-row items-center sm:col-span-2"><Switch checked={value.allow_expired_idp_certificate === true} onCheckedChange={(checked) => {setValidation(null);setValue((old) => ({...old,allow_expired_idp_certificate:checked}));}} />{t("enterprise.allowExpiredIDPCertificate")}</label>
    <p className="text-sm text-muted-foreground sm:col-span-2">{t("enterprise.samlExpiredTestingHint")}</p>
    <p className="text-sm text-muted-foreground sm:col-span-2">{t("enterprise.samlDownloadHint")}</p>
   </>}
   {!oidc && <>
    <label>{t("enterprise.interval")}<SettingsSelect value={String(value.interval_seconds ?? 2)} onChange={(event) => setValue((old) => ({...old, interval_seconds: Number(event.target.value)}))}>{[2, 30, 60, 300, 900, 3600, 86400].map((seconds) => <option key={seconds} value={seconds}>{t("enterprise.intervals." + seconds)}</option>)}{![2, 30, 60, 300, 900, 3600, 86400].includes(Number(value.interval_seconds ?? 2)) && <option value={String(value.interval_seconds)}>{t("enterprise.intervalCustom", {seconds: value.interval_seconds})}</option>}</SettingsSelect></label>
    <label>{t("enterprise.batchSize")}<Input type="number" min="1" max="200" step="1" required value={String(value.batch_size ?? 20)} onChange={(event) => setValue((old) => ({...old, batch_size: event.target.value === "" ? "" : Number(event.target.value)}))} /></label>
    <div className="space-y-2 rounded-lg bg-muted/40 p-4 text-sm text-muted-foreground sm:col-span-2">
      <p>{t("enterprise.scheduleHint")}</p><p>{t("enterprise.formatHint")}</p>
      <p>{t("enterprise.contentHint")}</p><p>{t("enterprise.archiveSafety")}</p>
      <p className="break-all">{t("enterprise.objectPath", {prefix: value.prefix || "billing"})}</p>
    </div>
   </>}
   <div className="flex flex-wrap items-center justify-between gap-3 border-t pt-4 sm:col-span-2"><label className="!flex-row items-center"><Switch checked={!!value.enabled} onCheckedChange={(checked) => setValue((old) => ({ ...old, enabled: checked }))} />{t("enterprise.enabled")}</label>
   <Button type="submit" disabled={busy}>{t("enterprise.save")}</Button></div>
  </form>
  {!oidc && <div className="flex flex-wrap gap-2"><Button variant="outline" type="button" disabled={busy} onClick={() => action("test")}>{t("enterprise.test")}</Button><Button variant="outline" type="button" disabled={busy} onClick={() => action("retry")}>{t("enterprise.retry")}</Button></div>}
  {certificateExpired && <p className="mt-3 text-sm text-destructive" role="alert">{t("enterprise.samlCertificateExpired")}</p>}
  {validation && !(certificateExpired && validation === "enterprise.samlCertificateExpired") && <p className="mt-3 text-sm text-destructive" role="alert">{t(validation)}</p>}
  {result && <p className={"mt-3 text-sm " + (result === "error" ? "text-destructive" : "text-muted-foreground")} role={result === "error" ? "alert" : "status"}>{t("enterprise." + result)}</p>}
  {status && <div className="mt-4 rounded-md bg-muted/40 p-3 text-sm" aria-label={t("enterprise.queue")}><p>{t("enterprise.counts", { pending: status.counts.pending ?? 0, processing: status.counts.processing ?? 0, failed: status.counts.failed ?? 0, done: status.counts.done ?? 0 })}</p><details className="mt-3"><summary>{t("enterprise.queueDetails")}</summary><ul className="mt-2 space-y-2 break-all text-muted-foreground">{status.events.map((event) => <li key={event.id}>{t("enterprise.state." + event.state)} · {event.object_key}</li>)}</ul></details></div>}
 </section>;
}
