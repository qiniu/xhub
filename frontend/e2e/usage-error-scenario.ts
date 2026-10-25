import {expect,test} from "@playwright/test";
import {readFileSync} from "node:fs";
import {join} from "node:path";
import {translate} from "../src/i18n/translate";
import {GATEWAY,UPSTREAM,MASTER,login,loginAdmin,sessionBearer,stableGoto} from "./helpers";
import {isSuccessfulGatewayGet} from "./gateway-response";

/** 注册用量失败恢复闭环；locale 为界面语言，前置真实浏览器、网关和隔离数据库。
 * 真实推理后删除旧库列制造 HTTP 500，验证失败非零值、迁移后重试、聚合回退及实际跨日分页不完整；finally 恢复故障并删除资源，运行器删 schema。 */
export function registerUsageErrorScenario(locale:"en"|"zh-CN"){
 test("usage errors fallback partial pages and retry "+locale,async({page},info)=>{
  test.setTimeout(150000);
  const control=JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!,"enterprise-fixture.json"),"utf8")).control;
  await loginAdmin(page);
  const headers={Authorization:"Bearer "+await sessionBearer(page)};
  const name="usage-state-"+locale+"-"+Date.now(),email=name+"@example.test";
  let userID="",keyID="",modelID="",legacy=false;
  /** 请求真实管理接口；参数为路径及对象，返回成功 JSON；自建资源在 finally 清理。 */
  const create=async(path:string,body:unknown)=>{const r=await page.request.post(GATEWAY+path,{headers,data:body});expect(r.status(),path+await r.text()).toBe(200);return r.json()};
  /** 设置隔离测试服务故障；mode 为允许的 HTTP 边界模式，无返回值，不替换成功接口数据。 */
  const fault=async(mode:string)=>expect((await page.request.post(control+"/usage/failure",{data:{mode}})).status()).toBe(200);
  try{
   modelID=(await create("/model/new",{model_name:name,litellm_params:{model:name,custom_llm_provider:"openai",api_base:UPSTREAM,api_key:"sk-fake",input_cost_per_token:0.01,output_cost_per_token:0.01},model_info:{transport:"bypass_openai_chat",pricing_source:"manual"}})).model_info.id;
   userID=(await create("/user/new",{user_email:email,password:"usage-state-test-password"})).user_id;
   const key=await create("/key/generate",{user_id:userID,key_alias:name});keyID=key.token_id;
   const call=await page.request.post(GATEWAY+"/v1/chat/completions",{headers:{Authorization:"Bearer "+key.key},data:{model:name,messages:[{role:"user",content:"Verify usage loading and recovery."}]}});
   expect(call.status(),await call.text()).toBe(200);
   await login(page,email,"usage-state-test-password","zh-CN");
   await expect(page.getByText(translate("zh-CN","nav.apiKeys"),{exact:true}).first()).toBeVisible();
   await page.getByRole("button",{name:locale==="en"?"English":translate("en","language.zhCN"),exact:true}).click();
   const tr=(key:string)=>translate(locale,key);
   await stableGoto(page,"/usage");await expect(page.getByTestId("daily-spend-settled")).toBeVisible();
   expect((await page.request.post(control+"/usage/legacy")).status()).toBe(200);legacy=true;
   const failed=page.waitForResponse(r=>r.url().includes("/user/daily/activity")&&!r.url().includes("aggregated")&&r.status()===500);
   await page.reload();await failed;
   await expect(page.getByRole("alert").filter({hasText:tr("usageLoad.failed")})).toBeVisible();
   await expect(page.getByTestId("daily-spend-settled")).toHaveCount(0);
   await expect(page.getByRole("button",{name:tr("pages.usage.exportData"),exact:true})).toHaveCount(0);
   expect((await page.request.post(control+"/usage/migrate")).status()).toBe(200);legacy=false;
   await page.getByRole("button",{name:tr("usageLoad.retry"),exact:true}).click();
   await expect(page.getByTestId("daily-spend-settled")).toBeVisible();
   await expect(page.getByText(tr("usageLoad.failed"),{exact:true})).toHaveCount(0);
   await fault("aggregated");await page.reload();await expect(page.getByTestId("daily-spend-settled")).toBeVisible();
   await expect(page.getByText(tr("usageLoad.failed"),{exact:true})).toHaveCount(0);
   expect((await page.request.post(control+"/usage/seed-pages",{data:{user_id:userID}})).status()).toBe(200);
   await page.getByRole("tab",{name:tr("pages.usage.keyActivity"),exact:true}).click();
   await fault("page2");
   await page.getByRole("button",{name:tr("Select Time Range"),exact:true}).click();
   const start=new Date(Date.now()-60*86400000).toISOString().slice(0,10);
   await page.getByLabel(tr("Start date"),{exact:true}).fill(start);
   await page.getByRole("button",{name:tr("Apply"),exact:true}).click();
   await expect(page.getByRole("alert").filter({hasText:translate(locale,"usageLoad.partial",{loaded:1,total:2})})).toBeVisible();
   await expect(page.getByRole("tab",{name:tr("pages.usage.keyActivity"),exact:true})).toHaveAttribute("aria-selected","true");
   await expect(page.getByRole("button",{name:tr("pages.usage.exportData"),exact:true})).toBeDisabled();
   await fault("");await page.getByRole("button",{name:tr("usageLoad.retry"),exact:true}).click();
   await expect(page.getByRole("alert").filter({hasText:translate(locale,"usageLoad.partial",{loaded:1,total:2})})).toHaveCount(0);
   await expect(page.getByRole("button",{name:tr("pages.usage.exportData"),exact:true})).toBeEnabled();
   await expect(page.getByRole("tab",{name:tr("pages.usage.keyActivity"),exact:true})).toHaveAttribute("aria-selected","true");
   await page.getByRole("tab",{name:tr("pages.usage.cost"),exact:true}).click();
   await expect(page.getByTestId("daily-spend-settled")).toBeVisible();
   // 成功空范围才允许展示空统计，不沿用刚才的 52 日数据。
   await page.getByRole("button",{name:tr("Select Time Range"),exact:true}).click();
   await page.getByLabel(tr("Start date"),{exact:true}).fill("2020-01-01");await page.getByLabel(tr("End date"),{exact:true}).fill("2020-01-02");
   await page.getByRole("button",{name:tr("Apply"),exact:true}).click();
   await expect(page.getByTestId("daily-spend-settled")).toBeVisible();
   await expect(page.getByText(tr("usageLoad.failed"),{exact:true})).toHaveCount(0);
   // 管理员实体视图同样不能将失败显示为零，筛选入口应保持可用以便修复范围。
   await login(page,"admin",MASTER,locale);
   await expect(page.getByText(tr("nav.apiKeys"),{exact:true}).first()).toBeVisible();
   await stableGoto(page,"/usage");
   await page.getByRole("tab",{name:tr("pages.usage.team"),exact:true}).click();
   await page.getByRole("tab",{name:tr("Key Activity"),exact:true}).click();
   expect((await page.request.post(control+"/usage/legacy")).status()).toBe(200);legacy=true;
   await page.getByRole("button",{name:tr("Select Time Range"),exact:true}).click();
   await page.getByLabel(tr("Start date"),{exact:true}).fill("2025-01-01");
   await page.getByRole("button",{name:tr("Apply"),exact:true}).click();
   await expect(page.getByText(tr("usageLoad.failed"),{exact:true})).toBeVisible();
   await expect(page.getByPlaceholder(tr("Search teams by alias..."),{exact:true})).toBeVisible();
   await expect(page.getByRole("button",{name:tr("Export Data"),exact:true})).toBeDisabled();
   expect((await page.request.post(control+"/usage/migrate")).status()).toBe(200);legacy=false;
   // 发布模式经 /gw 代理，本地可直连；两种路径均观察真实 GET，不拦截或伪造响应。
   // 先注册再点击，并在局部 15 秒内失败，避免耗尽整条测试预算后由清理错误掩盖原因。
   const [teamRecovered]=await Promise.all([
    page.waitForResponse(r=>isSuccessfulGatewayGet({url:r.url(),method:r.request().method(),status:r.status()},"/team/daily/activity/aggregated"),{timeout:15000}),
    page.getByRole("button",{name:tr("usageLoad.retry"),exact:true}).click(),
   ]);
   const teamBody=await teamRecovered.json();
   expect(teamBody.metadata.total_api_requests).toBeGreaterThan(0);
   expect(Array.isArray(teamBody.results)).toBe(true);
   await expect(page.getByRole("button",{name:tr("Export Data"),exact:true})).toBeEnabled();
   await expect(page.getByRole("tab",{name:tr("Key Activity"),exact:true})).toHaveAttribute("aria-selected","true");
   const recoveryEvidence={realDatabaseFailure:500,fallback:true,partialPages:[1,2],recovered:true,emptyRange:true,teamRecoveryPath:new URL(teamRecovered.url()).pathname};
   await info.attach("usage-recovery",{body:JSON.stringify(recoveryEvidence),contentType:"application/json"});
  }finally{
   test.setTimeout(test.info().timeout+30000);
   await fault("");if(legacy)expect((await page.request.post(control+"/usage/migrate")).status()).toBe(200);
   if(keyID)expect((await page.request.post(GATEWAY+"/key/delete",{headers,data:{keys:[keyID]}})).status()).toBe(200);
   if(userID)expect((await page.request.post(GATEWAY+"/user/delete",{headers,data:{user_id:userID}})).status()).toBe(200);
   if(modelID)expect((await page.request.post(GATEWAY+"/model/delete",{headers,data:{id:modelID}})).status()).toBe(200);
  }
 });
}
