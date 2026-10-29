import { matchesGatewayResponse } from "./gateway-response.cjs";
import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate, type Locale } from "../src/i18n/translate";

// 独立列举公开接口范围；不从服务端实现表读取，避免同时漏掉相同入口。
const modelAliasRoutes = [
  "DELETE /fallback/{model}",
  "GET /fallback/login",
  "GET /fallback/{model}",
  "GET /model/deprecations",
  "GET /model/info",
  "GET /model/settings",
  "GET /models/{model_id}",
  "GET /v1/model/deprecations",
  "GET /v1/model/info",
  "POST /fallback",
  "POST /make_public",
  "POST /model/block",
  "POST /model/unblock",
  "POST /model_group/make_public"
];

/** 注册模型旧接口双语真实浏览器流程；locale选择语言，使用隔离网关/PG/受控上游。
 * 真实模型页面读回、14条旧别名明确失败且不改变部署/上游，再刷新列表并发起页面对话。
 * 不模拟HTTP或恢复未挂载的旧表单；登录夹具与用量记录由运行器隔离schema最终清理。 */
export function registerModelAliasBoundary(locale: Locale) {
  test("legacy model aliases preserve real deployment and chat " + locale, async ({ page }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const readPath = GATEWAY + "/v2/model/info";
    const original = await page.request.get(readPath, { headers });
    expect(original.status()).toBe(200);
    const saved = await original.json();
    expect(saved.data.some((row: {model_name: string}) => row.model_name === "gpt-4o-mini")).toBe(true);
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: tr("pages.models.all"), exact: true }).click();
    await expect(page.getByText("gpt-4o-mini", { exact: true }).first()).toBeVisible();
    const before = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(before.status()).toBe(200);
    const calls = await before.json();
    for (const route of modelAliasRoutes) {
      const [method, template] = route.split(" ");
      const path = template.replace(/\{[^}]+\}/g, "gpt-4o-mini");
      const response = await page.request.fetch(GATEWAY + path, { method, headers, data: { private_input: "do-not-echo" } });
      expect(response.status(), route + ": " + await response.text()).toBe(501);
      expect(response.headers()["cache-control"]).toBe("no-store");
      expect(await response.json()).toMatchObject({ error: { type: "unsupported_model_operation" } });
      expect(await response.text()).not.toContain("do-not-echo");
    }
    const after = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(after.status()).toBe(200);
    expect(await after.json()).toEqual(calls);
    const preserved = await page.request.get(readPath, { headers });
    expect(preserved.status()).toBe(200);
    expect(await preserved.json()).toEqual(saved);
    await page.reload();
    await page.getByRole("tab", { name: tr("pages.models.all"), exact: true }).click();
    await expect(page.getByText("gpt-4o-mini", { exact: true }).first()).toBeVisible();
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
    await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /\/v1\/chat\/completions$/ }).click();
    const prompt = "Verify ordinary chat after unavailable legacy model aliases.";
    await page.getByTestId("chat-composer-input").fill(prompt);
    const chat = page.waitForResponse(r => ["/v1/chat/completions", "/chat/completions"].some(endpoint => matchesGatewayResponse(r.url(), r.request().method(), endpoint, "POST")));
    await page.getByTestId("chat-send-button").click();
    expect((await chat).status()).toBe(200);
    await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
    await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
    const called = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(called.status()).toBe(200);
    expect(await called.json()).not.toEqual(calls);
  });
}
