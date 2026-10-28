import { t as tRuntime } from "@/i18n/runtime";
import { createApiClient } from "./client";
import { getAuthHeaderName, getAuthToken, getRequestBaseUrl } from "./runtime";

const client = createApiClient({ getBaseUrl: getRequestBaseUrl, getAuthHeaderName });
export type EnterpriseConfiguration = Record<string, string | boolean | number>;
/** 独立 SSO 列表行；名称及标识属于登录配置，不依赖 SCIM 列表。 */
export interface SSOConfiguration {connection_id: string; name: string; enabled: boolean}
/** 加载已保存 SSO 列表；管理员页调用，无写入，失败抛错。 */
export function listSSO(): Promise<SSOConfiguration[]> {return client.get("/admin/enterprise/sso", {accessToken:getAuthToken()});}
/** 独立创建 SSO；body 为管理员输入，返回脱密配置，不开通 SCIM 或保存浏览器凭据。 */
export function createSSO(body: EnterpriseConfiguration): Promise<EnterpriseConfiguration> {return client.post("/admin/enterprise/sso", {accessToken:getAuthToken(),body});}
/** 删除 SSO；id 为配置标识，返回完成 Promise，不删除 SCIM，失败抛错。 */
export function deleteSSO(id: string): Promise<void> {return client.delete("/admin/enterprise/sso/"+encodeURIComponent(id),{accessToken:getAuthToken()});}
export interface ArchiveStatus { configuration: EnterpriseConfiguration | null; counts: Record<string, number>; events: { id: number; state: string; object_key: string; error: string }[] }

/** 读取管理员企业配置；path 为受控路由，返回配置或错误，不回显凭据，无写副作用。 */
export function readEnterprise<T>(path: string): Promise<T> { return client.get(path, { accessToken: getAuthToken() }); }
/** 读取已保存 SAML 配置的 SP 元数据；id 为配置标识，返回 XML 原文供管理员下载。
 * 管理表单调用；鉴权失败或响应缺少 XML 时抛错，不访问公开元数据入口，不保存凭据。
 */
export async function readSSOMetadata(id: string): Promise<string> {
  const result = await client.get<{metadata: string}>("/admin/enterprise/sso/" + encodeURIComponent(id) + "/metadata", {accessToken: getAuthToken()});
  if (typeof result?.metadata !== "string" || !result.metadata.trim()) throw new Error(tRuntime("uiErrors.samlMetadataInvalid"));
  return result.metadata;
}
/** 保存企业配置；path/body 为管理员输入，返回完成 Promise；凭据不写入浏览器存储。 */
export function saveEnterprise(path: string, body: EnterpriseConfiguration): Promise<void> { return client.put(path, { accessToken: getAuthToken(), body }); }
/** 执行归档探针或重试；action 为有限操作，返回完成 Promise；探针由后台清理。 */
export function archiveAction(action: "test" | "retry"): Promise<void> { return client.post("/admin/archive/kodo/" + action, { accessToken: getAuthToken(), body: {} }); }
/** 读取公开登录入口；返回受控名称和跳转路径，无秘密和写副作用，登录页调用。 */
export function enterpriseLoginOptions(): Promise<{ id: string; name: string; url: string }[]> { return client.get("/auth/enterprise/options"); }

/** SAML 刷新接口的公开状态；时间为 Unix 秒，0 表示尚无记录，租约和密钥不返回浏览器。 */
export interface SAMLRefreshStatus {connection_id:string;enabled:boolean;source_url:string;interval_hours:number;last_attempt:number;last_success:number;next_attempt:number;expires_at:number;error_code:string}
/** 对已保存的 SAML 配置立即执行受信元数据刷新；失败抛错，不自动重试写请求。 */
export function refreshSSOMetadata(id:string):Promise<SAMLRefreshStatus>{return client.post("/admin/enterprise/sso/"+encodeURIComponent(id)+"/refresh",{accessToken:getAuthToken(),body:{}});}
