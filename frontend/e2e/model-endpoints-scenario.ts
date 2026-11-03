import { expect, test, type Page } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t, watchGateway } from "./helpers";


/** supplier 保存本地 OpenAI 兼容凭据；参数 page、name 为页面和名称，返回完成 Promise。
 * 只在隔离 E2E schema 写入，套件结束清理，不推断专用供应商。 */
async function supplier(page: Page, name: string, _native = false) {
  const response = await page.request.post(GATEWAY + "/credentials", {
    headers: { Authorization: "Bearer " + (await sessionBearer(page)) },
    data: {
      credential_name: name,
      credential_info: { custom_llm_provider: "openai" },
      credential_values: { api_key: "sk-fake", api_base: UPSTREAM + "/v1" },
    },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}


/** discover 通过用户选择凭据获取真实网关目录；每个用例使用独立凭据，避免缓存掩盖请求。 */
async function discover(page: Page, credential: string) {
  const form = page.locator("form").filter({ has: page.getByLabel("模型提供商 *") });
  const pending = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/model/builtin/models" &&
      r.request().method() === "POST" &&
      r.request().postDataJSON().credential_name === credential,
    { timeout: 15_000 },
  );
  await form.getByLabel("模型提供商 *").selectOption(credential);
  const response = await pending;
  expect(response.status(), await response.text()).toBe(200);
  return { form, models: (await response.json()).models as { id: string }[] };
}


/** 在分组模型列表定位指定公开模型详情；参数为页面和别名，返回完成 Promise；只读页面，无数据写入。 */
async function openModelDetails(page: Page, name: string) {
  await page.getByLabel("搜索模型").fill(name);
  await page
    .getByRole("region", { name: "公开模型 " + name, exact: true })
    .getByRole("button", { name: "详情", exact: true })
    .click();
}
/** 用途：注册独立场景「unbound model explains configuration and valid model restores a callable endpoint」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC074() {


test("unbound model explains configuration and valid model restores a callable endpoint", async ({ page }) => {
  test.skip(process.env.E2E_ENDPOINT_UNBOUND !== "1", "需要隔离的无端点测试配置");
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  const models = await page.request.get(GATEWAY + "/model/available", { headers });
  expect(models.status()).toBe(200);
  const unbound = (await models.json()).data.find((model: { id: string }) => model.id === "e2e-unbound");
  expect(unbound).toMatchObject({ endpoints: [], unavailable_reason: expect.stringContaining("transport") });
  await stableGoto(page, "/playground");
  await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
  await page.getByRole("option", { name: "e2e-unbound", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "该模型没有可调用的端点" })).toBeVisible();
  await expect(page.getByRole("link", { name: "前往模型配置" })).toHaveAttribute("href", "/models-and-endpoints");
  await expect(page.getByPlaceholder("Describe the image you want to generate...")).toHaveCount(0);
  await expect(page.getByRole("button", { name: t("Send message") })).toHaveCount(0);
  await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
  await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
  await page
    .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
    .filter({ visible: true })
    .fill("hello");
  const called = page.waitForResponse(
    (r) => /^(\/v1)?\/chat\/completions$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: t("Send message") }).click();
  expect((await called).status()).toBe(200);
  await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
});
}
/** 用途：注册独立场景「native gemini saves exact protocol and executes in Playground」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC075() {
const native: {google:boolean;id:string;label:string;root:string;transport:string} = {"id":"gemini","transport":"gemini_generate_content","label":"Gemini · Generate Content","root":"/v1beta/models/","google":true};

test("native gemini saves exact protocol and executes in Playground", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const credential = "e2e-native-" + native.id;
    const name = credential + "-model";
    const upstreamModel = "e2e-native-real-" + native.id;
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    let id = "";
    try {
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("button", { name: t("pages.models.add") }).click();
      const form = page.locator("form").filter({ has: page.getByLabel("模型提供商 *") });
      await form.getByLabel("模型提供商 *").selectOption(credential);
      await expect(form.getByRole("alert").filter({ hasText: "可重试或直接输入模型 ID" })).toBeVisible();
      const upstream = form.getByRole("combobox", { name: "上游模型 *" });
      await upstream.fill(upstreamModel);
      await upstream.press("Escape");
      const endpoint = form.getByRole("combobox", { name: "上游接口协议", exact: true });
      await endpoint.click();
      await expect(page.getByText("OpenAI", { exact: true })).toBeVisible();
      await expect(page.getByText("Bypass 转发（含 Fal）", { exact: true })).toBeVisible();
      await expect(page.getByRole("option", { name: "图像", exact: true })).toHaveCount(0);
      await page.getByRole("option", { name: native.label, exact: true }).click();
      await expect(form.getByRole("combobox", { name: "上游接口协议", exact: true })).toContainText(native.label);
      await form.getByLabel("对外模型名称 *").fill(name);
      await form.getByLabel("价格来源").selectOption("manual");
      await form.locator("#editor-input_cost_per_token").fill("1");
      await form.locator("#editor-output_cost_per_token").fill("2");
      const submitted = page.waitForResponse(
        (r) => new URL(r.url()).pathname === "/model/new" && r.request().method() === "POST",
      );
      await form.getByRole("button", { name: "添加模型", exact: true }).click();
      const saved = await submitted;
      expect(saved.status(), await saved.text()).toBe(200);
      expect(saved.request().postDataJSON().model_info).toMatchObject({
        endpoint_types: native.google ? ["chat", "gemini", "vertex", "responses", "messages"] : [native.id],
        transport: native.transport,
      });
      id = (await saved.json()).model_info.id;
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      await expect(endpoint).toContainText(native.label);
      await expect(upstream).toHaveValue(upstreamModel);
      await form.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      // 图像原生请求在窄屏执行，验证 JSON 编辑器、提交与真实结果不横向溢出。
      if (native.id === "image_generation") await page.setViewportSize({ width: 390, height: 844 });
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      const path = native.google ? native.root + name + ":generateContent" : native.root;
      if (native.google) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await page
          .getByRole("option", { name: new RegExp(native.label.split(" · ")[0] + ".*generateContent$") })
          .click();
      }
      await page.getByText("接口详情", { exact: true }).click();
      await expect(page.getByText("POST " + path, { exact: true })).toBeVisible();
      const body = native.google
        ? { contents: [{ role: "user", parts: [{ text: "hello" }] }] }
        : { prompt: "hello", size: "1024x1024", n: 1 };
      if (!native.google) {
        await expect(page.getByLabel("图片尺寸", { exact: true })).toHaveValue("1024x1024");
        // 创建流程直接使用表单；先验证本地必填校验，再复现渠道不支持尺寸的真实 400 并重试。
        if (native.id === "image_generation") {
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
          await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("图片提示词");
          await page.getByLabel("图片提示词", { exact: true }).fill("hello");
          await page.getByLabel("图片尺寸", { exact: true }).fill("2048x2048");
          const failed = page.waitForResponse(
            (r) => new URL(r.url()).pathname === path && r.request().method() === "POST",
          );
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
          expect((await failed).status()).toBe(400);
          await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("2048x2048");
          await expect(page.getByLabel("图片提示词", { exact: true })).toHaveValue("hello");
          await page.getByLabel("图片尺寸", { exact: true }).fill("1024x1024");
        }
        await page.getByText("JSON 参数", { exact: true }).click();
      }
      await page.getByLabel("原生请求参数").fill(JSON.stringify(body));
      await page.getByRole("button", { name: "格式化", exact: true }).click();
      if (native.id === "image_edit") {
        await page.getByLabel("编辑图片", { exact: true }).setInputFiles({
          name: "edit.png",
          mimeType: "image/png",
          buffer: Buffer.from(
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0S8AAAAASUVORK5CYII=",
            "base64",
          ),
        });
      }
      const inferred = page.waitForResponse(
        (r) => new URL(r.url()).pathname === path && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: "提交请求", exact: true }).click();
      const response = await inferred;
      expect(response.status(), await response.text()).toBe(200);
      if (native.id === "image_generation")
        expect(response.request().postDataJSON()).toMatchObject({ ...body, model: name });
      if (!native.google) {
        await expect(page.getByRole("img", { name: "生成图片 1", exact: true })).toBeVisible();
        await expect
          .poll(() =>
            page
              .getByRole("img", { name: "生成图片 1", exact: true })
              .evaluate((img) => (img as HTMLImageElement).naturalWidth),
          )
          .toBeGreaterThan(0);
        await page.getByText("原生响应", { exact: true }).click();
      }
      await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
      if (native.id === "image_generation") {
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
        await page.getByLabel("原生响应").scrollIntoViewIfNeeded();
        await expect(page.getByLabel("原生响应")).toBeInViewport();
        await page.screenshot({ path: "../.e2e/playground/native-mobile.png", fullPage: true });
      }
      if (native.google) expect(response.request().postDataJSON()).toEqual(body);
      else if (native.id === "image_edit")
        expect(response.request().headers()["content-type"]).toContain("multipart/form-data");
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId, "原生推理必须关联真实账单").toBeTruthy();
      await expect
        .poll(async () => {
          const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
          return detail.ok() ? Number((await detail.json()).spend) : 0;
        })
        .toBeCloseTo(0.000012, 10);
      const disabled = await page.request.patch(GATEWAY + "/model/" + id + "/update", {
        headers,
        data: { model_info: { disabled: true } },
      });
      expect(disabled.status(), await disabled.text()).toBe(200);
      const denied = await page.request.post(GATEWAY + path, { headers, data: { ...body, model: name } });
      expect(denied.status(), "禁用部署必须拒绝实际调用").toBe(400);
      await guard.assertOk();
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「native vertex saves exact protocol and executes in Playground」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC076() {
const native: {google:boolean;id:string;label:string;root:string;transport:string} = {"id":"vertex","transport":"vertex_generate_content","label":"Vertex AI · Generate Content","root":"/vertex/v1/models/","google":true};

test("native vertex saves exact protocol and executes in Playground", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const credential = "e2e-native-" + native.id;
    const name = credential + "-model";
    const upstreamModel = "e2e-native-real-" + native.id;
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    let id = "";
    try {
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("button", { name: t("pages.models.add") }).click();
      const form = page.locator("form").filter({ has: page.getByLabel("模型提供商 *") });
      await form.getByLabel("模型提供商 *").selectOption(credential);
      await expect(form.getByRole("alert").filter({ hasText: "可重试或直接输入模型 ID" })).toBeVisible();
      const upstream = form.getByRole("combobox", { name: "上游模型 *" });
      await upstream.fill(upstreamModel);
      await upstream.press("Escape");
      const endpoint = form.getByRole("combobox", { name: "上游接口协议", exact: true });
      await endpoint.click();
      await expect(page.getByText("OpenAI", { exact: true })).toBeVisible();
      await expect(page.getByText("Bypass 转发（含 Fal）", { exact: true })).toBeVisible();
      await expect(page.getByRole("option", { name: "图像", exact: true })).toHaveCount(0);
      await page.getByRole("option", { name: native.label, exact: true }).click();
      await expect(form.getByRole("combobox", { name: "上游接口协议", exact: true })).toContainText(native.label);
      await form.getByLabel("对外模型名称 *").fill(name);
      await form.getByLabel("价格来源").selectOption("manual");
      await form.locator("#editor-input_cost_per_token").fill("1");
      await form.locator("#editor-output_cost_per_token").fill("2");
      const submitted = page.waitForResponse(
        (r) => new URL(r.url()).pathname === "/model/new" && r.request().method() === "POST",
      );
      await form.getByRole("button", { name: "添加模型", exact: true }).click();
      const saved = await submitted;
      expect(saved.status(), await saved.text()).toBe(200);
      expect(saved.request().postDataJSON().model_info).toMatchObject({
        endpoint_types: native.google ? ["chat", "gemini", "vertex", "responses", "messages"] : [native.id],
        transport: native.transport,
      });
      id = (await saved.json()).model_info.id;
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      await expect(endpoint).toContainText(native.label);
      await expect(upstream).toHaveValue(upstreamModel);
      await form.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      // 图像原生请求在窄屏执行，验证 JSON 编辑器、提交与真实结果不横向溢出。
      if (native.id === "image_generation") await page.setViewportSize({ width: 390, height: 844 });
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      const path = native.google ? native.root + name + ":generateContent" : native.root;
      if (native.google) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await page
          .getByRole("option", { name: new RegExp(native.label.split(" · ")[0] + ".*generateContent$") })
          .click();
      }
      await page.getByText("接口详情", { exact: true }).click();
      await expect(page.getByText("POST " + path, { exact: true })).toBeVisible();
      const body = native.google
        ? { contents: [{ role: "user", parts: [{ text: "hello" }] }] }
        : { prompt: "hello", size: "1024x1024", n: 1 };
      if (!native.google) {
        await expect(page.getByLabel("图片尺寸", { exact: true })).toHaveValue("1024x1024");
        // 创建流程直接使用表单；先验证本地必填校验，再复现渠道不支持尺寸的真实 400 并重试。
        if (native.id === "image_generation") {
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
          await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("图片提示词");
          await page.getByLabel("图片提示词", { exact: true }).fill("hello");
          await page.getByLabel("图片尺寸", { exact: true }).fill("2048x2048");
          const failed = page.waitForResponse(
            (r) => new URL(r.url()).pathname === path && r.request().method() === "POST",
          );
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
          expect((await failed).status()).toBe(400);
          await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("2048x2048");
          await expect(page.getByLabel("图片提示词", { exact: true })).toHaveValue("hello");
          await page.getByLabel("图片尺寸", { exact: true }).fill("1024x1024");
        }
        await page.getByText("JSON 参数", { exact: true }).click();
      }
      await page.getByLabel("原生请求参数").fill(JSON.stringify(body));
      await page.getByRole("button", { name: "格式化", exact: true }).click();
      if (native.id === "image_edit") {
        await page.getByLabel("编辑图片", { exact: true }).setInputFiles({
          name: "edit.png",
          mimeType: "image/png",
          buffer: Buffer.from(
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0S8AAAAASUVORK5CYII=",
            "base64",
          ),
        });
      }
      const inferred = page.waitForResponse(
        (r) => new URL(r.url()).pathname === path && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: "提交请求", exact: true }).click();
      const response = await inferred;
      expect(response.status(), await response.text()).toBe(200);
      if (native.id === "image_generation")
        expect(response.request().postDataJSON()).toMatchObject({ ...body, model: name });
      if (!native.google) {
        await expect(page.getByRole("img", { name: "生成图片 1", exact: true })).toBeVisible();
        await expect
          .poll(() =>
            page
              .getByRole("img", { name: "生成图片 1", exact: true })
              .evaluate((img) => (img as HTMLImageElement).naturalWidth),
          )
          .toBeGreaterThan(0);
        await page.getByText("原生响应", { exact: true }).click();
      }
      await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
      if (native.id === "image_generation") {
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
        await page.getByLabel("原生响应").scrollIntoViewIfNeeded();
        await expect(page.getByLabel("原生响应")).toBeInViewport();
        await page.screenshot({ path: "../.e2e/playground/native-mobile.png", fullPage: true });
      }
      if (native.google) expect(response.request().postDataJSON()).toEqual(body);
      else if (native.id === "image_edit")
        expect(response.request().headers()["content-type"]).toContain("multipart/form-data");
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId, "原生推理必须关联真实账单").toBeTruthy();
      await expect
        .poll(async () => {
          const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
          return detail.ok() ? Number((await detail.json()).spend) : 0;
        })
        .toBeCloseTo(0.000012, 10);
      const disabled = await page.request.patch(GATEWAY + "/model/" + id + "/update", {
        headers,
        data: { model_info: { disabled: true } },
      });
      expect(disabled.status(), await disabled.text()).toBe(200);
      const denied = await page.request.post(GATEWAY + path, { headers, data: { ...body, model: name } });
      expect(denied.status(), "禁用部署必须拒绝实际调用").toBe(400);
      await guard.assertOk();
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「native image_generation saves exact protocol and executes in Playground」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC077() {
const native: {google:boolean;id:string;label:string;root:string;transport:string} = {"id":"image_generation","transport":"openai_image_generation","label":"OpenAI Images · Generate Image","root":"/v1/images/generations","google":false};

test("native image_generation saves exact protocol and executes in Playground", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const credential = "e2e-native-" + native.id;
    const name = credential + "-model";
    const upstreamModel = "e2e-native-real-" + native.id;
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    let id = "";
    try {
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("button", { name: t("pages.models.add") }).click();
      const form = page.locator("form").filter({ has: page.getByLabel("模型提供商 *") });
      await form.getByLabel("模型提供商 *").selectOption(credential);
      await expect(form.getByRole("alert").filter({ hasText: "可重试或直接输入模型 ID" })).toBeVisible();
      const upstream = form.getByRole("combobox", { name: "上游模型 *" });
      await upstream.fill(upstreamModel);
      await upstream.press("Escape");
      const endpoint = form.getByRole("combobox", { name: "上游接口协议", exact: true });
      await endpoint.click();
      await expect(page.getByText("OpenAI", { exact: true })).toBeVisible();
      await expect(page.getByText("Bypass 转发（含 Fal）", { exact: true })).toBeVisible();
      await expect(page.getByRole("option", { name: "图像", exact: true })).toHaveCount(0);
      await page.getByRole("option", { name: native.label, exact: true }).click();
      await expect(form.getByRole("combobox", { name: "上游接口协议", exact: true })).toContainText(native.label);
      await form.getByLabel("对外模型名称 *").fill(name);
      await form.getByLabel("价格来源").selectOption("manual");
      await form.locator("#editor-input_cost_per_token").fill("1");
      await form.locator("#editor-output_cost_per_token").fill("2");
      const submitted = page.waitForResponse(
        (r) => new URL(r.url()).pathname === "/model/new" && r.request().method() === "POST",
      );
      await form.getByRole("button", { name: "添加模型", exact: true }).click();
      const saved = await submitted;
      expect(saved.status(), await saved.text()).toBe(200);
      expect(saved.request().postDataJSON().model_info).toMatchObject({
        endpoint_types: native.google ? ["chat", "gemini", "vertex", "responses", "messages"] : [native.id],
        transport: native.transport,
      });
      id = (await saved.json()).model_info.id;
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      await expect(endpoint).toContainText(native.label);
      await expect(upstream).toHaveValue(upstreamModel);
      await form.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      // 图像原生请求在窄屏执行，验证 JSON 编辑器、提交与真实结果不横向溢出。
      if (native.id === "image_generation") await page.setViewportSize({ width: 390, height: 844 });
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      const path = native.google ? native.root + name + ":generateContent" : native.root;
      if (native.google) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await page
          .getByRole("option", { name: new RegExp(native.label.split(" · ")[0] + ".*generateContent$") })
          .click();
      }
      await page.getByText("接口详情", { exact: true }).click();
      await expect(page.getByText("POST " + path, { exact: true })).toBeVisible();
      const body = native.google
        ? { contents: [{ role: "user", parts: [{ text: "hello" }] }] }
        : { prompt: "hello", size: "1024x1024", n: 1 };
      if (!native.google) {
        await expect(page.getByLabel("图片尺寸", { exact: true })).toHaveValue("1024x1024");
        // 创建流程直接使用表单；先验证本地必填校验，再复现渠道不支持尺寸的真实 400 并重试。
        if (native.id === "image_generation") {
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
          await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("图片提示词");
          await page.getByLabel("图片提示词", { exact: true }).fill("hello");
          await page.getByLabel("图片尺寸", { exact: true }).fill("2048x2048");
          const failed = page.waitForResponse(
            (r) => new URL(r.url()).pathname === path && r.request().method() === "POST",
          );
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
          expect((await failed).status()).toBe(400);
          await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("2048x2048");
          await expect(page.getByLabel("图片提示词", { exact: true })).toHaveValue("hello");
          await page.getByLabel("图片尺寸", { exact: true }).fill("1024x1024");
        }
        await page.getByText("JSON 参数", { exact: true }).click();
      }
      await page.getByLabel("原生请求参数").fill(JSON.stringify(body));
      await page.getByRole("button", { name: "格式化", exact: true }).click();
      if (native.id === "image_edit") {
        await page.getByLabel("编辑图片", { exact: true }).setInputFiles({
          name: "edit.png",
          mimeType: "image/png",
          buffer: Buffer.from(
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0S8AAAAASUVORK5CYII=",
            "base64",
          ),
        });
      }
      const inferred = page.waitForResponse(
        (r) => new URL(r.url()).pathname === path && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: "提交请求", exact: true }).click();
      const response = await inferred;
      expect(response.status(), await response.text()).toBe(200);
      if (native.id === "image_generation")
        expect(response.request().postDataJSON()).toMatchObject({ ...body, model: name });
      if (!native.google) {
        await expect(page.getByRole("img", { name: "生成图片 1", exact: true })).toBeVisible();
        await expect
          .poll(() =>
            page
              .getByRole("img", { name: "生成图片 1", exact: true })
              .evaluate((img) => (img as HTMLImageElement).naturalWidth),
          )
          .toBeGreaterThan(0);
        await page.getByText("原生响应", { exact: true }).click();
      }
      await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
      if (native.id === "image_generation") {
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
        await page.getByLabel("原生响应").scrollIntoViewIfNeeded();
        await expect(page.getByLabel("原生响应")).toBeInViewport();
        await page.screenshot({ path: "../.e2e/playground/native-mobile.png", fullPage: true });
      }
      if (native.google) expect(response.request().postDataJSON()).toEqual(body);
      else if (native.id === "image_edit")
        expect(response.request().headers()["content-type"]).toContain("multipart/form-data");
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId, "原生推理必须关联真实账单").toBeTruthy();
      await expect
        .poll(async () => {
          const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
          return detail.ok() ? Number((await detail.json()).spend) : 0;
        })
        .toBeCloseTo(0.000012, 10);
      const disabled = await page.request.patch(GATEWAY + "/model/" + id + "/update", {
        headers,
        data: { model_info: { disabled: true } },
      });
      expect(disabled.status(), await disabled.text()).toBe(200);
      const denied = await page.request.post(GATEWAY + path, { headers, data: { ...body, model: name } });
      expect(denied.status(), "禁用部署必须拒绝实际调用").toBe(400);
      await guard.assertOk();
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「native image_edit saves exact protocol and executes in Playground」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC078() {
const native: {google:boolean;id:string;label:string;root:string;transport:string} = {"id":"image_edit","transport":"openai_image_edit","label":"OpenAI Images · Edit Image","root":"/v1/images/edits","google":false};

test("native image_edit saves exact protocol and executes in Playground", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const credential = "e2e-native-" + native.id;
    const name = credential + "-model";
    const upstreamModel = "e2e-native-real-" + native.id;
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    let id = "";
    try {
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("button", { name: t("pages.models.add") }).click();
      const form = page.locator("form").filter({ has: page.getByLabel("模型提供商 *") });
      await form.getByLabel("模型提供商 *").selectOption(credential);
      await expect(form.getByRole("alert").filter({ hasText: "可重试或直接输入模型 ID" })).toBeVisible();
      const upstream = form.getByRole("combobox", { name: "上游模型 *" });
      await upstream.fill(upstreamModel);
      await upstream.press("Escape");
      const endpoint = form.getByRole("combobox", { name: "上游接口协议", exact: true });
      await endpoint.click();
      await expect(page.getByText("OpenAI", { exact: true })).toBeVisible();
      await expect(page.getByText("Bypass 转发（含 Fal）", { exact: true })).toBeVisible();
      await expect(page.getByRole("option", { name: "图像", exact: true })).toHaveCount(0);
      await page.getByRole("option", { name: native.label, exact: true }).click();
      await expect(form.getByRole("combobox", { name: "上游接口协议", exact: true })).toContainText(native.label);
      await form.getByLabel("对外模型名称 *").fill(name);
      await form.getByLabel("价格来源").selectOption("manual");
      await form.locator("#editor-input_cost_per_token").fill("1");
      await form.locator("#editor-output_cost_per_token").fill("2");
      const submitted = page.waitForResponse(
        (r) => new URL(r.url()).pathname === "/model/new" && r.request().method() === "POST",
      );
      await form.getByRole("button", { name: "添加模型", exact: true }).click();
      const saved = await submitted;
      expect(saved.status(), await saved.text()).toBe(200);
      expect(saved.request().postDataJSON().model_info).toMatchObject({
        endpoint_types: native.google ? ["chat", "gemini", "vertex", "responses", "messages"] : [native.id],
        transport: native.transport,
      });
      id = (await saved.json()).model_info.id;
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      await expect(endpoint).toContainText(native.label);
      await expect(upstream).toHaveValue(upstreamModel);
      await form.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      // 图像原生请求在窄屏执行，验证 JSON 编辑器、提交与真实结果不横向溢出。
      if (native.id === "image_generation") await page.setViewportSize({ width: 390, height: 844 });
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      const path = native.google ? native.root + name + ":generateContent" : native.root;
      if (native.google) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await page
          .getByRole("option", { name: new RegExp(native.label.split(" · ")[0] + ".*generateContent$") })
          .click();
      }
      await page.getByText("接口详情", { exact: true }).click();
      await expect(page.getByText("POST " + path, { exact: true })).toBeVisible();
      const body = native.google
        ? { contents: [{ role: "user", parts: [{ text: "hello" }] }] }
        : { prompt: "hello", size: "1024x1024", n: 1 };
      if (!native.google) {
        await expect(page.getByLabel("图片尺寸", { exact: true })).toHaveValue("1024x1024");
        // 创建流程直接使用表单；先验证本地必填校验，再复现渠道不支持尺寸的真实 400 并重试。
        if (native.id === "image_generation") {
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
          await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("图片提示词");
          await page.getByLabel("图片提示词", { exact: true }).fill("hello");
          await page.getByLabel("图片尺寸", { exact: true }).fill("2048x2048");
          const failed = page.waitForResponse(
            (r) => new URL(r.url()).pathname === path && r.request().method() === "POST",
          );
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
          expect((await failed).status()).toBe(400);
          await expect(page.getByRole("region", { name: "请求结果" }).getByRole("alert")).toContainText("2048x2048");
          await expect(page.getByLabel("图片提示词", { exact: true })).toHaveValue("hello");
          await page.getByLabel("图片尺寸", { exact: true }).fill("1024x1024");
        }
        await page.getByText("JSON 参数", { exact: true }).click();
      }
      await page.getByLabel("原生请求参数").fill(JSON.stringify(body));
      await page.getByRole("button", { name: "格式化", exact: true }).click();
      if (native.id === "image_edit") {
        await page.getByLabel("编辑图片", { exact: true }).setInputFiles({
          name: "edit.png",
          mimeType: "image/png",
          buffer: Buffer.from(
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0S8AAAAASUVORK5CYII=",
            "base64",
          ),
        });
      }
      const inferred = page.waitForResponse(
        (r) => new URL(r.url()).pathname === path && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: "提交请求", exact: true }).click();
      const response = await inferred;
      expect(response.status(), await response.text()).toBe(200);
      if (native.id === "image_generation")
        expect(response.request().postDataJSON()).toMatchObject({ ...body, model: name });
      if (!native.google) {
        await expect(page.getByRole("img", { name: "生成图片 1", exact: true })).toBeVisible();
        await expect
          .poll(() =>
            page
              .getByRole("img", { name: "生成图片 1", exact: true })
              .evaluate((img) => (img as HTMLImageElement).naturalWidth),
          )
          .toBeGreaterThan(0);
        await page.getByText("原生响应", { exact: true }).click();
      }
      await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
      if (native.id === "image_generation") {
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
        await page.getByLabel("原生响应").scrollIntoViewIfNeeded();
        await expect(page.getByLabel("原生响应")).toBeInViewport();
        await page.screenshot({ path: "../.e2e/playground/native-mobile.png", fullPage: true });
      }
      if (native.google) expect(response.request().postDataJSON()).toEqual(body);
      else if (native.id === "image_edit")
        expect(response.request().headers()["content-type"]).toContain("multipart/form-data");
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId, "原生推理必须关联真实账单").toBeTruthy();
      await expect
        .poll(async () => {
          const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
          return detail.ok() ? Number((await detail.json()).spend) : 0;
        })
        .toBeCloseTo(0.000012, 10);
      const disabled = await page.request.patch(GATEWAY + "/model/" + id + "/update", {
        headers,
        data: { model_info: { disabled: true } },
      });
      expect(disabled.status(), await disabled.text()).toBe(200);
      const denied = await page.request.post(GATEWAY + path, { headers, data: { ...body, model: name } });
      expect(denied.status(), "禁用部署必须拒绝实际调用").toBe(400);
      await guard.assertOk();
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「provider directory omits fixed relay accounts while protocol directory keeps custom Qiniu transports」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC079() {


test("provider directory omits fixed relay accounts while protocol directory keeps custom Qiniu transports", async ({
  page,
}) => {
  await loginAdmin(page);
  await stableGoto(page, "/models-and-endpoints");
  await page.getByRole("button", { name: t("pages.models.add") }).click();
  const providers = await page.request.get(GATEWAY + "/public/providers/fields");
  expect(providers.ok()).toBeTruthy();
  const providerRows = (await providers.json()) as {
    provider: string;
    litellm_provider: string;
    credential_fields: { key: string }[];
  }[];
  expect(
    providerRows.some((row) => /qiniu|fenno/i.test(row.provider) || /qiniu|fenno/i.test(row.litellm_provider)),
  ).toBe(false);
  for (const id of ["CUSTOM", "CUSTOM_OPENAI"]) {
    const row = providerRows.find((item) => item.provider === id);
    expect(row, `${id} 应保留`).toBeTruthy();
    expect(row!.credential_fields.map((field) => field.key)).toEqual(expect.arrayContaining(["api_base", "api_key"]));
  }
  const endpoints = await page.request.get(GATEWAY + "/public/endpoints");
  expect(endpoints.ok()).toBeTruthy();
  const payload = (await endpoints.json()) as { transports: { id: string; providers?: string[] }[] };
  for (const id of ["qiniu_contents_generation", "qiniu_fal_kling"]) {
    expect(payload.transports.find((item) => item.id === id)).toMatchObject({
      providers: ["custom", "custom_openai", "openai"],
    });
  }
});
}
/** 用途：注册独立场景「editing retains the saved chat declaration when endpoint directory is delayed」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC080() {
const mode: string = "delayed";

test("editing retains the saved chat declaration when endpoint directory is delayed", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const name = "e2e-endpoint-edit-" + mode;
    const created = await page.request.post(GATEWAY + "/model/new", {
      headers: { Authorization: "Bearer " + (await sessionBearer(page)) },
      data: {
        model_name: name,
        litellm_params: {
          model: "glm-4.5",
          custom_llm_provider: "openai",
          api_key: "sk-fake",
          api_base: UPSTREAM,
          input_cost_per_token: 0.000001,
          output_cost_per_token: 0.000002,
        },
        model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"] },
      },
    });
    expect(created.status(), await created.text()).toBe(200);
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: t("pages.models.all") }).click();
    await openModelDetails(page, name);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    await page.route("**/public/endpoints", async (route) => {
      if (mode === "failed") {
        await route.abort("failed");
        return;
      }
      const real = await route.fetch();
      await gate;
      await route.fulfill({ response: real });
    });
    await page.getByRole("button", { name: "编辑模型", exact: true }).click();
    const endpoint = page.getByRole("combobox", { name: "上游接口协议", exact: true });
    await expect(endpoint).toBeDisabled();
    await page.getByLabel("对外模型名称 *").fill(name + "-saved");
    if (mode === "delayed") {
      release();
      await expect(endpoint).toBeEnabled();
      await expect(endpoint).toContainText("OpenAI · Chat Completions");
    } else {
      await expect(page.getByRole("alert").filter({ hasText: "已保存的端点声明已保留" })).toBeVisible();
    }
    const submitted = page.waitForResponse(
      (r) => r.request().method() === "PATCH" && /^\/model\/[^/]+\/update$/.test(new URL(r.url()).pathname),
      { timeout: 15_000 },
    );
    await page.getByRole("button", { name: "保存修改", exact: true }).click();
    const saved = await submitted;
    expect(saved.status(), await saved.text()).toBe(200);
    expect(saved.request().postDataJSON().model_info).toMatchObject({
      endpoint_types: ["chat"],
      transport: "bypass_openai_chat",
    });
    await page.unroute("**/public/endpoints");
    await page.reload();
    // 编辑保存后当前 URL 是详情页；显式返回列表再点击，验证列表和详情均读取持久化数据。
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: t("pages.models.all") }).click();
    await openModelDetails(page, name + "-saved");
    await page.getByRole("button", { name: "编辑模型", exact: true }).click();
    await expect(endpoint).toBeEnabled();
    await expect(endpoint).toContainText("OpenAI · Chat Completions");
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「editing retains the saved chat declaration when endpoint directory is failed」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC081() {
const mode: string = "failed";

test("editing retains the saved chat declaration when endpoint directory is failed", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const name = "e2e-endpoint-edit-" + mode;
    const created = await page.request.post(GATEWAY + "/model/new", {
      headers: { Authorization: "Bearer " + (await sessionBearer(page)) },
      data: {
        model_name: name,
        litellm_params: {
          model: "glm-4.5",
          custom_llm_provider: "openai",
          api_key: "sk-fake",
          api_base: UPSTREAM,
          input_cost_per_token: 0.000001,
          output_cost_per_token: 0.000002,
        },
        model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"] },
      },
    });
    expect(created.status(), await created.text()).toBe(200);
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: t("pages.models.all") }).click();
    await openModelDetails(page, name);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    await page.route("**/public/endpoints", async (route) => {
      if (mode === "failed") {
        await route.abort("failed");
        return;
      }
      const real = await route.fetch();
      await gate;
      await route.fulfill({ response: real });
    });
    await page.getByRole("button", { name: "编辑模型", exact: true }).click();
    const endpoint = page.getByRole("combobox", { name: "上游接口协议", exact: true });
    await expect(endpoint).toBeDisabled();
    await page.getByLabel("对外模型名称 *").fill(name + "-saved");
    if (mode === "delayed") {
      release();
      await expect(endpoint).toBeEnabled();
      await expect(endpoint).toContainText("OpenAI · Chat Completions");
    } else {
      await expect(page.getByRole("alert").filter({ hasText: "已保存的端点声明已保留" })).toBeVisible();
    }
    const submitted = page.waitForResponse(
      (r) => r.request().method() === "PATCH" && /^\/model\/[^/]+\/update$/.test(new URL(r.url()).pathname),
      { timeout: 15_000 },
    );
    await page.getByRole("button", { name: "保存修改", exact: true }).click();
    const saved = await submitted;
    expect(saved.status(), await saved.text()).toBe(200);
    expect(saved.request().postDataJSON().model_info).toMatchObject({
      endpoint_types: ["chat"],
      transport: "bypass_openai_chat",
    });
    await page.unroute("**/public/endpoints");
    await page.reload();
    // 编辑保存后当前 URL 是详情页；显式返回列表再点击，验证列表和详情均读取持久化数据。
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: t("pages.models.all") }).click();
    await openModelDetails(page, name + "-saved");
    await page.getByRole("button", { name: "编辑模型", exact: true }).click();
    await expect(endpoint).toBeEnabled();
    await expect(endpoint).toContainText("OpenAI · Chat Completions");
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「switching supplier clears selection and isolates the model directory」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC082() {


test("switching supplier clears selection and isolates the model directory", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  await supplier(page, "e2e-switch-first");
  await supplier(page, "e2e-switch-openai", false);
  await stableGoto(page, "/models-and-endpoints");
  await page.getByRole("button", { name: t("pages.models.add") }).click();
  const { form } = await discover(page, "e2e-switch-first");
  const upstream = form.getByRole("combobox", { name: "上游模型 *" });
  await upstream.fill("gpt-4o-mini");
  await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
  const endpoint = form.getByRole("combobox", { name: "上游接口协议", exact: true });
  await endpoint.click();
  await page.getByRole("option", { name: "OpenAI · Chat Completions", exact: true }).click();
  // 等待当前菜单退出动画完成，防止同名上游选项定位到尚未卸载的端点菜单。
  await expect(page.getByRole("option", { name: "OpenAI · Chat Completions", exact: true })).toBeHidden();

  await expect(endpoint).toContainText("OpenAI · Chat Completions");
  const { models } = await discover(page, "e2e-switch-openai");
  expect(models).toHaveLength(155);
  expect(models.some((item) => item.id === "gpt-4o-mini")).toBe(true);
  await expect(upstream).toHaveValue("");
  await expect(endpoint).toBeDisabled();
  await upstream.click();
  // 供应商切换时输入框可能仍保有焦点；键盘展开避免依赖重复触发 onFocus。
  await upstream.press("ArrowDown");
  const list = page.getByRole("listbox", { name: "上游模型联想列表" });
  await expect(list.getByRole("option")).toHaveCount(155);
  await upstream.fill("e2e-unknown-model");
  await expect(list).toContainText("没有匹配项");
  await upstream.press("Escape");
  await expect(upstream).toHaveValue("e2e-unknown-model");
  await expect(endpoint).toBeEnabled();
  await expect(endpoint).toContainText("OpenAI · Chat Completions");
  await guard.assertOk();
});
}
/** 用途：注册独立场景「unknown upstream model persists a manually declared chat endpoint and price」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC083() {


test("unknown upstream model persists a manually declared chat endpoint and price", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  const credential = "e2e-manual-endpoint-supplier";
  const name = "e2e-manual-endpoint-model";
  const model = "e2e-unregistered-chat-model";
  await supplier(page, credential, false);
  await stableGoto(page, "/models-and-endpoints");
  await page.getByRole("button", { name: t("pages.models.add") }).click();
  const { form } = await discover(page, credential);
  const upstream = form.getByRole("combobox", { name: "上游模型 *" });
  await upstream.fill(model);
  await upstream.press("Escape");
  const endpoint = form.getByRole("combobox", { name: "上游接口协议", exact: true });
  // 常规 OpenAI 连接会为任意手工模型预选 Chat 协议，用户仍可按上游能力切换。
  await expect(endpoint).toContainText("OpenAI · Chat Completions");
  await form.getByLabel("对外模型名称 *").fill(name);
  await form.getByLabel("价格来源").selectOption("manual");
  await form.locator("#editor-input_cost_per_token").fill("0.15");
  await form.locator("#editor-output_cost_per_token").fill("0.60");
  const submitted = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/model/new" && r.request().method() === "POST",
  );
  await form.getByRole("button", { name: "添加模型", exact: true }).click();
  const saved = await submitted;
  expect(saved.status(), await saved.text()).toBe(200);
  expect(saved.request().postDataJSON().model_info).toMatchObject({
    endpoint_types: ["chat", "gemini", "vertex", "responses", "messages"],
    transport: "bypass_openai_chat",
  });
  await page.reload();
  await stableGoto(page, "/models-and-endpoints");
  await page.getByRole("tab", { name: t("pages.models.all") }).click();
  await openModelDetails(page, name);
  await page.getByRole("button", { name: "编辑模型", exact: true }).click();
  await expect(upstream).toHaveValue(model);
  await expect(endpoint).toContainText("OpenAI · Chat Completions");
  await expect(form.getByLabel("价格来源")).toHaveValue("manual");
  await expect(form.locator("#editor-input_cost_per_token")).toHaveValue("0.15");
  await expect(form.locator("#editor-output_cost_per_token")).toHaveValue("0.6");
  // 回显正确之外，还必须验证用户点击的连接结果与网关推理的业务内容。
  await form.getByRole("button", { name: "取消", exact: true }).click();
  await page.getByRole("button", { name: "测试连接", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "连接正常，模型已响应。" })).toBeVisible();
  const inference = await page.request.post(GATEWAY + "/v1/chat/completions", {
    headers: { Authorization: "Bearer " + (await sessionBearer(page)) },
    data: { model: name, messages: [{ role: "user", content: "manual-endpoint-e2e" }] },
  });
  expect(inference.status(), await inference.text()).toBe(200);
  const completion = await inference.json();
  expect(completion.choices[0].message.content).toBe("e2e-ok");
  expect(completion.usage).toMatchObject({ prompt_tokens: 8, completion_tokens: 2, total_tokens: 10 });
  const callId = inference.headers()["x-litellm-call-id"];
  expect(callId, "手动端点推理必须返回可关联账单的 call_id").toBeTruthy();
  await expect
    .poll(
      async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, {
          headers: { Authorization: "Bearer " + (await sessionBearer(page)) },
        });
        if (!detail.ok()) return 0;
        return Number((await detail.json()).spend);
      },
      { message: "保存的人工费率必须用于实际账单：8×0.15/1M + 2×0.60/1M" },
    )
    .toBeCloseTo(0.0000024, 10);
  await guard.assertOk();
});
}
/** 用途：注册独立场景「model connection test shows a localized upstream failure」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC084() {


test("model connection test shows a localized upstream failure", async ({ page }) => {
  await loginAdmin(page);
  const suffix = Date.now().toString(36);
  const credential = "e2e-connection-failure-" + suffix;
  const name = "e2e-connection-failure-model-" + suffix;
  const upstreamModel = "e2e-fallback-500-connection-" + suffix;
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  let id = "";
  try {
    await supplier(page, credential);
    const created = await page.request.post(GATEWAY + "/model/new", {
      headers,
      data: {
        model_name: name,
        litellm_params: {
          model: upstreamModel,
          custom_llm_provider: "openai",
          litellm_credential_name: credential,
          input_cost_per_token: 0.000001,
          output_cost_per_token: 0.000002,
        },
        model_info: {
          transport: "bypass_openai_chat",
          endpoint_types: ["chat"],
          pricing_source: "manual",
        },
      },
    });
    expect(created.status(), await created.text()).toBe(200);
    id = (await created.json()).model_info.id;
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: t("pages.models.all") }).click();
    await openModelDetails(page, name);
    const response = page.waitForResponse(
      (item) => new URL(item.url()).pathname === "/health/test_connection" && item.request().method() === "POST",
    );
    await page.getByRole("button", { name: "测试连接", exact: true }).click();
    const tested = await response;
    expect(tested.status(), await tested.text()).toBe(200);
    expect(await tested.json()).toMatchObject({
      status: "error",
      healthy: false,
      error: "upstream_http_error",
      message: "The upstream service returned HTTP 500.",
    });
    await expect(page.getByRole("status")).toContainText("连接测试失败");
    await expect(page.getByRole("status")).toContainText("The upstream service returned HTTP 500.");
  } finally {
    if (id) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
});
}
/** 用途：注册独立场景「template editor document saves and executes a customer Responses request」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC085() {


test("template editor document saves and executes a customer Responses request", async ({ page }) => {
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  let templateID = "",
    key = "";
  try {
    await stableGoto(page, "/route-templates");
    await page.getByRole("button", { name: t("pages.routeTemplates.create"), exact: true }).click();
    await page.getByRole("tab", { name: t("pages.routeTemplates.jsonTab"), exact: true }).click();
    const example = page.getByRole("textbox", { name: t("pages.routeTemplates.jsonTab"), exact: true });
    await expect(example).toBeVisible();
    const body = JSON.parse(await example.inputValue());
    expect(body).toMatchObject({
      model_routes: [],
      retry_policy: { max_attempts: 1, timeout_seconds: 60, failure_threshold: 3, cooldown_seconds: 60 },
      routing_groups: [],
      fallbacks: [],
      context_window_fallbacks: [],
      content_policy_fallbacks: [],
    });
    const saved = await page.request.post(GATEWAY + "/route_template/new", {
      headers,
      data: { name: "endpoint-guide-" + Date.now(), body },
    });
    expect(saved.status(), await saved.text()).toBe(200);
    const template = await saved.json();
    templateID = template.id;
    // 创建接口只返回资源标识；通过详情接口回读，继续验证编辑器 JSON 已完整持久化。
    const loaded = await page.request.get(GATEWAY + "/route_template/" + templateID, { headers });
    expect(loaded.status(), await loaded.text()).toBe(200);
    expect((await loaded.json()).body).toEqual(body);
    const teams = await (await page.request.get(GATEWAY + "/v2/team/list?page=1&page_size=500", { headers })).json();
    const team = teams.teams.find((row: { team_alias: string }) => row.team_alias === "e2e-fixture-team");
    const jwt = (await page.context().cookies()).find((cookie) => cookie.name === "token")!.value;
    const userID = JSON.parse(Buffer.from(jwt.split(".")[1], "base64url").toString()).user_id;
    const minted = await page.request.post(GATEWAY + "/key/generate", {
      headers,
      data: {
        key_alias: "endpoint-guide",
        key_type: "llm_api",
        team_id: team.team_id,
        user_id: userID,
        route_template_id: templateID,
      },
    });
    expect(minted.status(), await minted.text()).toBe(200);
    key = (await minted.json()).key;
    const response = await page.request.post(GATEWAY + "/v1/responses", {
      headers: { Authorization: "Bearer " + key },
      data: { model: "gpt-4o-mini", input: "template guide example" },
    });
    expect(response.status(), await response.text()).toBe(200);
    expect((await response.json()).output[0].content[0].text).toBe("e2e-ok");
    const callID = response.headers()["x-litellm-call-id"];
    expect(callID).toBeTruthy();
    // 基础夹具没有外部价格目录，金额可为零；账单明细中的模型关联才是稳定的记账证据。
    await expect
      .poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
        return detail.ok() ? (await detail.json()).model : "";
      })
      .toBe("gpt-4o-mini");
  } finally {
    if (key) {
      expect(
        (
          await page.request.post(GATEWAY + "/key/update", { headers, data: { key, route_template_id: null } })
        ).status(),
      ).toBe(200);
      expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })).status()).toBe(200);
    }
    if (templateID)
      expect(
        (
          await page.request.post(GATEWAY + "/route_template/" + templateID + "/delete", { headers, data: {} })
        ).status(),
      ).toBe(200);
  }
});
}
/** 用途：注册独立场景「upstream-only chat automatically exposes all customer dialogue APIs」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC086() {
const source: {id:string;transport:string} = {"id":"chat","transport":"bypass_openai_chat"};

test("upstream-only chat automatically exposes all customer dialogue APIs", async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const name = "e2e-auto-" + source.id;
    const credential = name + "-credential";
    let id = "";
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: name,
          litellm_params: {
            model: "e2e-native-real-" + source.id,
            custom_llm_provider: "custom",
            litellm_credential_name: credential,
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
          model_info: { transport: source.transport, pricing_source: "manual" },
        },
      });
      expect(created.status(), await created.text()).toBe(200);
      id = (await created.json()).model_info.id;
      const available = await page.request.get(GATEWAY + "/model/available", { headers });
      const deployment = (await available.json()).data.find((row: { id: string }) => row.id === name);
      expect(deployment.endpoints).toHaveLength(8);
      expect(deployment.endpoints.filter((entry: { endpoint_id: string }) => entry.endpoint_id.startsWith("bypass:")).map((entry: { endpoint_id: string }) => entry.endpoint_id)).toEqual(["bypass:openai-chat"]);
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      const published = page.getByRole("region", { name: "XHub 对外接口" });
      await published.getByRole("button", { name: "XHub 对外接口", exact: true }).click();
      await expect(published).toContainText("/v1/chat/completions");
      await expect(published).toContainText("/v1/responses");
      await expect(published).toContainText("/v1/messages");
      await expect(published).toContainText("/v1beta/models/");
      await expect(published).toContainText("/vertex/v1/models/");
      await page.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      // 从真实端点选择器选择默认透传，并验证原生响应和实际账单。
      for (const target of [
        { path: "/bypass/openai/v1/chat/completions", google: true },
        { path: "/v1/chat/completions", google: false },
        { path: "/v1/responses", google: false },
        { path: "/v1/messages", google: false },
        { path: "/v1beta/models/" + name + ":generateContent", google: true },
        { path: "/vertex/v1/models/" + name + ":generateContent", google: true },
      ]) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await expect(page.getByRole("option")).toHaveCount(8);
        await page.getByRole("option").filter({ hasText: " · " + target.path }).click();
        // SDK 在代理根地址下使用无 /v1 前缀路径，原生编辑器使用完整注册路径；两者均为网关真实入口。
        const pending = page.waitForResponse(
          (response) =>
            [target.path, target.google ? target.path : target.path.replace(/^\/v1/, "")].includes(
              new URL(response.url()).pathname,
            ) && response.request().method() === "POST",
        );
        if (target.google) {
          await page
            .getByLabel("原生请求参数")
            .fill(JSON.stringify(target.path.startsWith("/bypass/") ? { messages: [{ role: "user", content: "Say hello." }], max_tokens: 32 } : { contents: [{ role: "user", parts: [{ text: "hello" }] }] }));
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
        } else {
          await page.getByRole("button", { name: t("Clear Chat"), exact: true }).click();
          await page
            .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
            .filter({ visible: true })
            .fill("hello");
          await page.getByRole("button", { name: t("Send message") }).click();
        }
        const response = await pending;
        // 对话响应可能是持续读取的 SSE，Chromium 不保证能再次读取响应体；校验状态后以页面结果和账单验证内容。
        expect(response.status(), "客户接口 " + target.path + " 应成功返回").toBe(200);
        if (target.google) await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
        else await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
        const callId = response.headers()["x-litellm-call-id"];
        expect(callId, "跨协议请求必须记录真实账单").toBeTruthy();
        await expect
          .poll(async () => {
            const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
            return detail.ok() ? Number((await detail.json()).spend) : 0;
          })
          .toBeCloseTo(0.000012, 10);
      }
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「upstream-only responses automatically exposes all customer dialogue APIs」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC087() {
const source: {id:string;transport:string} = {"id":"responses","transport":"bypass_openai_responses"};

test("upstream-only responses automatically exposes all customer dialogue APIs", async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const name = "e2e-auto-" + source.id;
    const credential = name + "-credential";
    let id = "";
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: name,
          litellm_params: {
            model: "e2e-native-real-" + source.id,
            custom_llm_provider: "custom",
            litellm_credential_name: credential,
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
          model_info: { transport: source.transport, pricing_source: "manual" },
        },
      });
      expect(created.status(), await created.text()).toBe(200);
      id = (await created.json()).model_info.id;
      const available = await page.request.get(GATEWAY + "/model/available", { headers });
      const deployment = (await available.json()).data.find((row: { id: string }) => row.id === name);
      expect(deployment.endpoints).toHaveLength(8);
      expect(deployment.endpoints.filter((entry: { endpoint_id: string }) => entry.endpoint_id.startsWith("bypass:")).map((entry: { endpoint_id: string }) => entry.endpoint_id)).toEqual(["bypass:openai-responses"]);
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      const published = page.getByRole("region", { name: "XHub 对外接口" });
      await published.getByRole("button", { name: "XHub 对外接口", exact: true }).click();
      await expect(published).toContainText("/v1/chat/completions");
      await expect(published).toContainText("/v1/responses");
      await expect(published).toContainText("/v1/messages");
      await expect(published).toContainText("/v1beta/models/");
      await expect(published).toContainText("/vertex/v1/models/");
      await page.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      // 从真实端点选择器选择默认透传，并验证原生响应和实际账单。
      for (const target of [
        { path: "/bypass/openai/v1/responses", google: false },
        { path: "/v1/chat/completions", google: false },
        { path: "/v1/responses", google: false },
        { path: "/v1/messages", google: false },
        { path: "/v1beta/models/" + name + ":generateContent", google: true },
        { path: "/vertex/v1/models/" + name + ":generateContent", google: true },
      ]) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await expect(page.getByRole("option")).toHaveCount(8);
        await page.getByRole("option").filter({ hasText: " · " + target.path }).click();
        // SDK 在代理根地址下使用无 /v1 前缀路径，原生编辑器使用完整注册路径；两者均为网关真实入口。
        const pending = page.waitForResponse(
          (response) =>
            [target.path, target.google ? target.path : target.path.replace(/^\/v1/, "")].includes(
              new URL(response.url()).pathname,
            ) && response.request().method() === "POST",
        );
        if (target.google) {
          await page
            .getByLabel("原生请求参数")
            .fill(JSON.stringify({ contents: [{ role: "user", parts: [{ text: "hello" }] }] }));
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
        } else {
          await page.getByRole("button", { name: t("Clear Chat"), exact: true }).click();
          await page
            .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
            .filter({ visible: true })
            .fill("hello");
          await page.getByRole("button", { name: t("Send message") }).click();
        }
        const response = await pending;
        // 对话响应可能是持续读取的 SSE，Chromium 不保证能再次读取响应体；校验状态后以页面结果和账单验证内容。
        expect(response.status(), "客户接口 " + target.path + " 应成功返回").toBe(200);
        if (target.google) await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
        else await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
        const callId = response.headers()["x-litellm-call-id"];
        expect(callId, "跨协议请求必须记录真实账单").toBeTruthy();
        await expect
          .poll(async () => {
            const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
            return detail.ok() ? Number((await detail.json()).spend) : 0;
          })
          .toBeCloseTo(0.000012, 10);
      }
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「upstream-only claude automatically exposes all customer dialogue APIs」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC088() {
const source: {id:string;transport:string} = {"id":"claude","transport":"bypass_anthropic_messages"};

test("upstream-only claude automatically exposes all customer dialogue APIs", async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const name = "e2e-auto-" + source.id;
    const credential = name + "-credential";
    let id = "";
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: name,
          litellm_params: {
            model: "e2e-native-real-" + source.id,
            custom_llm_provider: "custom",
            litellm_credential_name: credential,
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
          model_info: { transport: source.transport, pricing_source: "manual" },
        },
      });
      expect(created.status(), await created.text()).toBe(200);
      id = (await created.json()).model_info.id;
      const available = await page.request.get(GATEWAY + "/model/available", { headers });
      const deployment = (await available.json()).data.find((row: { id: string }) => row.id === name);
      expect(deployment.endpoints).toHaveLength(8);
      expect(deployment.endpoints.filter((entry: { endpoint_id: string }) => entry.endpoint_id.startsWith("bypass:")).map((entry: { endpoint_id: string }) => entry.endpoint_id)).toEqual(["bypass:anthropic-messages"]);
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      const published = page.getByRole("region", { name: "XHub 对外接口" });
      await published.getByRole("button", { name: "XHub 对外接口", exact: true }).click();
      await expect(published).toContainText("/v1/chat/completions");
      await expect(published).toContainText("/v1/responses");
      await expect(published).toContainText("/v1/messages");
      await expect(published).toContainText("/v1beta/models/");
      await expect(published).toContainText("/vertex/v1/models/");
      await page.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      // 从真实端点选择器选择默认透传，并验证原生响应和实际账单。
      for (const target of [
        { path: "/bypass/anthropic/v1/messages", google: false },
        { path: "/v1/chat/completions", google: false },
        { path: "/v1/responses", google: false },
        { path: "/v1/messages", google: false },
        { path: "/v1beta/models/" + name + ":generateContent", google: true },
        { path: "/vertex/v1/models/" + name + ":generateContent", google: true },
      ]) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await expect(page.getByRole("option")).toHaveCount(8);
        await page.getByRole("option").filter({ hasText: " · " + target.path }).click();
        // SDK 在代理根地址下使用无 /v1 前缀路径，原生编辑器使用完整注册路径；两者均为网关真实入口。
        const pending = page.waitForResponse(
          (response) =>
            [target.path, target.google ? target.path : target.path.replace(/^\/v1/, "")].includes(
              new URL(response.url()).pathname,
            ) && response.request().method() === "POST",
        );
        if (target.google) {
          await page
            .getByLabel("原生请求参数")
            .fill(JSON.stringify({ contents: [{ role: "user", parts: [{ text: "hello" }] }] }));
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
        } else {
          await page.getByRole("button", { name: t("Clear Chat"), exact: true }).click();
          await page
            .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
            .filter({ visible: true })
            .fill("hello");
          await page.getByRole("button", { name: t("Send message") }).click();
        }
        const response = await pending;
        // 对话响应可能是持续读取的 SSE，Chromium 不保证能再次读取响应体；校验状态后以页面结果和账单验证内容。
        expect(response.status(), "客户接口 " + target.path + " 应成功返回").toBe(200);
        if (target.google) await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
        else await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
        const callId = response.headers()["x-litellm-call-id"];
        expect(callId, "跨协议请求必须记录真实账单").toBeTruthy();
        await expect
          .poll(async () => {
            const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
            return detail.ok() ? Number((await detail.json()).spend) : 0;
          })
          .toBeCloseTo(0.000012, 10);
      }
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「upstream-only gemini automatically exposes all customer dialogue APIs」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC089() {
const source: {id:string;transport:string} = {"id":"gemini","transport":"gemini_generate_content"};

test("upstream-only gemini automatically exposes all customer dialogue APIs", async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const name = "e2e-auto-" + source.id;
    const credential = name + "-credential";
    let id = "";
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: name,
          litellm_params: {
            model: "e2e-native-real-" + source.id,
            custom_llm_provider: "custom",
            litellm_credential_name: credential,
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
          model_info: { transport: source.transport, pricing_source: "manual" },
        },
      });
      expect(created.status(), await created.text()).toBe(200);
      id = (await created.json()).model_info.id;
      const available = await page.request.get(GATEWAY + "/model/available", { headers });
      const deployment = (await available.json()).data.find((row: { id: string }) => row.id === name);
      expect(deployment.endpoints).toHaveLength(7);
      expect(
        deployment.endpoints.some((entry: { endpoint_id: string }) => entry.endpoint_id.startsWith("bypass:")),
      ).toBe(false);
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      const published = page.getByRole("region", { name: "XHub 对外接口" });
      await published.getByRole("button", { name: "XHub 对外接口", exact: true }).click();
      await expect(published).toContainText("/v1/chat/completions");
      await expect(published).toContainText("/v1/responses");
      await expect(published).toContainText("/v1/messages");
      await expect(published).toContainText("/v1beta/models/");
      await expect(published).toContainText("/vertex/v1/models/");
      await page.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      for (const target of [
        { path: "/v1/chat/completions", google: false },
        { path: "/v1/responses", google: false },
        { path: "/v1/messages", google: false },
        { path: "/v1beta/models/" + name + ":generateContent", google: true },
        { path: "/vertex/v1/models/" + name + ":generateContent", google: true },
      ]) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await expect(page.getByRole("option")).toHaveCount(7);
        await page.getByRole("option").filter({ hasText: target.path }).click();
        // SDK 在代理根地址下使用无 /v1 前缀路径，原生编辑器使用完整注册路径；两者均为网关真实入口。
        const pending = page.waitForResponse(
          (response) =>
            [target.path, target.google ? target.path : target.path.replace(/^\/v1/, "")].includes(
              new URL(response.url()).pathname,
            ) && response.request().method() === "POST",
        );
        if (target.google) {
          await page
            .getByLabel("原生请求参数")
            .fill(JSON.stringify({ contents: [{ role: "user", parts: [{ text: "hello" }] }] }));
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
        } else {
          await page.getByRole("button", { name: t("Clear Chat"), exact: true }).click();
          await page
            .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
            .filter({ visible: true })
            .fill("hello");
          await page.getByRole("button", { name: t("Send message") }).click();
        }
        const response = await pending;
        // 对话响应可能是持续读取的 SSE，Chromium 不保证能再次读取响应体；校验状态后以页面结果和账单验证内容。
        expect(response.status(), "客户接口 " + target.path + " 应成功返回").toBe(200);
        if (target.google) await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
        else await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
        const callId = response.headers()["x-litellm-call-id"];
        expect(callId, "跨协议请求必须记录真实账单").toBeTruthy();
        await expect
          .poll(async () => {
            const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
            return detail.ok() ? Number((await detail.json()).spend) : 0;
          })
          .toBeCloseTo(0.000012, 10);
      }
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「upstream-only vertex automatically exposes all customer dialogue APIs」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC090() {
const source: {id:string;transport:string} = {"id":"vertex","transport":"vertex_generate_content"};

test("upstream-only vertex automatically exposes all customer dialogue APIs", async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const name = "e2e-auto-" + source.id;
    const credential = name + "-credential";
    let id = "";
    const createdCredential = await page.request.post(GATEWAY + "/credentials", {
      headers,
      data: {
        credential_name: credential,
        credential_info: { custom_llm_provider: "custom" },
        credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
      },
    });
    expect(createdCredential.status(), await createdCredential.text()).toBe(200);
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: name,
          litellm_params: {
            model: "e2e-native-real-" + source.id,
            custom_llm_provider: "custom",
            litellm_credential_name: credential,
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
          model_info: { transport: source.transport, pricing_source: "manual" },
        },
      });
      expect(created.status(), await created.text()).toBe(200);
      id = (await created.json()).model_info.id;
      const available = await page.request.get(GATEWAY + "/model/available", { headers });
      const deployment = (await available.json()).data.find((row: { id: string }) => row.id === name);
      expect(deployment.endpoints).toHaveLength(7);
      expect(
        deployment.endpoints.some((entry: { endpoint_id: string }) => entry.endpoint_id.startsWith("bypass:")),
      ).toBe(false);
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: t("pages.models.all") }).click();
      await openModelDetails(page, name);
      await page.getByRole("button", { name: "编辑模型", exact: true }).click();
      const published = page.getByRole("region", { name: "XHub 对外接口" });
      await published.getByRole("button", { name: "XHub 对外接口", exact: true }).click();
      await expect(published).toContainText("/v1/chat/completions");
      await expect(published).toContainText("/v1/responses");
      await expect(published).toContainText("/v1/messages");
      await expect(published).toContainText("/v1beta/models/");
      await expect(published).toContainText("/vertex/v1/models/");
      await page.getByRole("button", { name: "取消", exact: true }).click();
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      for (const target of [
        { path: "/v1/chat/completions", google: false },
        { path: "/v1/responses", google: false },
        { path: "/v1/messages", google: false },
        { path: "/v1beta/models/" + name + ":generateContent", google: true },
        { path: "/vertex/v1/models/" + name + ":generateContent", google: true },
      ]) {
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await expect(page.getByRole("option")).toHaveCount(7);
        await page.getByRole("option").filter({ hasText: target.path }).click();
        // SDK 在代理根地址下使用无 /v1 前缀路径，原生编辑器使用完整注册路径；两者均为网关真实入口。
        const pending = page.waitForResponse(
          (response) =>
            [target.path, target.google ? target.path : target.path.replace(/^\/v1/, "")].includes(
              new URL(response.url()).pathname,
            ) && response.request().method() === "POST",
        );
        if (target.google) {
          await page
            .getByLabel("原生请求参数")
            .fill(JSON.stringify({ contents: [{ role: "user", parts: [{ text: "hello" }] }] }));
          await page.getByRole("button", { name: "提交请求", exact: true }).click();
        } else {
          await page.getByRole("button", { name: t("Clear Chat"), exact: true }).click();
          await page
            .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
            .filter({ visible: true })
            .fill("hello");
          await page.getByRole("button", { name: t("Send message") }).click();
        }
        const response = await pending;
        // 对话响应可能是持续读取的 SSE，Chromium 不保证能再次读取响应体；校验状态后以页面结果和账单验证内容。
        expect(response.status(), "客户接口 " + target.path + " 应成功返回").toBe(200);
        if (target.google) await expect(page.getByLabel("原生响应")).toContainText("e2e-ok");
        else await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
        const callId = response.headers()["x-litellm-call-id"];
        expect(callId, "跨协议请求必须记录真实账单").toBeTruthy();
        await expect
          .poll(async () => {
            const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
            return detail.ok() ? Number((await detail.json()).spend) : 0;
          })
          .toBeCloseTo(0.000012, 10);
      }
    } finally {
      if (id)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
