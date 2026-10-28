import { expect, test, type Page } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t, watchGateway } from "./helpers";


/** 选择公开模型及客户聊天端点；参数 page/name 为页面和别名，返回完成 Promise；只操作隔离测试页面。 */
async function selectChat(page: Page, name: string) {
  await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
  await page.getByRole("option", { name, exact: true }).click();
  await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
  await page
    .getByRole("option")
    .filter({ hasText: /\/v1\/chat\/completions$/ })
    .click();
}


/** 创建隔离部署；参数 page/name/key 为浏览器、公开别名和是否配密钥，返回部署 ID。
 * 仅使用本地上游，调用方 finally 删除部署，账单由 runner 私有 schema 清理。 */
async function deployment(page: Page, name: string, key = true) {
  const response = await page.request.post(GATEWAY + "/model/new", {
    headers: { Authorization: "Bearer " + (await sessionBearer(page)) },
    data: {
      model_name: name,
      litellm_params: {
        model: name,
        custom_llm_provider: "openai",
        api_base: UPSTREAM,
        ...(key ? { api_key: "sk-fake" } : {}),
      },
      model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat", "responses"] },
    },
  });
  expect(response.status(), await response.text()).toBe(200);
  return (await response.json()).model_info.id as string;
}
/** 用途：注册独立场景「chat explains missing credentials and unavailable deployment, then recovers」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC108() {


test("chat explains missing credentials and unavailable deployment, then recovers", async ({ page }) => {
  await loginAdmin(page);
  const name = "e2e-playground-recovery";
  const id = await deployment(page, name, false);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  let deleted = false;
  try {
    await stableGoto(page, "/playground");
    await selectChat(page, name);
    const input = page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true });
    await input.fill("保留这条调试草稿");
    const denied = page.waitForResponse(
      (r) => /chat\/completions$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: t("Send message") }).click();
    expect((await denied).status()).toBe(401);
    await expect(page.getByRole("alert").filter({ hasText: "模型未配置上游密钥" })).toBeVisible();
    await expect(input).toHaveValue("保留这条调试草稿");
    await expect(page.getByRole("link", { name: "检查模型与端点配置" })).toHaveAttribute(
      "href",
      "/ui/models-and-endpoints",
    );
    await page.screenshot({ path: "../.e2e/playground/credential-diagnostic.png", fullPage: true });
    expect(
      (
        await page.request.patch(GATEWAY + "/model/" + id + "/update", {
          headers,
          data: { litellm_params: { api_key: "sk-fake" } },
        })
      ).status(),
    ).toBe(200);
    const success = page.waitForResponse(
      (r) => /chat\/completions$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: t("Send message") }).click();
    const response = await success;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON().messages).toHaveLength(1);
    await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
    await expect(input).toHaveValue("");
    const callId = response.headers()["x-litellm-call-id"];
    expect(callId).toBeTruthy();
    await expect
      .poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
        return detail.ok() ? (await detail.json()).model : "";
      })
      .toBe(name);
    expect(
      (
        await page.request.patch(GATEWAY + "/model/" + id + "/update", {
          headers,
          data: { model_info: { disabled: true } },
        })
      ).status(),
    ).toBe(200);
    await input.fill("不可用部署");
    await page.getByRole("button", { name: t("Send message") }).click();
    await expect(page.getByRole("alert").filter({ hasText: "模型没有可用部署" })).toBeVisible();
    await page.getByRole("button", { name: t("Clear Chat"), exact: true }).click();
    await expect(page.getByRole("alert").filter({ hasText: "模型没有可用部署" })).toHaveCount(0);
    expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    deleted = true;
    await input.fill("已删除部署");
    const unavailable = page.waitForResponse(
      (r) => /chat\/completions$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: t("Send message") }).click();
    expect((await unavailable).status()).toBe(400);
    await expect(page.getByRole("alert").filter({ hasText: "模型没有可用部署" })).toBeVisible();
  } finally {
    if (!deleted)
      expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
  }
});
}
/** 用途：注册独立场景「single workspace has no comparison controls and preserves bilingual chat」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC109() {


/** 前置真实网关、私有 schema 与本地上游；兼容模式使用非安全 HTTP 域名和未注入地址的生产构建。
 * 验证登录、双语聊天、会话恢复/清空、真实推理与日志；finally 删除模型，runner 回收 schema。 */
test("single workspace has no comparison controls and preserves bilingual chat", async ({ page }) => {
  const guard = watchGateway(page);
  const localFailures: string[] = [];
  page.on("requestfailed", request => {
    if (request.url().startsWith("http://localhost:4000")) localFailures.push(request.url());
  });
  await loginAdmin(page);
  if (process.env.E2E_PORTABLE_CONSOLE === "1") {
    expect(await page.evaluate(() => window.isSecureContext), "必须在普通 HTTP 上验证，不能被 localhost 安全上下文掩盖").toBe(false);
    expect(await page.evaluate(() => typeof crypto.randomUUID)).toBe("undefined");
  }
  const name = "e2e-single-workspace";
  const id = await deployment(page, name);
  const switchedName = name + "-switched";
  const switchedID = await deployment(page, switchedName);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  try {
    await page.evaluate(() => {
      sessionStorage.setItem("selectedModel", "e2e-single-workspace");
      sessionStorage.setItem("endpointType", "responses");
      sessionStorage.removeItem("endpointPath");
    });
    await stableGoto(page, "/playground");
    await expect(page.getByRole("tablist")).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "对比", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "添加对比卡片", exact: true })).toHaveCount(0);
    await expect(page.getByPlaceholder(t("Select an endpoint"), { exact: true })).not.toBeDisabled();
    await expect.poll(() => page.evaluate(() => sessionStorage.getItem("endpointPath"))).toBe("/v1/chat/completions");
    await page.evaluate(() => sessionStorage.removeItem("endpointType"));
    await selectChat(page, name);
    await expect.poll(() => page.evaluate(() => sessionStorage.getItem("endpointPath"))).toBe("/v1/chat/completions");
    expect(await page.evaluate(() => sessionStorage.getItem("endpointType"))).toBeNull();
    const prompt = "Describe a sunrise in one sentence.";
    await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).fill(prompt);
    await page.getByRole("button", { name: "English", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Models and endpoints", exact: true })).toBeVisible();
    await expect(page.getByRole("tablist")).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "Compare", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Add Comparison", exact: true })).toHaveCount(0);
    const input = page.getByPlaceholder("Type your message... (Shift+Enter for new line)");
    await expect(input).toHaveValue(prompt);
    const guide = page.getByLabel("Complete curl request", { exact: true });
    await guide.locator("summary").click();
    await expect(guide.getByLabel("Copy request example", { exact: true }).filter({ hasText: "curl" })).toContainText(
      prompt,
    );
    const pending = page.waitForResponse(
      (response) =>
        /chat\/completions$/.test(new URL(response.url()).pathname) && response.request().method() === "POST",
    );
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    const response = await pending;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON().messages).toEqual([{ role: "user", content: prompt }]);
    await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
    await expect(input).toHaveValue("");
    // 校验真实请求和响应使用客户端会话 ID，刷新及切换入口不拆分会话。
    const sessionId = await page.evaluate(() => sessionStorage.getItem("playgroundSessionId"));
    expect(sessionId).toBeTruthy();
    expect(response.request().headers()["x-session-id"]).toBe(sessionId);
    expect(response.headers()["x-session-id"]).toBe(sessionId);
    await expect.poll(() => page.evaluate(() => sessionStorage.getItem("chatHistory"))).toContain(prompt);
    await page.reload();
    await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
    expect(await page.evaluate(() => sessionStorage.getItem("playgroundSessionId"))).toBe(sessionId);
    await page.getByRole("button", { name: "中文", exact: true }).click();
    await selectChat(page, switchedName);
    await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /\/v1\/responses$/ }).click();
    expect(await page.evaluate(() => sessionStorage.getItem("playgroundSessionId"))).toBe(sessionId);
    // 实际发送不同模型及端点，验证同一会话归组，而不是仅检查选择器和本地存储。
    const switchedInput = page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)"));
    await switchedInput.fill("Continue after changing the model and endpoint.");
    const switchedPending = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/responses") && r.request().method() === "POST");
    await page.getByRole("button", { name: t("Send message"), exact: true }).click();
    const switched = await switchedPending;
    expect(switched.status()).toBe(200);
    expect(switched.request().postDataJSON().model).toBe(switchedName);
    expect(switched.headers()["x-session-id"]).toBe(sessionId);
    // 响应完成后输入框会清空，发送按钮因此禁用；等待下一条输入可用再继续切换。
    await expect(switchedInput).toHaveValue("");
    await selectChat(page, name);
    await switchedInput.fill("Continue after returning to the original model and endpoint.");
    const returnedPending = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/chat/completions") && r.request().method() === "POST");
    await page.getByRole("button", { name: t("Send message"), exact: true }).click();
    const returned = await returnedPending;
    expect(returned.status()).toBe(200);
    expect(returned.headers()["x-session-id"]).toBe(sessionId);
    for (const [result, requestedModel, endpoint] of [[response, name, "chat/completions"], [switched, switchedName, "responses"], [returned, name, "chat/completions"]] as const) {
      await expect.poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + result.headers()["x-litellm-call-id"], { headers });
        if (!detail.ok()) return null;
        const log = await detail.json();
        return [log.model, log.session_id, log.session_source, String(log.inbound_endpoint).endsWith(endpoint)];
      }).toEqual([requestedModel, sessionId, "explicit", true]);
    }
    const callId = response.headers()["x-litellm-call-id"];
    expect(callId).toBeTruthy();
    await expect
      .poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
        return detail.ok() ? (await detail.json()).model : "";
      })
      .toBe(name);
    await expect(page.getByText("e2e-ok", { exact: true }).first()).toBeVisible();
    await page.getByRole("button", { name: t("Clear Chat"), exact: true }).click();
    await expect(page.getByText("e2e-ok", { exact: true })).toHaveCount(0);
    const nextSessionId = await page.evaluate(() => sessionStorage.getItem("playgroundSessionId"));
    expect(nextSessionId).toBeTruthy();
    expect(nextSessionId).not.toBe(sessionId);
    // 相同输入在清空后应建立新日志组，并确认会话头贯穿页面、网关和记录。
    await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).fill(prompt);
    const nextPending = page.waitForResponse(r => /chat\/completions$/.test(new URL(r.url()).pathname) && r.request().method() === "POST");
    await page.getByRole("button", { name: t("Send message"), exact: true }).click();
    const nextResponse = await nextPending;
    expect(nextResponse.status()).toBe(200);
    expect(nextResponse.request().headers()["x-session-id"]).toBe(nextSessionId);
    expect(nextResponse.headers()["x-session-id"]).toBe(nextSessionId);
    await guard.assertOk();
    expect(localFailures, "发布镜像不能请求访问者自己的 localhost 网关").toEqual([]);
    await expect.poll(async () => {
      const log = await page.request.get(GATEWAY + "/spend/logs/ui/" + nextResponse.headers()["x-litellm-call-id"], { headers });
      return log.ok() ? (await log.json()).session_id : "";
    }).toBe(nextSessionId);
  } finally {
    expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: switchedID } })).status()).toBe(200);
    expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
  }
});
}
/** 用途：注册独立场景「cancel and clear ignore delayed real responses; mobile chat remains usable」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC110() {


test("cancel and clear ignore delayed real responses; mobile chat remains usable", async ({ page }) => {
  await loginAdmin(page);
  await stableGoto(page, "/playground");
  // 损坏的旧开关不能使工作区崩溃；随后仍通过真实网关完成取消和发送。
  await page.evaluate(() => sessionStorage.setItem("codeInterpreterEnabled", "{bad"));
  await page.reload();
  await selectChat(page, "gpt-4o-mini");
  const input = page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true });
  for (const action of ["clear", "stop"]) {
    let release!: () => void;
    let received!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const fetched = new Promise<void>((resolve) => {
      received = resolve;
    });
    await page.route(/chat\/completions$/, async (route) => {
      const real = await route.fetch();
      expect(real.status()).toBe(200);
      received();
      await gate;
      await route.fulfill({ response: real }).catch(() => {});
    });
    await input.fill("delayed-" + action);
    await page.getByRole("button", { name: t("Send message") }).click();
    await fetched;
    if (action === "clear") await page.getByRole("button", { name: t("Clear Chat"), exact: true }).click();
    else await page.getByRole("button", { name: t("Stop request"), exact: true }).click();
    release();
    await page.unroute(/chat\/completions$/);
    await expect(page.getByText("e2e-ok", { exact: true })).toHaveCount(0);
    await expect(input).toHaveValue("delayed-" + action);
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await input.fill("mobile-real-request");
  await page.getByRole("button", { name: t("Send message") }).click();
  await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: "../.e2e/playground/chat-mobile.png", fullPage: true });
});
}
