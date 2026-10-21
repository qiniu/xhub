import { expect, test, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { GATEWAY, UPSTREAM, loginAdmin, login, sessionBearer, stableGoto, uiPath, t } from "./helpers";
import { translate } from "../src/i18n/translate";
import { requireAuthorizationError } from "./assertions";

/** 读取本轮私有企业控制端；返回本地 URL，仅浏览器测试使用；未启用夹具时明确失败。 */
function control(): string {
 return JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8")).control;
}
/** 注册自然到期情况；title 为独立报告标题。私有控制端缩短当前会话 TTL，真实时钟经过边界，验证到期前 200、到期后 401 与页面重新登录；上下文及 schema 回收会话。 */
export function registerSessionExpiry(title: string) {
 test(title, async ({page}) => {
  await loginAdmin(page);
  const session = await sessionBearer(page);
  const headers = {Authorization: "Bearer " + session};
  const shortened = await page.request.post(control() + "/session/ttl", {data: {session}});
  expect(shortened.status(), await shortened.text()).toBe(200);
  expect((await page.request.get(GATEWAY + "/auth/me", {headers})).status()).toBe(200);
  const before = await count(page);
  await expect.poll(async () => (await page.request.get(GATEWAY + "/auth/me", {headers})).status(), {timeout: 10_000}).toBe(401);
  const expired = await page.request.get(GATEWAY + "/auth/me", {headers});
  requireAuthorizationError(expired.status(), await expired.json(), 401);
  expect(await count(page)).toEqual(before);
  await page.goto(uiPath("/api-keys"));
  await expect(page).toHaveURL(/\/login/);
  await loginAdmin(page);
  expect(await sessionBearer(page)).not.toBe(session);
  expect((await page.request.get(GATEWAY + "/auth/me", {headers: {Authorization: "Bearer " + await sessionBearer(page)}})).status()).toBe(200);
 });
}
/** 读取真实上游调用计数；page 为当前浏览器，返回计数快照；控制端错误直接失败，不改变数据。 */
async function count(page:Page) {
 const response=await page.request.get(UPSTREAM+"/e2e/request-count");expect(response.status()).toBe(200);return response.json();
}
/** 注册退出凭据重放；前置真实管理员登录，验证旧 bearer/JWT 均 401 且无上游调用，重新登录恢复；上下文/会话随 schema 回收。 */
export function registerLogoutReplay(title:string) {
 test(title, async({page})=>{
  await loginAdmin(page);const bearer=await sessionBearer(page);const jwt=(await page.context().cookies()).find(c=>c.name==="token")!.value;
  await page.getByRole("button",{name:/^Account menu/}).click();const logout=page.waitForResponse(r=>/\/(?:auth\/)?logout$/.test(new URL(r.url()).pathname));await page.getByRole("button",{name:t("Logout"),exact:true}).click();expect((await logout).status()).toBe(200);await expect(page).toHaveURL(/\/login/);
  const before=await count(page);
  for(const token of [bearer,jwt]) {
   const refused=await page.request.get(GATEWAY+"/auth/me",{headers:{Authorization:"Bearer "+token}});requireAuthorizationError(refused.status(),await refused.json(),401);
  }
  expect(await count(page)).toEqual(before);await page.goto(uiPath("/api-keys"));await expect(page).toHaveURL(/\/login/);await loginAdmin(page);expect(await sessionBearer(page)).not.toBe(bearer);
 });
}
/** 注册会话连续性；前置自建成员，刷新和导航保持同一会话，管理员重置密码使旧会话失效，页面跳转并使用新密码恢复；finally 删除自建用户。 */
export function registerSessionContinuity(title:string) {
 test(title, async({page})=>{
  await loginAdmin(page);const headers={Authorization:"Bearer "+await sessionBearer(page)};const email="continuity-"+Date.now()+"@example.com";
  const created=await page.request.post(GATEWAY+"/user/new",{headers,data:{user_email:email,password:"continuity-password"}});expect(created.status()).toBe(200);const user=await created.json();
  try {
   await login(page,email,"continuity-password");await expect(page.getByTestId("admin-shell")).toBeVisible();const token=await sessionBearer(page);
   for(const path of ["/api-keys","/usage","/logs"]){await stableGoto(page,path);await page.reload();expect(await sessionBearer(page)).toBe(token);expect((await page.request.get(GATEWAY+"/auth/me",{headers:{Authorization:"Bearer "+token}})).status()).toBe(200);}
   const reset=await page.request.post(GATEWAY+"/user/set_password",{headers,data:{user_id:user.user_id,password:"continuity-new-password"}});expect(reset.status(),await reset.text()).toBe(200);
   const denied=await page.request.get(GATEWAY+"/auth/me",{headers:{Authorization:"Bearer "+token}});requireAuthorizationError(denied.status(),await denied.json(),401);
   await page.goto(uiPath("/api-keys"));await expect(page).toHaveURL(/\/login/);await login(page,email,"continuity-new-password");await expect(page.getByTestId("admin-shell")).toBeVisible();
  } finally {expect((await page.request.post(GATEWAY+"/user/delete",{headers,data:{user_id:user.user_id}})).status()).toBe(200);expect((await page.request.get(GATEWAY+"/user/info?user_id="+user.user_id,{headers})).status()).toBe(404);}
 });
}
/** 注册密钥轮换；前置页面创建独立密钥，实际推理后轮换并验证旧密钥拒绝/不转发，新密钥成功且内部身份及日志不变；finally 删除密钥。 */
export function registerKeyRotation(title:string, locale: "zh-CN" | "en" = "zh-CN") {
 test(title,async({page})=>{
  await loginAdmin(page);
  if (locale === "en") await page.getByRole("button", {name:"English",exact:true}).click();
  const t = (key: string) => translate(locale, key);
  const alias = "LiteLLM-模型-rotation-" + Date.now();
  const headers={Authorization:"Bearer "+await sessionBearer(page)};
  await page.getByTestId("create-key-button").click();await page.getByLabel(t("Key Name")).fill(alias);const created=page.waitForResponse(r=>r.url().endsWith("/key/generate")&&r.request().method()==="POST");await page.getByRole("button",{name:t("pages.apiKeys.createSubmit"),exact:true}).click();const response=await created;expect(response.status()).toBe(200);const key=await response.json();let secret=key.key;
  /** 调用实际数据面；token 为待验证密钥、suffix 避免缓存，返回响应；账单随私有 schema 清理。 */
  const invoke=(token:string,suffix:string)=>page.request.post(GATEWAY+"/v1/chat/completions",{headers:{Authorization:"Bearer "+token},data:{model:"gpt-4o-mini",messages:[{role:"user",content:"Verify rotated credential "+suffix}]}});
  try {
   await expect(page.getByText(t("createdKey.warning"),{exact:true})).toBeVisible();
   await expect(page.getByRole("button",{name:t("Copy Virtual Key"),exact:true})).toBeVisible();
   expect(key.key_alias).toBe(alias);
   const first=await invoke(secret,"before");expect(first.status()).toBe(200);const id=first.headers()["x-litellm-call-id"];
   await page.keyboard.press("Escape");await stableGoto(page,"/api-keys?key="+key.token_id);await page.getByRole("button",{name:t("More key actions"),exact:true}).click();await page.getByRole("menuitem",{name:t("Regenerate Key"),exact:true}).click();const rotated=page.waitForResponse(r=>r.url().includes("/regenerate")&&r.request().method()==="POST");await page.getByRole("button",{name:t("Regenerate"),exact:true}).click();const fresh=await rotated;expect(fresh.status()).toBe(200);secret=(await fresh.json()).key;expect(secret).not.toBe(key.key);
   const before=await count(page);const denied=await invoke(key.key,"revoked");requireAuthorizationError(denied.status(),await denied.json(),401);expect(await count(page)).toEqual(before);
   const second=await invoke(secret,"after");expect(second.status()).toBe(200);const nextID=second.headers()["x-litellm-call-id"];
   const info=await page.request.get(GATEWAY+"/key/info?key="+key.token_id,{headers});expect(info.status()).toBe(200);expect((await info.json()).info.token_id).toBe(key.token_id);
   await expect.poll(async()=> (await page.request.get(GATEWAY+"/spend/logs/ui/"+nextID,{headers})).status()).toBe(200);
   const oldLog=await page.request.get(GATEWAY+"/spend/logs/ui/"+id,{headers});const newLog=await page.request.get(GATEWAY+"/spend/logs/ui/"+nextID,{headers});expect(oldLog.status()).toBe(200);expect((await oldLog.json()).api_key).toBe((await newLog.json()).api_key);
   await page.keyboard.press("Escape");await stableGoto(page,"/logs");await expect(page.getByRole("main")).toContainText("gpt-4o-mini");
  } finally {expect((await page.request.post(GATEWAY+"/key/delete",{headers,data:{keys:[key.token_id||secret]}})).status()).toBe(200);expect((await page.request.get(GATEWAY+"/key/info?key="+key.token_id,{headers})).status()).toBe(404);}
 });
}
/** 注册 SCIM 轮换或冲突；前置私有身份锚点，通过页面生成凭据与启用，真实 SCIM 请求验证身份稳定/事务回滚；finally 控制端回收专属身份。 */
export function registerSCIMBoundary(kind:"rotation"|"conflict",title:string) {
 test(title,async({page})=>{
  await loginAdmin(page);const headers={Authorization:"Bearer "+await sessionBearer(page)};const c=control();const provision=await page.request.post(c+"/identity");expect(provision.status()).toBe(200);const identity=await provision.json();const route=GATEWAY+"/admin/directory/connections/"+identity.connection_id;const base=GATEWAY+"/scim/v2/connections/"+identity.connection_id;const name="SCIM boundary "+kind;
  /** 通过真实弹窗重新连接并读取一次性 token；返回凭据，保存生成和启用副作用仅限当前私有组织。 */
  const configure=async()=>{
   await stableGoto(page,"/admin-panel");await page.getByRole("tab",{name:"SCIM",exact:true}).click();await page.getByRole("button",{name:/^(配置 SCIM|新建目录连接)$/}).click();const dialog=page.getByRole("dialog",{name:"配置 SCIM"});await dialog.getByLabel("组织 ID").selectOption(identity.organization_id);await dialog.getByLabel("目录名称").fill(name);await dialog.getByLabel("XHub 公开访问地址").fill("https://console.example.com");await dialog.getByRole("button",{name:"创建目录连接"}).click();const token=await dialog.getByLabel("SCIM 同步凭据").inputValue();expect(token).toBeTruthy();await dialog.getByRole("button",{name:"关闭配置"}).click();await page.getByRole("switch",{name:"启用 "+name,exact:true}).click();await expect(page.getByRole("switch",{name:"停用 "+name,exact:true})).toBeChecked();return token;
  };
  try {
   expect((await page.request.delete(route,{headers})).status()).toBe(204);const token=await configure();const scim={Authorization:"Bearer "+token};const listed=await page.request.get(base+"/Users",{headers:scim});expect(listed.status(),await listed.text()).toBe(200);const before=await listed.json();expect(before.totalResults).toBe(1);
   if(kind==="rotation") {
    await page.getByRole("button",{name:"删除 "+name,exact:true}).click();await page.getByRole("dialog").getByRole("button",{name:"删除连接",exact:true}).click();await expect(page.getByRole("dialog")).not.toBeVisible();const fresh=await configure();expect(fresh).not.toBe(token);const denied=await page.request.get(base+"/Users",{headers:scim});expect(denied.status()).toBe(401);expect((await denied.json()).status).toBe("401");const read=await page.request.get(base+"/Users",{headers:{Authorization:"Bearer "+fresh}});expect(read.status()).toBe(200);expect((await read.json()).Resources).toEqual(before.Resources);
   } else {
    const current=before.Resources[0];const conflict=await page.request.post(base+"/Users",{headers:scim,data:{schemas:current.schemas,externalId:current.externalId+"-conflict",userName:current.userName,displayName:"Should not overwrite",active:true}});expect(conflict.status()).toBe(409);expect(await conflict.json()).toMatchObject({status:"409",scimType:"uniqueness"});
    const invalid=await page.request.patch(base+"/Users/"+current.id,{headers:scim,data:{Operations:[{op:"replace",path:"unsupportedField",value:"invalid"}]}});expect(invalid.status()).toBe(400);const unchanged=await page.request.get(base+"/Users",{headers:scim});expect((await unchanged.json()).Resources).toEqual(before.Resources);
    const restored=await page.request.patch(base+"/Users/"+current.id,{headers:scim,data:{schemas:["urn:ietf:params:scim:api:messages:2.0:PatchOp"],Operations:[{op:"replace",path:"displayName",value:"Recovered directory user"}]}});expect(restored.status()).toBe(200);expect((await restored.json()).displayName).toBe("Recovered directory user");await page.reload();await page.getByRole("tab",{name:"SCIM",exact:true}).click();await expect(page.getByRole("switch",{name:"停用 "+name,exact:true})).toBeChecked();
   }
  } finally {const cleanup=await page.request.post(c+"/identity/cleanup?organization_id="+identity.organization_id);expect(cleanup.status(),await cleanup.text()).toBe(200);expect((await page.request.get(route,{headers})).status()).toBe(404);}
 });
}
/** 注册独立文档有效/无效路由；真实匿名浏览器分别核对 HTTP200 正文或 HTTP404，无业务写入，上下文由运行器清理。 */
export function registerDocsBoundary(valid:boolean,title:string) {
 test(title,async({page})=>{
  const response=await page.goto(valid?"/docs/product/quickstart":"/docs/product/no-such-e2e-document");expect(response?.status()).toBe(valid?200:404);
  if(valid){await expect(page.getByRole("heading",{name:"快速开始",exact:true})).toBeVisible();await expect(page.getByRole("main")).toContainText("curl");}else {await expect(page.getByRole("heading",{name:"404",exact:true})).toBeVisible();await expect(page.getByRole("heading",{name:"快速开始",exact:true})).toHaveCount(0);}
 });
}
