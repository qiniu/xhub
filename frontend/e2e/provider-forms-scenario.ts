import { expect, test, type Page } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t, watchGateway } from "./helpers";


/** 选择后台提供的供应商；page 为已打开弹窗浏览器，label 为显示名，返回完成 Promise；仅变更草稿。 */
async function choose(page: Page, label: string) {
  const input=page.getByRole("combobox",{name:t("Provider type")});
  await input.click();
  await input.fill("");
  await input.pressSequentially(label);
  await page.getByRole("option").filter({has:page.getByText(label,{exact:true})}).click();
  // 下拉会保留隐藏节点供下一次搜索复用；等待其不可见，防止后续选择沿用关闭动画中的状态。
  await expect(page.getByRole("listbox")).toBeHidden();
}

/** 打开模型提供商弹窗；page 已登录，返回弹窗定位器；供创建流程调用，不写数据库。 */
async function openAdd(page:Page) {
  await stableGoto(page,"/model-providers");
  await page.getByRole("button",{name:"添加提供商",exact:true}).click();
  return page.getByRole("dialog",{name:t("Add model provider")});
}

/** 从列表删除 name 凭据；page 已登录，返回完成 Promise；UI 确认后验证行消失，供用例清理。 */
async function deleteProvider(page:Page,name:string) {
  await stableGoto(page,"/model-providers");
  await page.getByTestId("credential-actions-"+name).click();
  await page.getByTestId("credential-action-delete").click();
  const dialog=page.getByRole("dialog");
  await dialog.getByPlaceholder(name,{exact:true}).fill(name);
  await dialog.getByRole("button",{name:t("Delete"),exact:true}).click();
  await expect(page.getByTestId("credential-actions-"+name)).toHaveCount(0);
}
/** 用途：注册独立场景「provider editor uses current API protocol without repairing display-name identities」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC116() {


test("provider editor uses current API protocol without repairing display-name identities", async({page})=>{
  const guard=watchGateway(page);await loginAdmin(page);
  const headers={Authorization:"Bearer "+await sessionBearer(page)};
  const names=["e2e-current-api-provider","e2e-invalid-display-provider"];
  try {
    for (const [index,name] of names.entries()) {
      const created=await page.request.post(GATEWAY+"/credentials",{headers,data:{credential_name:name,
        credential_info:{custom_llm_provider:index===0?"openai":"OpenAI-Compatible Endpoints (Together AI, etc.)"},
        credential_values:{api_key:"sk-fake",api_base:UPSTREAM+"/v1"}}});
      expect(created.status(),await created.text()).toBe(200);
    }
    await stableGoto(page,"/model-providers");
    await page.getByTestId("credential-actions-"+names[0]).click();await page.getByTestId("credential-action-edit").click();
    const edit=page.getByRole("dialog",{name:t("Edit model provider")});
    // 列表接口返回脱敏凭据，编辑回填不能要求后台暴露明文。
    await expect(edit.getByLabel("OpenAI API Key")).toHaveValue("sk***ke");
    await expect(edit.getByRole("button",{name:t("Save model provider")})).toBeEnabled();
    await edit.getByRole("button",{name:t("Cancel"),exact:true}).click();
    await page.getByTestId("credential-actions-"+names[1]).click();await page.getByTestId("credential-action-edit").click();
    await expect(edit.getByText(t("Select a provider to see its configuration fields."))).toBeVisible();
    await expect(edit.getByLabel("OpenAI API Key")).toHaveCount(0);
    await expect(edit.getByRole("button",{name:t("Save model provider")})).toBeDisabled();
    await edit.getByRole("button",{name:t("Cancel"),exact:true}).click();await guard.assertOk();
  } finally {
    for (const name of names) {
      const deleted=await page.request.delete(GATEWAY+"/credentials/"+name,{headers});
      expect(deleted.status(),await deleted.text()).toBe(200);
    }
  }
});
}
/** 用途：注册独立场景「provider forms switch and persist DeepSeek credentials」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC117() {


test("provider forms switch and persist DeepSeek credentials", async({page})=>{
  const guard=watchGateway(page);await loginAdmin(page);const dialog=await openAdd(page);
  await expect(dialog.getByText(t("Select a provider to see its configuration fields."))).toBeVisible();
  await expect(dialog.getByLabel("OpenAI API Key")).toHaveCount(0);
  await choose(page,"OpenAI");
  await dialog.getByLabel(t("Provider name")).fill("e2e-form-deepseek");
  await dialog.getByLabel("OpenAI API Key").fill("old-secret");
  await dialog.getByLabel("OpenAI Organization ID").fill("old-organization");
  await choose(page,"Azure");
  await expect(dialog.getByLabel("API Version",{exact:true})).toBeVisible();
  await expect(dialog.getByLabel("Azure AD Token",{exact:true})).toBeVisible();
  await expect(dialog.getByLabel("API Base",{exact:true})).toHaveValue("");
  await expect(dialog.getByLabel("Azure API Key",{exact:true})).toHaveValue("");
  await page.screenshot({path:"../.e2e/provider-forms/azure.png"});
  await choose(page,"Vertex AI (Anthropic, Gemini, etc.)");
  await expect(dialog.getByLabel("Vertex Project",{exact:true})).toBeVisible();
  await expect(dialog.getByLabel("Vertex Location",{exact:true})).toBeVisible();
  await expect(dialog.getByLabel("Vertex Credentials",{exact:true})).toBeAttached();
  await choose(page,"Amazon Bedrock");
  await expect(dialog.getByLabel("AWS Region Name",{exact:true})).toBeVisible();
  await expect(dialog.getByLabel("AWS Secret Access Key",{exact:true})).toBeVisible();
  await choose(page,"Ollama");
  await expect(dialog.getByLabel("API Base",{exact:true})).toHaveValue("http://localhost:11434");
  const providerSearch=page.getByRole("combobox",{name:t("Provider type")});
  await providerSearch.click();await providerSearch.fill("ChatGPT Subscription");
  await expect(page.getByRole("option",{name:"ChatGPT Subscription",exact:true})).toHaveCount(0);
  await choose(page,"DeepSeek");
  await expect(dialog.getByLabel("API Base",{exact:true})).toHaveValue("https://api.deepseek.com");
  await expect(dialog.getByLabel("API Key",{exact:true})).toHaveValue("");
  await dialog.getByLabel("API Key",{exact:true}).fill("sk-fake");
  await page.screenshot({path:"../.e2e/provider-forms/deepseek.png"});
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy();
  await page.screenshot({path:"../.e2e/provider-forms/mobile.png"});
  await page.setViewportSize({width:1440,height:1000});
  const saving=page.waitForResponse(r=>new URL(r.url()).pathname==="/credentials"&&r.request().method()==="POST");
  await dialog.getByRole("button",{name:t("Add model provider"),exact:true}).click();
  const saved=await saving;expect(saved.status(),await saved.text()).toBe(200);
  expect(saved.request().postDataJSON()).toMatchObject({credential_info:{custom_llm_provider:"deepseek",provider_id:"Deepseek"},credential_values:{api_base:"https://api.deepseek.com",api_key:"sk-fake"}});
  expect(saved.request().postDataJSON().credential_values).not.toHaveProperty("organization");
  await page.reload();
  await page.getByTestId("credential-actions-e2e-form-deepseek").click();await page.getByTestId("credential-action-edit").click();
  const edit=page.getByRole("dialog",{name:t("Edit model provider")});
  await expect(edit.getByRole("combobox",{name:t("Provider type")})).toHaveValue("DeepSeek");
  await expect(edit.getByRole("combobox",{name:t("Provider type")})).toBeDisabled();
  await edit.getByLabel("API Base",{exact:true}).fill(UPSTREAM+"/v1");
  const updating=page.waitForResponse(r=>new URL(r.url()).pathname==="/credentials/e2e-form-deepseek"&&r.request().method()==="PATCH");
  await edit.getByRole("button",{name:t("Save model provider")}).click();expect((await updating).status()).toBe(200);
  await deleteProvider(page,"e2e-form-deepseek");await guard.assertOk();
});
}
/** 用途：注册独立场景「OpenAI compatible form saves a provider and invokes its model」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC118() {


test("OpenAI compatible form saves a provider and invokes its model", async({page})=>{
  test.setTimeout(90_000);const guard=watchGateway(page);await loginAdmin(page);const name="e2e-form-compatible";
  const dialog=await openAdd(page);await choose(page,"OpenAI-Compatible Endpoints (Together AI, etc.)");
  await dialog.getByLabel(t("Provider name")).fill(name);await dialog.getByLabel("API Base",{exact:true}).fill(UPSTREAM+"/v1");
  await dialog.getByLabel("OpenAI API Key").fill("sk-fake");
  await dialog.getByRole("button",{name:t("Add model provider"),exact:true}).click();await expect(dialog).toHaveCount(0);
  await page.getByTestId("credential-actions-"+name).click();await page.getByTestId("credential-action-edit").click();
  const edit=page.getByRole("dialog",{name:t("Edit model provider")});
  await expect(edit.getByRole("combobox",{name:t("Provider type")})).toHaveValue("OpenAI-Compatible Endpoints (Together AI, etc.)");
  await edit.getByRole("button",{name:t("Cancel"),exact:true}).click();
  await stableGoto(page,"/models-and-endpoints");
  await page.getByRole("button",{name:t("pages.models.add"),exact:true}).click();
  const form=page.locator("form").filter({has:page.getByLabel("模型提供商 *")});
  await form.getByLabel("模型提供商 *").selectOption(name);
  const upstream=form.getByRole("combobox",{name:"上游模型 *"});await upstream.fill("gpt-4o-mini");
  await page.getByRole("option",{name:"gpt-4o-mini",exact:true}).click();
  await form.getByLabel("对外模型名称 *").fill(name);
  await form.getByRole("combobox", { name: "上游接口协议", exact: true }).click();
  await page.getByRole("option", { name: "OpenAI · Chat Completions", exact: true }).click();
  await form.getByLabel("价格来源").selectOption("manual");
  await form.locator("#editor-input_cost_per_token").fill("0.15");await form.locator("#editor-output_cost_per_token").fill("0.60");
  const creating=page.waitForResponse(r=>new URL(r.url()).pathname==="/model/new"&&r.request().method()==="POST");
  await form.getByRole("button",{name:"添加模型",exact:true}).click();expect((await creating).status()).toBe(200);
  await page.reload();await stableGoto(page,"/models-and-endpoints");await page.getByRole("tab",{name:t("pages.models.all")}).click();
  await page.getByRole("region",{name:"公开模型 "+name,exact:true}).getByRole("button",{name:"详情",exact:true}).click();await page.getByRole("button",{name:"测试连接",exact:true}).click();
  await expect(page.getByRole("status").filter({hasText:"连接正常，模型已响应。"})).toBeVisible();
  const response=await page.request.post(GATEWAY+"/v1/chat/completions",{headers:{Authorization:"Bearer "+await sessionBearer(page)},data:{model:name,messages:[{role:"user",content:"provider-form-e2e"}]}});
  expect(response.status(),await response.text()).toBe(200);expect((await response.json()).choices[0].message.content).toBe("e2e-ok");
  await page.getByRole("button",{name:"删除",exact:true}).click();await page.getByRole("dialog").getByPlaceholder(name).fill(name);
  await page.getByRole("dialog").getByRole("button",{name:"删除",exact:true}).click();
  await expect(page.getByRole("button",{name,exact:true})).toHaveCount(0);await deleteProvider(page,name);await guard.assertOk();
});
}
/** 用途：注册独立场景「Custom provider explicitly selects and invokes a Qiniu compatible transport」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC119() {


test("Custom provider explicitly selects and invokes a Qiniu compatible transport", async({page})=>{
  test.setTimeout(90_000);const guard=watchGateway(page);await loginAdmin(page);
  const credential="e2e-custom-qiniu",name="e2e-custom-qiniu-model";
  const dialog=await openAdd(page);await choose(page,"Custom");
  await dialog.getByLabel(t("Provider name")).fill(credential);
  await dialog.getByLabel("API Base",{exact:true}).fill(UPSTREAM);await dialog.getByLabel("API Key",{exact:true}).fill("sk-fake");
  await dialog.getByRole("button",{name:t("Add model provider"),exact:true}).click();await expect(dialog).toHaveCount(0);
  await stableGoto(page,"/models-and-endpoints");
  await page.getByRole("button",{name:t("pages.models.add"),exact:true}).click();
  const form=page.locator("form").filter({has:page.getByLabel("模型提供商 *")});await form.getByLabel("模型提供商 *").selectOption(credential);
  const upstream=form.getByRole("combobox",{name:"上游模型 *"});
  await upstream.fill("qiniu/bytedance/doubao-seedance-2-0-260128");await upstream.press("Escape");
  const endpoint=form.getByRole("combobox",{name:"上游接口协议",exact:true});await expect(endpoint).toContainText("Seedance");
  await form.getByLabel("对外模型名称 *").fill(name);await form.getByLabel("价格来源").selectOption("manual");
  await form.locator("#editor-output_cost_per_token").fill("10");
  const creating=page.waitForResponse(r=>new URL(r.url()).pathname==="/model/new"&&r.request().method()==="POST");
  await form.getByRole("button",{name:"添加模型",exact:true}).click();const saved=await creating;expect(saved.status(),await saved.text()).toBe(200);
  expect(saved.request().postDataJSON()).toMatchObject({litellm_params:{custom_llm_provider:"custom",litellm_credential_name:credential},model_info:{transport:"qiniu_contents_generation",endpoint_types:["bypass:ark-video"]}});
  await page.reload();await stableGoto(page,"/models-and-endpoints");await page.getByRole("tab",{name:t("pages.models.all")}).click();
  await page.getByRole("region",{name:"公开模型 "+name,exact:true}).getByRole("button",{name:"详情",exact:true}).click();await page.getByRole("button",{name:"编辑模型",exact:true}).click();
  await expect(upstream).toHaveValue("qiniu/bytedance/doubao-seedance-2-0-260128");await expect(endpoint).toContainText("Seedance");
  const headers={Authorization:"Bearer "+await sessionBearer(page)};
  const queued=await page.request.post(GATEWAY+"/v3/contents/generations/tasks",{headers,data:{model:name,content:[{type:"text",text:"custom-qiniu-e2e"}]}});
  expect(queued.status(),await queued.text()).toBe(200);expect((await queued.json()).id).toMatch(/^ark-e2e-/);
  await form.getByRole("button",{name:"取消",exact:true}).click();await page.getByRole("button",{name:"删除",exact:true}).click();
  await page.getByRole("dialog",{name:"删除模型"}).getByPlaceholder(name,{exact:true}).fill(name);
  await page.getByRole("dialog",{name:"删除模型"}).getByRole("button",{name:/删除|确认/}).click();
  await deleteProvider(page,credential);await guard.assertOk();
});
}
