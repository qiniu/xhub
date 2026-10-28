import { expect, test } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t, watchGateway } from "./helpers";
/** 用途：注册独立场景「official Ark credential saves edits and calls its native task model」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC063() {


test("official Ark credential saves edits and calls its native task model", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + await sessionBearer(page) };
  const credential = "e2e-official-ark-protocol";
  const name = "e2e-official-ark";
  const model = "doubao-seedance-2-0-260128";
  const createdCredential = await page.request.post(GATEWAY + "/credentials", { headers, data: {
    credential_name: credential, credential_info: { custom_llm_provider: "volcengine", catalog_id: "volcengine" },
    credential_values: { custom_llm_provider: "volcengine", api_key: "sk-fake", api_base: UPSTREAM },
  } });
  expect(createdCredential.status(), await createdCredential.text()).toBe(200);
  await stableGoto(page, "/models-and-endpoints");
  await page.getByRole("button", { name: t("pages.models.add") }).click();
  const form = page.locator("form").filter({ has: page.getByLabel("模型提供商 *") });
  await form.getByLabel("模型提供商 *").selectOption(credential);
  const upstream = form.getByRole("combobox", { name: "上游模型 *" });
  await upstream.fill(model); await upstream.press("Escape");
  const endpoint = form.getByRole("combobox", { name: "上游接口协议", exact: true });
  await expect(endpoint).toContainText("Ark Video");
  await form.getByLabel("对外模型名称 *").fill(name);
  await form.getByLabel("价格来源").selectOption("manual");
  await form.locator("#editor-output_cost_per_token").fill("10");
  const saving = page.waitForResponse(r => new URL(r.url()).pathname === "/model/new" && r.request().method() === "POST");
  await form.getByRole("button", { name: "添加模型", exact: true }).click();
  const saved = await saving;
  expect(saved.status(), await saved.text()).toBe(200);
  expect(saved.request().postDataJSON()).toMatchObject({
    litellm_params: { custom_llm_provider: "volcengine", litellm_credential_name: credential, model },
    model_info: { transport: "ark_contents_generation", endpoint_types: ["bypass:ark-video"] },
  });
  const id = (await saved.json()).model_info.id;
  const missingProtocol = credential + "-missing-protocol";
  expect((await page.request.post(GATEWAY + "/credentials", { headers, data: {
    credential_name: missingProtocol, credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
  } })).status()).toBe(200);
  try {
    const invalid = await page.request.patch(GATEWAY + "/model/" + id + "/update", { headers, data: {
      litellm_params: { litellm_credential_name: missingProtocol },
    } });
    expect(invalid.status(), "未声明协议的凭据不能借助历史默认行为调用模型").toBe(400);
    expect((await invalid.json()).error.message).toContain("protocol");
  } finally {
    expect((await page.request.delete(GATEWAY + "/credentials/" + missingProtocol, { headers })).status()).toBe(200);
  }
  await page.reload();
  await stableGoto(page, "/models-and-endpoints");
  await page.getByRole("tab", { name: t("pages.models.all") }).click();
  await page.getByRole("region", { name: "公开模型 " + name, exact: true }).getByRole("button", { name: "详情", exact: true }).click();
  await page.getByRole("button", { name: "编辑模型", exact: true }).click();
  await expect(upstream).toHaveValue(model);
  await expect(endpoint).toContainText("Ark Video");
  await expect(form.getByLabel("模型提供商 *")).toHaveValue(credential);
  await form.getByLabel("对外模型名称 *").fill(name + "-edited");
  const updating = page.waitForResponse(r => r.request().method() === "PATCH" && new URL(r.url()).pathname === "/model/" + id + "/update");
  await form.getByRole("button", { name: "保存修改", exact: true }).click();
  const edited = await updating;
  expect(edited.status(), await edited.text()).toBe(200);
  await page.reload();
  await page.getByRole("button", { name: "编辑模型", exact: true }).click();
  await expect(form.getByLabel("对外模型名称 *")).toHaveValue(name + "-edited");
  await expect(endpoint).toContainText("Ark Video");

  // 真实网关转发官方任务路径，本地上游检查 Bearer 鉴权与模型名。
  const queued=await page.request.post(GATEWAY+"/api/v3/contents/generations/tasks",{headers,data:{model:name+"-edited",content:[{type:"text",text:"official-ark-e2e"}]}});
  expect(queued.status(),await queued.text()).toBe(200);
  const task=await queued.json();expect(task.id).toMatch(/^ark-e2e-/);
  for(let i=0;i<2;i++){
   const r=await page.request.get(GATEWAY+"/api/v3/contents/generations/tasks/"+task.id,{headers});
   expect(r.status(),await r.text()).toBe(200);expect(await r.json()).toMatchObject({status:"succeeded",usage:{completion_tokens:100}});
   expect(Number(r.headers()["x-litellm-response-cost"])).toBeCloseTo(0.001,10);
  }
  await form.getByRole("button", { name: "取消", exact: true }).click();
  await stableGoto(page, "/playground");
  await expect(page.getByRole("heading", { name: "模型与端点" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: t("Virtual Key Source") })).not.toBeVisible();
  await page.getByText("连接设置", { exact: true }).click();
  await expect(page.getByRole("combobox", { name: t("Virtual Key Source") })).toBeVisible();
  await page.getByText("连接设置", { exact: true }).click();
  await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
  await page.getByRole("option", { name: name + "-edited", exact: true }).click();
  await expect(page.getByText("提交请求后，结果显示在这里", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "获取结果", exact: true })).toBeDisabled();
  await page.getByLabel("原生请求参数").fill("[]");
  await page.getByRole("button", { name: "提交请求", exact: true }).click();
  await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("请求参数必须是 JSON 对象");
  await page.getByLabel("原生请求参数").fill(JSON.stringify({ content: [{type:"text",text:"official-ark-e2e"}] }, null, 2));
  const creatingTask = page.waitForResponse(r => new URL(r.url()).pathname === "/api/v3/contents/generations/tasks" && r.request().method() === "POST");
  await page.getByRole("button", { name: "提交请求", exact: true }).click();
  const uiTask = await creatingTask;
  expect(uiTask.status(), await uiTask.text()).toBe(200);
  await expect(page.getByLabel("任务 ID")).toHaveValue((await uiTask.json()).id);
  await page.getByRole("button", { name: "获取结果", exact: true }).click();
  await expect(page.getByLabel("原生响应")).toContainText("succeeded");
  await page.getByRole("button", { name: "获取结果", exact: true }).click();
  await expect(page.getByLabel("原生响应")).toContainText("ark-e2e.mp4");
  await page.screenshot({ path: "../.e2e/playground-refactor/desktop.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  // 等待侧栏折叠动画完成，并验证编辑区的实际可用宽度；仅检查无溢出会漏掉被导航挤窄的页面。
  await expect.poll(async () => (await page.getByLabel("原生请求参数").boundingBox())?.width ?? 0,
    { message: "手机请求编辑区应保留足够宽度，避免 JSON 按单个字符换行" }).toBeGreaterThanOrEqual(220);
  await expect(page.getByLabel("原生请求参数")).toBeVisible();
  await expect(page.getByRole("button", { name: "获取结果", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await page.screenshot({ path: "../.e2e/playground-refactor/mobile.png", fullPage: true });
  await page.getByRole("button", { name: "获取结果", exact: true }).click();
  await expect(page.getByLabel("原生响应")).toContainText("ark-e2e.mp4");
  await page.getByLabel("原生响应").scrollIntoViewIfNeeded();
  await page.screenshot({ path: "../.e2e/playground-refactor/mobile-result.png", fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await stableGoto(page, "/models-and-endpoints");
  await page.getByRole("tab", { name: t("pages.models.all") }).click();
  await page.getByRole("region", { name: "公开模型 " + name + "-edited", exact: true }).getByRole("button", { name: "详情", exact: true }).click();
  await page.getByRole("button", { name: "删除", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "删除模型" });
  await dialog.getByPlaceholder(name + "-edited", { exact: true }).fill(name + "-edited");
  // 删除提交是异步请求；先等待后台确认，避免立即导航取消尚未完成的写操作。
  const deleting = page.waitForResponse(r =>
    new URL(r.url()).pathname === "/model/delete" && r.request().method() === "POST",
  );
  await dialog.getByRole("button", { name: /删除|确认/ }).click();
  const deleted = await deleting;
  expect(deleted.status(), await deleted.text()).toBe(200);
  await stableGoto(page, "/models-and-endpoints");
  await page.getByRole("tab", { name: t("pages.models.all") }).click();
  await expect(page.getByRole("region", { name: "公开模型 " + name + "-edited", exact: true })).toHaveCount(0);
  await guard.assertOk();
});
}
