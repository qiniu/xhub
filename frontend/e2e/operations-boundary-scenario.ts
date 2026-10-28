import { expect, test } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate, type Locale } from "../src/i18n/translate";

/** 注册运维失败边界的真实浏览器流程；locale为控制台语言，返回无。
 * 前置隔离网关/PostgreSQL及本地协议上游；登录、运维请求明确拒绝、公开就绪仍可读，最后调试台实际对话。
 * 这些旧运维入口没有独立可用页面，不伪造表单；页面会话由浏览器回收，登录夹具/聊天记录由运行器严格删除私有schema。
 */
export function registerOperationsBoundary(locale: Locale) {
  test("unsupported operations preserve real health and chat " + locale, async ({ page }) => {
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    for (const operation of [
      { method: "GET", path: "/coordination_redis/settings" },
      { method: "POST", path: "/coordination_redis/settings" },
      { method: "POST", path: "/coordination_redis/settings/test" },
      { method: "POST", path: "/debug/memory/gc/configure" },
      { method: "GET", path: "/debug/memory/summary" },
      { method: "GET", path: "/memory-usage" },
      { method: "GET", path: "/health/drain" },
      { method: "GET", path: "/health/services" },
    ]) {
      const denied = await page.request.fetch(GATEWAY + operation.path, { method: operation.method, headers, data: { host: "127.0.0.1", port: 1, password: "local-test-secret", generation_0: 500 } });
      expect(denied.status(), operation.path + ": " + await denied.text()).toBe(501);
      expect(await denied.json()).toMatchObject({ error: { type: "unsupported_operation" } });
      expect(await denied.text()).not.toContain("local-test-secret");
    }
    for (const path of ["/health", "/health/readiness", "/health/readiness/details"]) {
      const ready = await page.request.get(GATEWAY + path);
      expect(ready.status(), path + ": " + await ready.text()).toBe(200);
      expect(await ready.json()).toMatchObject({ status: "ready" });
    }
    const alive = await page.request.get(GATEWAY + "/health/liveness");
    expect(alive.status()).toBe(200);
    expect(await alive.json()).toMatchObject({ status: "ok" });
    const anonymous = await page.request.post(GATEWAY + "/coordination_redis/settings/test", { data: {} });
    expect(anonymous.status()).toBe(401);
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(translate(locale, "Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: /gpt-4o-mini/ }).click();
    const prompt = "Verify unsupported maintenance operations do not interrupt this conversation.";
    await page.getByTestId("chat-composer-input").fill(prompt);
    await page.getByTestId("chat-send-button").click();
    await expect(page.getByTestId("message-surface").filter({ hasText: /e2e-ok/ })).toBeVisible();
    await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
  });
}
