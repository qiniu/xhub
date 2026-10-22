import { expect, test, type Page } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 操作真实筛选抽屉并返回后台结果；参数为页面、语言、独立模型、状态和原始消息。
 * 用于验证界面到SQL的完整链路，不mock请求；只改变当前页面筛选，不修改业务数据。 */
async function applyErrorFilter(page: Page, locale: "en" | "zh-CN", model: string, code: string, message: string) {
  const tr = (key: string) => translate(locale, key);
  await page.getByTestId("datatable-filters-trigger").click();
  const drawer = page.getByRole("dialog");
  await drawer.getByPlaceholder(tr("Enter public model or search tool…")).fill(model);
  const input = drawer.getByPlaceholder(tr("Select or type an error code"));
  await input.fill(code);
  await page.getByRole("option", {name: new RegExp("^" + code + " - ")}).click();
  await drawer.getByPlaceholder(tr("Enter error message…")).fill(message);
  const fetched = page.waitForResponse(r => {
    const u = new URL(r.url());
    return u.pathname === "/spend/logs/ui" && u.searchParams.get("error_code") === code && (u.searchParams.get("error_message") || "") === message && u.searchParams.get("model") === model;
  });
  await drawer.getByTestId("filter-drawer-apply").click();
  const response = await fetched;
  expect(response.status(), "日志筛选后台请求应成功").toBe(200);
  await expect(drawer).toBeHidden();
  return response.json();
}

for (const locale of ["en", "zh-CN"] as const) {
  /** 真实浏览器验证预算403、RPM429、错误筛选和同请求详情；隔离数据库/本地上游，无生产凭据。
   * 同时验证中英文与原始错误文本不被翻译；finally删除自建Key和模型，运行器清理日志schema。 */
  test("error filters match budget and rate responses " + locale, async ({ page }) => {
    await loginAdmin(page);
    const headers = {Authorization: "Bearer " + await sessionBearer(page)};
    await page.getByRole("button", {name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true}).click();
    const tr = (key: string) => translate(locale,key);
    const model = "error-filter-" + locale.toLowerCase() + "-" + Date.now();
    const keys: string[] = [];
    let deployment = "";
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {headers, data: {
        model_name: model, litellm_params: {model, custom_llm_provider:"openai", api_base: UPSTREAM, api_key:"sk-fake", input_cost_per_token:0.000001, output_cost_per_token:0.000002},
        model_info: {transport:"bypass_openai_chat", pricing_source:"manual", endpoint_types:["chat"]},
      }});
      expect(created.status(),await created.text()).toBe(200); deployment=(await created.json()).model_info.id;
      const failures: {code:number;id:string;body:string;message:string}[]=[];
      for (const kind of ["budget","rate"] as const) {
        const minted=await page.request.post(GATEWAY+"/key/generate",{headers,data:{key_alias:model+kind,...(kind==="budget"?{max_budget:0}:{rpm_limit:0})}});
        expect(minted.status(),await minted.text()).toBe(200);
        const key=await minted.json();keys.push(key.token_id);
        const response=await page.request.post(GATEWAY+"/bypass/openai/v1/chat/completions",{headers:{Authorization:"Bearer "+key.key},data:{model,messages:[{role:"user",content:"Verify error filtering."}]}});
        const code=kind==="budget"?403:429;
        expect(response.status(),await response.text()).toBe(code);
        const id=response.headers()["x-litellm-call-id"];
        expect(id).toBeTruthy();
        failures.push({code,id,body:await response.text(),message:kind==="budget"?"budget_exceeded":"rpm_limit"});
        await expect.poll(async()=> (await page.request.get(GATEWAY+"/spend/logs/ui/"+id,{headers})).status()).toBe(200);
      }
      await stableGoto(page,"/logs");
      await page.getByRole("tab",{name:tr("Error Logs"),exact:true}).click();
      for (const failure of failures) {
        const data=await applyErrorFilter(page,locale,model,String(failure.code),failure.message);
        expect(data.total,"筛选总数应只包含对应失败").toBe(1);
        expect(data.data[0].request_id).toBe(failure.id);
        expect(data.data[0].metadata.http_status).toBe(failure.code);
        await expect(page.getByRole("row").filter({hasText:failures.find(f=>f.id!==failure.id)!.id})).toHaveCount(0);
        await page.getByRole("row").filter({hasText:failure.id}).click();
        const detail=page.getByRole("dialog");
        await expect(detail.getByRole("alert")).toContainText(new RegExp("HTTP (状态码|Status):\\s*"+failure.code));
        await expect(detail.getByRole("region",{name:tr("Full Error Details")}).locator("pre")).toHaveText(failure.body);
        await page.keyboard.press("Escape");
        await expect(detail).toBeHidden();
      }
      const empty=await applyErrorFilter(page,locale,model,"403","rpm_limit");
      expect(empty.total).toBe(0);expect(empty.data).toEqual([]);
      await expect(page.getByTestId("pagination-range")).toHaveText(tr("consoleDirectory.pagination.empty"));
      const missing=await applyErrorFilter(page,locale,model,"429","%_' OR true --");
      expect(missing.total).toBe(0);
      await page.getByTestId("datatable-clear-filters").click();
      for (const failure of failures) await expect(page.getByRole("row").filter({hasText:failure.id})).toBeVisible();
    } finally {
      if(keys.length) expect((await page.request.post(GATEWAY+"/key/delete",{headers,data:{keys}})).status()).toBe(200);
      if(deployment) expect((await page.request.post(GATEWAY+"/model/delete",{headers,data:{id:deployment}})).status()).toBe(200);
    }
  });
}
