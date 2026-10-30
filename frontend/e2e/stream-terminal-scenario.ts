import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";

/** 创建本地上游连接与专属部署后运行完整浏览器流程；参数为页面、夹具后缀和场景回调，无返回值。
 * 真实后台负责鉴权和计费；finally 删除部署及连接，运行器回收隔离 schema 中的身份、日志和账单。 */
async function withTerminalModel(page: Page, suffix: string, run: (model: string, bearer: string) => Promise<void>) {
  await loginAdmin(page);
  const bearer = await sessionBearer(page);
  const headers = { Authorization: "Bearer " + bearer };
  const model = "terminal-browser-" + suffix;
  const credential = model + "-connection";
  let id = "";
  expect(
    (
      await page.request.post(GATEWAY + "/credentials", {
        headers,
        data: {
          credential_name: credential,
          credential_info: { custom_llm_provider: "openai" },
          credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" },
        },
      })
    ).status(),
  ).toBe(200);
  try {
    const response = await page.request.post(GATEWAY + "/model/new", {
      headers,
      data: {
        model_name: model,
        litellm_params: {
          model: "e2e-stream-terminal-" + suffix,
          custom_llm_provider: "openai",
          litellm_credential_name: credential,
          input_cost_per_token: 0.000001,
          output_cost_per_token: 0.000002,
        },
        model_info: {
          transport: suffix === "response" ? "bypass_openai_responses" : "bypass_openai_chat",
          endpoint_types: ["responses", "chat", "messages"],
          pricing_source: "manual",
        },
      },
    });
    expect(response.status(), await response.text()).toBe(200);
    id = (await response.json()).model_info.id;
    await run(model, bearer);
  } finally {
    if (id) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
}

/** 在 Playground 选择真实部署和协议并发起流请求；返回浏览器收到的响应头，调用方继续等待页面结束并核对正文和账单。 */
async function sendTerminalPrompt(page: Page, model: string, endpoint: string) {
  await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "true"));
  await stableGoto(page, "/playground");
  await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
  await page.getByRole("option", { name: model, exact: true }).click();
  await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
  await page
    .getByRole("option")
    .filter({ hasText: new RegExp("(?:^|\\s)/v1/" + endpoint + "$") })
    .click();
  await page
    .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
    .filter({ visible: true })
    .fill("Return the terminal test marker.");
  const pending = page.waitForResponse(
    (response) => new URL(response.url()).pathname.endsWith("/" + endpoint) && response.request().method() === "POST",
  );
  await page.getByRole("button", { name: t("Send message") }).click();
  return pending;
}

/** 查询真实持久化账单后打开日志详情；参数是调用ID及期望状态/费用，返回详情弹窗供场景断言，调用数据由运行器清理。 */
async function inspectTerminalBill(
  page: Page,
  bearer: string,
  callID: string,
  expected: { status: string; spend: number },
) {
  expect(callID, "真实网关必须返回日志关联 ID").toBeTruthy();
  await expect
    .poll(async () => {
      const response = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, {
        headers: { Authorization: "Bearer " + bearer },
      });
      if (!response.ok()) return null;
      const bill = await response.json();
      return { status: bill.status, spend: Number(bill.spend) };
    })
    .toEqual(expected);
  await stableGoto(page, "/logs");
  if (expected.status === "error") await page.getByRole("tab", { name: /错误日志|Error Logs/, exact: true }).click();
  await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).fill(callID);
  const row = page.getByRole("row").filter({ hasText: callID });
  await expect(row).toHaveCount(1);
  await row.click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  return dialog;
}

/** 注册缺失语义终态的独立场景；参数为夹具后缀，由入口包装文件调用，不共享请求或数据。 */
export function registerMalformedTerminal(suffix: "missing" | "response") {
  const endpoint = suffix === "missing" ? "responses" : "chat/completions";
  const reason = suffix === "missing" ? "without finish_reason" : "invalid upstream response terminal";
  /** 前置真实浏览器、Go 后台、私有数据库和非法终态上游；验证页面结束、协议失败与零费用日志，无成功会话发布；finally 清理部署。 */
  test("stream terminal rejects malformed " + suffix + " through the console", async ({ page }) => {
    await withTerminalModel(page, suffix, async (model, bearer) => {
      const response = await sendTerminalPrompt(page, model, endpoint);
      expect(response.status()).toBe(200);
      await expect(page.getByRole("button", { name: t("Send message") })).toBeVisible();
      // 客户端收到协议错误会主动取消读取，Chromium不保证此时还能取回response.text；验证用户可见错误与真实持久化失败。
      await expect(page.getByText(reason, { exact: false }).filter({ visible: true }).first()).toBeVisible();
      if (endpoint === "responses")
        expect(await page.evaluate(() => sessionStorage.getItem("responsesSessionId"))).toBeFalsy();
      const dialog = await inspectTerminalBill(page, bearer, response.headers()["x-litellm-call-id"], {
        status: "error",
        spend: 0,
      });
      await expect(dialog).toContainText(reason);
    });
  });
}

/** 前置上游在合法终态后保留连接十秒；验证页面五秒内完成、合法响应与一次成功费用，防止再次等待 HTTP EOF；finally 清理部署。 */
export function registerHeldTerminal() {
  test("stream terminal completes before upstream EOF through the console", async ({ page }) => {
    await withTerminalModel(page, "held", async (model, bearer) => {
      const response = await sendTerminalPrompt(page, model, "responses");
      expect(response.status()).toBe(200);
      await expect(page.getByRole("button", { name: t("Send message") })).toBeVisible({ timeout: 5000 });
      await expect(page.getByText("TERMINAL_BROWSER_OK", { exact: true }).last()).toBeVisible();
      expect(await response.text()).toContain('"type":"response.completed"');
      await inspectTerminalBill(page, bearer, response.headers()["x-litellm-call-id"], {
        status: "success",
        spend: 0.000012,
      });
    });
  });
}

/** 前置真实浏览器会话和截断工具上游；通过同源网关发送 JSON/SSE，验证工具仍可见但 stop_reason=max_tokens，页面可查看两次真实成功用量；finally 清理部署。 */
export function registerTruncatedTerminal() {
  test("stream terminal preserves truncated tools in JSON and SSE billing", async ({ page }) => {
    await withTerminalModel(page, "length", async (model, bearer) => {
      for (const stream of [false, true]) {
        const result = await page.evaluate(
          async ({ model, bearer, stream }) => {
            const body = {
              model,
              stream,
              max_tokens: 32,
              messages: [{ role: "user", content: "Call lookup." }],
              tools: [{ name: "lookup", input_schema: { type: "object" } }],
            };
            // 测试必须在浏览器上下文直接读取原始 SSE，避免产品客户端封装掩盖协议终态。
            // eslint-disable-next-line no-restricted-syntax
            const response = await fetch("/gw/v1/messages", {
              method: "POST",
              headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json" },
              body: JSON.stringify(body),
            });
            return {
              status: response.status,
              raw: await response.text(),
              callID: response.headers.get("x-litellm-call-id") || "",
            };
          },
          { model, bearer, stream },
        );
        expect(result.status, result.raw).toBe(200);
        expect(result.raw).toContain('"stop_reason":"max_tokens"');
        expect(result.raw).toContain("call_terminal_browser");
        expect(result.raw).not.toContain('"stop_reason":"tool_use"');
        const dialog = await inspectTerminalBill(page, bearer, result.callID, { status: "success", spend: 0.000012 });
        await expect(dialog).toContainText("length");
        // 关闭详情后再通过同源代理发起下一次调用，保证每次账单独立可查询。
        await page.keyboard.press("Escape");
      }
    });
  });
}
