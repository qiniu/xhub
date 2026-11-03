import { expect, test } from "@playwright/test";

import { GATEWAY, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";
/** 用途：注册独立场景「Responses incremental continuation SSE」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC120() {
const stream: boolean = true;

test("Responses incremental continuation SSE", async ({ page }) => {
    await loginAdmin(page);
    await page.evaluate(value => sessionStorage.setItem("streamingEnabled", JSON.stringify(value)), stream);
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: "e2e-responses-history", exact: true }).click();
    await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/responses$/ }).click();
    await expect(page.getByRole("switch", { name: t("Use API session management") })).toBeChecked();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    let previous = "";
    let finalCall = "";
    const sessionId = await page.evaluate(() => sessionStorage.getItem("playgroundSessionId"));
    expect(sessionId).toBeTruthy();
    for (let turn = 1; turn <= 3; turn++) {
      if (turn === 3) {
        await expect.poll(() => page.evaluate(() => sessionStorage.getItem("chatHistory"))).toContain("history-ok-2");
        await expect.poll(() => page.evaluate(() => sessionStorage.getItem("responsesSessionId"))).toBe(previous);
        await page.reload();
        // 页面刷新后重新选择模型及入口，历史和 response ID 由会话存储恢复。
        await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
        await page.getByRole("option", { name: "e2e-responses-history", exact: true }).click();
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/responses$/ }).click();
        await expect(page.getByText("history-ok-2", { exact: true })).toBeVisible();
      }
      await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true }).fill("continuation-" + turn);
      const pending = page.waitForResponse(response => /\/(v1\/)?responses$/.test(new URL(response.url()).pathname) && response.request().method() === "POST");
      await page.getByRole("button", { name: t("Send message") }).click();
      const response = await pending;
      // SSE 响应由页面持续读取，统一从页面状态取得已解析的 response ID，避免二次读取 Chromium 响应体。
      expect(response.status(), "Responses 续接请求应成功返回").toBe(200);
      expect(response.request().headers()["x-session-id"]).toBe(sessionId);
      expect(response.headers()["x-session-id"]).toBe(sessionId);
      const body = response.request().postDataJSON();
      expect(body.stream).toBe(stream);
      expect(body.input, "浏览器必须只发送本轮消息").toHaveLength(1);
      expect(body.input[0].content).toBe("continuation-" + turn);
      if (turn > 1) expect(body.previous_response_id).toBe(previous);
      else expect(body.previous_response_id).toBeUndefined();
      await expect(page.getByText("history-ok-" + turn, { exact: true })).toBeVisible();
      const prior = previous;
      await expect.poll(() => page.evaluate(() => sessionStorage.getItem("responsesSessionId")))
        .toMatch(/^chatcmpl-/);
      previous = (await page.evaluate(() => sessionStorage.getItem("responsesSessionId"))) || "";
      expect(previous).not.toBe(prior);
      const call = response.headers()["x-litellm-call-id"];
      finalCall = call;
      await expect.poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + call, { headers });
        return detail.ok() ? Number((await detail.json()).spend) : 0;
      }).toBeCloseTo(0.000012, 10);
      const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + call, { headers });
      const log = await detail.json();
      expect(log.session_id).toBe(sessionId);
      expect(log.session_source).toBe(turn === 1 ? "explicit" : "parent");
      expect(log.cache_hit, "提示词缓存不得变成整段响应缓存命中").toBe("false");
      expect(log.metadata.cached_tokens).toBe(turn === 1 ? 0 : 4);
      expect(log.metadata.upstream_response).toMatchObject({
        status_code: 200, usage_reported: true,
        headers: { "X-Upstream-Trace": ["trace-first", "trace-second"], "Set-Cookie": ["***"] },
        usage: { prompt_tokens_details: { cached_tokens: turn === 1 ? 0 : 4 }, provider_statistics: { turn, cache_source: "local-fixture" } },
        billing_usage: stream
          ? { prompt_tokens_details: { cached_tokens: turn === 1 ? 0 : 4 } }
          : { input_tokens_details: { cached_tokens: turn === 1 ? 0 : 4 } },
      });

    }
    await stableGoto(page, "/logs");
    await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).fill(finalCall);
    const row = page.getByRole("row").filter({ hasText: finalCall });
    await expect(row).toHaveCount(1);
    await row.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("region", { name: /上游响应|Upstream Response/, exact: true })).toBeVisible();
    await expect(dialog).toContainText("X-Upstream-Trace");
    await expect(dialog).toContainText("trace-second");
    await expect(dialog).toContainText(t("Prompt Cache Read Tokens"));
    await expect(dialog).not.toContainText("fixture-secret");
    await page.screenshot({ path: "../artifacts/upstream-diagnostics/" + (stream ? "sse" : "json") + ".png", fullPage: true });
  });
}
/** 用途：注册独立场景「Responses incremental continuation JSON」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC121() {
const stream: boolean = false;

test("Responses incremental continuation JSON", async ({ page }) => {
    await loginAdmin(page);
    await page.evaluate(value => sessionStorage.setItem("streamingEnabled", JSON.stringify(value)), stream);
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: "e2e-responses-history", exact: true }).click();
    await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/responses$/ }).click();
    await expect(page.getByRole("switch", { name: t("Use API session management") })).toBeChecked();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    let previous = "";
    let finalCall = "";
    const sessionId = await page.evaluate(() => sessionStorage.getItem("playgroundSessionId"));
    expect(sessionId).toBeTruthy();
    for (let turn = 1; turn <= 3; turn++) {
      if (turn === 3) {
        await expect.poll(() => page.evaluate(() => sessionStorage.getItem("chatHistory"))).toContain("history-ok-2");
        await expect.poll(() => page.evaluate(() => sessionStorage.getItem("responsesSessionId"))).toBe(previous);
        await page.reload();
        // 页面刷新后重新选择模型及入口，历史和 response ID 由会话存储恢复。
        await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
        await page.getByRole("option", { name: "e2e-responses-history", exact: true }).click();
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/responses$/ }).click();
        await expect(page.getByText("history-ok-2", { exact: true })).toBeVisible();
      }
      await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true }).fill("continuation-" + turn);
      const pending = page.waitForResponse(response => /\/(v1\/)?responses$/.test(new URL(response.url()).pathname) && response.request().method() === "POST");
      await page.getByRole("button", { name: t("Send message") }).click();
      const response = await pending;
      // SSE 响应由页面持续读取，统一从页面状态取得已解析的 response ID，避免二次读取 Chromium 响应体。
      expect(response.status(), "Responses 续接请求应成功返回").toBe(200);
      expect(response.request().headers()["x-session-id"]).toBe(sessionId);
      expect(response.headers()["x-session-id"]).toBe(sessionId);
      const body = response.request().postDataJSON();
      expect(body.stream).toBe(stream);
      expect(body.input, "浏览器必须只发送本轮消息").toHaveLength(1);
      expect(body.input[0].content).toBe("continuation-" + turn);
      if (turn > 1) expect(body.previous_response_id).toBe(previous);
      else expect(body.previous_response_id).toBeUndefined();
      await expect(page.getByText("history-ok-" + turn, { exact: true })).toBeVisible();
      const prior = previous;
      await expect.poll(() => page.evaluate(() => sessionStorage.getItem("responsesSessionId")))
        .toMatch(/^chatcmpl-/);
      previous = (await page.evaluate(() => sessionStorage.getItem("responsesSessionId"))) || "";
      expect(previous).not.toBe(prior);
      const call = response.headers()["x-litellm-call-id"];
      finalCall = call;
      await expect.poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + call, { headers });
        return detail.ok() ? Number((await detail.json()).spend) : 0;
      }).toBeCloseTo(0.000012, 10);
      const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + call, { headers });
      const log = await detail.json();
      expect(log.session_id).toBe(sessionId);
      expect(log.session_source).toBe(turn === 1 ? "explicit" : "parent");
      expect(log.cache_hit, "提示词缓存不得变成整段响应缓存命中").toBe("false");
      expect(log.metadata.cached_tokens).toBe(turn === 1 ? 0 : 4);
      expect(log.metadata.upstream_response).toMatchObject({
        status_code: 200, usage_reported: true,
        headers: { "X-Upstream-Trace": ["trace-first", "trace-second"], "Set-Cookie": ["***"] },
        usage: { prompt_tokens_details: { cached_tokens: turn === 1 ? 0 : 4 }, provider_statistics: { turn, cache_source: "local-fixture" } },
        billing_usage: stream
          ? { prompt_tokens_details: { cached_tokens: turn === 1 ? 0 : 4 } }
          : { input_tokens_details: { cached_tokens: turn === 1 ? 0 : 4 } },
      });

    }
    await stableGoto(page, "/logs");
    await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).fill(finalCall);
    const row = page.getByRole("row").filter({ hasText: finalCall });
    await expect(row).toHaveCount(1);
    await row.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("region", { name: /上游响应|Upstream Response/, exact: true })).toBeVisible();
    await expect(dialog).toContainText("X-Upstream-Trace");
    await expect(dialog).toContainText("trace-second");
    await expect(dialog).toContainText(t("Prompt Cache Read Tokens"));
    await expect(dialog).not.toContainText("fixture-secret");
    await page.screenshot({ path: "../artifacts/upstream-diagnostics/" + (stream ? "sse" : "json") + ".png", fullPage: true });
  });
}
