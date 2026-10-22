import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, MASTER, login, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册组织管理闭环；locale 为语言，无返回值。前置真实后台、隔离库与本地上游。
 * 验证周期创建/刷新/修改/取消、组合搜索、管理员口径及角色只读、到期准入恢复；finally 删除模型/Key/用户/团队/组织，运行器删 schema。 */
export function registerOrganizationManagementScenario(locale: "en" | "zh-CN") {
 test("organization budgets filters and roles " + locale, async ({page},info) => {
  test.setTimeout(150000);
  const control=JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!,"enterprise-fixture.json"),"utf8")).control;
  await loginAdmin(page);
  await page.getByRole("button",{name:locale==="en"?"English":translate("en","language.zhCN"),exact:true}).click();
  const tr=(key:string)=>translate(locale,key);
  const headers={Authorization:"Bearer "+await sessionBearer(page)};
  const name="budget-"+locale+"-"+Date.now(),email=name+"@example.test";
  let orgID="",teamID="",userID="",keyID="",modelID="";
  /** 调用本测试的真实管理接口；path/body 为请求，返回成功 JSON；资源仅属于当前场景并在 finally 清理。 */
  const create=async(path:string,body:unknown)=>{const r=await page.request.post(GATEWAY+path,{headers,data:body});expect(r.status(),path+": "+await r.text()).toBe(200);return r.json()};
  try {
   modelID=(await create("/model/new",{model_name:name,litellm_params:{model:name,custom_llm_provider:"openai",api_base:UPSTREAM,api_key:"sk-fake",input_cost_per_token:0.01,output_cost_per_token:0.01},model_info:{transport:"bypass_openai_chat",pricing_source:"manual",max_input_tokens:64,max_output_tokens:8,supported_parameters:["max_completion_tokens"]}})).model_info.id;
   await stableGoto(page,"/organizations");
   await page.getByRole("button",{name:"+ "+tr("pages.organizations.create"),exact:true}).click();
   const dialog=page.getByRole("dialog");
   await dialog.getByLabel(tr("Organization Name"),{exact:true}).fill(name);
   await dialog.getByLabel(tr("Max Budget (USD)"),{exact:true}).fill("10");
   await dialog.getByRole("combobox",{name:tr("Reset Budget"),exact:true}).click();
   await page.getByRole("option",{name:tr("organizationBudget.every30d"),exact:true}).click();
   const created=page.waitForResponse(r=>r.url().endsWith("/organization/new")&&r.request().method()==="POST");
   await dialog.getByRole("button",{name:tr("pages.organizations.createTitle"),exact:true}).click();
   const response=await created;expect(response.status(),await response.text()).toBe(200);
   const org=await response.json();orgID=org.organization_id;expect(org.budget_duration).toBe("30d");expect(org.budget_reset_at).toBeTruthy();
   const search=page.getByPlaceholder(tr("Search by Organization Name"),{exact:true});
   await search.fill("not-present-"+name);await expect(page.getByText(orgID,{exact:true})).toHaveCount(0);
   await search.fill(name.toUpperCase());await expect(page.getByText(orgID,{exact:true})).toBeVisible();
   await page.getByRole("button",{name:tr("Filters"),exact:true}).click();
   const idSearch=page.getByPlaceholder(tr("Search by Organization ID"),{exact:true});
   await idSearch.fill("foreign");await expect(page.getByText(orgID,{exact:true})).toHaveCount(0);
   await idSearch.fill(orgID);await expect(page.getByText(orgID,{exact:true})).toBeVisible();
   await page.getByRole("button",{name:tr("Reset Filters"),exact:true}).click();
   await stableGoto(page,"/organizations?org="+orgID);
   await page.getByRole("tab",{name:tr("Settings"),exact:true}).click();
   await expect(page.getByText(tr("Reset: {value0}").replace("{value0}",tr("organizationBudget.every30d")),{exact:true})).toBeVisible();
   await page.getByRole("button",{name:tr("Edit Settings"),exact:true}).click();
   await page.getByRole("combobox",{name:tr("Reset Budget"),exact:true}).click();
   await page.getByRole("option",{name:tr("organizationBudget.every7d"),exact:true}).click();
   const saved=page.waitForResponse(r=>r.url().endsWith("/organization/update")&&r.request().method()==="PATCH");
   await page.getByRole("button",{name:tr("Save Changes"),exact:true}).click();expect((await saved).status()).toBe(200);
   await page.reload();await page.getByRole("tab",{name:tr("Settings"),exact:true}).click();
   await expect(page.getByText(tr("Reset: {value0}").replace("{value0}",tr("organizationBudget.every7d")),{exact:true})).toBeVisible();
   teamID=(await create("/team/new",{organization_id:orgID,team_alias:name})).team_id;
   userID=(await create("/user/new",{user_email:email,password:"organization-test-password",team_id:teamID})).user_id;
   await create("/organization/member_add",{organization_id:orgID,member:{user_email:email,role:"org_admin"}});
   const credential=await create("/key/generate",{user_id:userID,key_alias:name});keyID=credential.token_id;
   let requestSequence=0;
   /** 每次提交不同的英文请求以验证真实准入；本地模型整窗输入 64、输出上限 8，返回原始 HTTP 响应，测试资源由 finally 清理。 */
   const call=()=>page.request.post(GATEWAY+"/v1/chat/completions",{headers:{Authorization:"Bearer "+credential.key},data:{model:name,max_completion_tokens:8,messages:[{role:"user",content:"Verify the organization renewal budget. Request "+(++requestSequence)}]}});
   const first=await call();expect(first.status(),await first.text()).toBe(200);
   expect(first.headers()["x-xhub-budget-guarantee"]).toBe("soft");
   let spend=0;
   await expect.poll(async()=>{const o=await (await page.request.get(GATEWAY+"/organization/info?organization_id="+orgID,{headers})).json();spend=o.spend;return spend}).toBeGreaterThan(0);
   // 实际费用0.10；将额度降至已消费后余额为零，新周期重置后恢复。
   expect(spend).toBeCloseTo(0.1,8);
   expect((await page.request.patch(GATEWAY+"/organization/update",{headers,data:{organization_id:orgID,max_budget:spend}})).status()).toBe(200);
   expect((await call()).status()).toBe(403);
   expect((await page.request.post(control+"/organization/budget/due",{data:{organization_id:orgID,pending_previous:true}})).status()).toBe(200);
   expect((await call()).status()).toBe(200);
   const reset=await (await page.request.get(GATEWAY+"/organization/info?organization_id="+orgID,{headers})).json();
   expect(reset.budget_duration).toBe("7d");expect(reset.administrator_count).toBe(1);
   await stableGoto(page,"/organizations");
   await expect(page.getByRole("row").filter({hasText:orgID}).getByText(translate(locale,"organizationBudget.administratorCount",{count:1}),{exact:true})).toBeVisible();
   await login(page,email,"organization-test-password",locale);
   await expect(page.getByText(translate(locale,"nav.apiKeys"),{exact:true}).first()).toBeVisible();
   await stableGoto(page,"/organizations?org="+orgID);
   await page.getByRole("tab",{name:tr("Organization administrators"),exact:true}).click();
   await expect(page.getByTestId("edit-member")).toHaveCount(0);
   await page.getByRole("tab",{name:tr("Settings"),exact:true}).click();await page.getByRole("button",{name:tr("Edit Settings"),exact:true}).click();
   for(const label of ["Max Budget (USD)","Reset Budget","Tokens per minute Limit (TPM)","Requests per minute Limit (RPM)"]) await expect(page.getByLabel(tr(label),{exact:true})).toBeDisabled();
   await page.getByLabel(tr("Organization Name"),{exact:true}).fill(name+"-renamed");
   const renamed=page.waitForResponse(r=>r.url().endsWith("/organization/update")&&r.request().method()==="PATCH");
   await page.getByRole("button",{name:tr("Save Changes"),exact:true}).click();expect((await renamed).status()).toBe(200);
   await login(page,"admin",MASTER,locale);
   await expect(page.getByText(tr("nav.apiKeys"),{exact:true}).first()).toBeVisible();
   await stableGoto(page,"/organizations?org="+orgID);
   await page.getByRole("tab",{name:tr("Settings"),exact:true}).click();await page.getByRole("button",{name:tr("Edit Settings"),exact:true}).click();
   await page.getByRole("combobox",{name:tr("Reset Budget"),exact:true}).click();await page.getByRole("option",{name:tr("No reset"),exact:true}).click();
   const cancellation=page.waitForResponse(r=>r.url().endsWith("/organization/update")&&r.request().method()==="PATCH");
   await page.getByRole("button",{name:tr("Save Changes"),exact:true}).click();
   const cancelled=await cancellation;expect(cancelled.status()).toBe(200);expect((await cancelled.json()).budget_reset_at).toBeNull();
   await page.reload();await page.getByRole("tab",{name:tr("Settings"),exact:true}).click();
   await expect(page.getByText(translate(locale,"Reset: {value0}",{value0:tr("No reset")}),{exact:true})).toBeVisible();
   await page.getByRole("tab",{name:tr("Organization administrators"),exact:true}).click();
   const removed=page.waitForResponse(r=>r.url().endsWith("/organization/member_delete"));
   await page.getByTestId("delete-member").click();expect((await removed).status()).toBe(200);
   await expect(page.getByText(tr("No organization administrators"),{exact:true})).toBeVisible();
   await info.attach("organization-budget",{body:JSON.stringify({orgID,duration:"7d",initialSpend:spend,budgetPolicy:"soft",renewed:true,readonly:true}),contentType:"application/json"});
  } finally {
   test.setTimeout(test.info().timeout+30000);
   if(keyID)expect((await page.request.post(GATEWAY+"/key/delete",{headers,data:{keys:[keyID]}})).status()).toBe(200);
   if(userID)expect((await page.request.post(GATEWAY+"/user/delete",{headers,data:{user_id:userID}})).status()).toBe(200);
   if(teamID)expect((await page.request.post(GATEWAY+"/team/delete",{headers,data:{team_id:teamID}})).status()).toBe(200);
   if(orgID)expect((await page.request.delete(GATEWAY+"/organization/delete",{headers,data:{organization_id:orgID}})).status()).toBe(200);
   if(modelID)expect((await page.request.post(GATEWAY+"/model/delete",{headers,data:{id:modelID}})).status()).toBe(200);
  }
 });
}
