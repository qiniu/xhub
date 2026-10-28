import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, login, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { matchesGatewayResponse } from "./gateway-response.cjs";
import { translate, type Locale } from "../src/i18n/translate";

// 独立列出旧入口，不从后台实现读拒绝表，防止同源清单同时漏项。
const routes = [
  "GET /authorize",
  "GET /discover",
  "GET /enabled",
  "GET /invitation/info",
  "GET /jwt/key/mapping/info",
  "GET /jwt/key/mapping/list",
  "GET /onboarding/get_token",
  "GET /token/generate",
  "POST /introspect",
  "POST /invitation/delete",
  "POST /invitation/new",
  "POST /invitation/update",
  "POST /jwt/key/mapping/delete",
  "POST /jwt/key/mapping/new",
  "POST /jwt/key/mapping/update",
  "POST /onboarding/claim_token",
  "POST /register",
  "POST /revoke",
  "POST /test/connection",
  "POST /token",
  "POST /v3/login/exchange"
];

/** 注册双语真实浏览器流程；locale控制登录及调试台语言，无线上账户或外部服务凭据。
 * 管理员调用21条旧接口验证明确拒绝、零上游调用；随后普通用户页面登录、保留500预算并完成真实对话。
 * 页面请求不mock；独立API上下文在页面关闭后仍删除临时用户及Key，运行器最终回收隔离schema。
 */
export function registerIdentityAliasBoundary(locale: Locale) {
  test("identity aliases reject false success without breaking login and chat " + locale, async ({ page, playwright }) => {
    const tr = (key: string) => translate(locale, key);
    const api = await playwright.request.newContext({ timeout: 10_000 });
    let userID = "";
    let headers: Record<string, string> = {};
    try {
      await loginAdmin(page);
      // 登录辅助函数按当前语言定位表单；先通过真实语言按钮设置，避免英语场景仍落在默认中文页。
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      headers = { Authorization: "Bearer " + await sessionBearer(page) };
      const before = await api.get(UPSTREAM + "/e2e/request-count");
      expect(before.status()).toBe(200);
      const calls = await before.json();
      for (const route of routes) {
        const [method, path] = route.split(" ");
        const response = await page.request.fetch(GATEWAY + path, { method, headers, data: { code: "private-fixture-code" } });
        expect(response.status(), route + ": " + await response.text()).toBe(501);
        expect(await response.json()).toMatchObject({ error: { type: "unsupported_operation" } });
        expect(response.headers()["cache-control"]).toBe("no-store");
        expect(response.headers()["set-cookie"]).toBeUndefined();
        expect(await response.text()).not.toContain("private-fixture-code");
      }
      const after = await api.get(UPSTREAM + "/e2e/request-count");
      expect(after.status()).toBe(200);
      expect(await after.json()).toEqual(calls);
      const email = `identity-alias-${locale}-${Date.now()}@example.invalid`;
      const created = await api.post(GATEWAY + "/user/new", { headers, data: { user_email: email, password: "identity-test-password", user_role: "user", max_budget: 500 } });
      expect(created.status(), await created.text()).toBe(200);
      userID = (await created.json()).user_id;
      expect(userID).toBeTruthy();
      await login(page, email, "identity-test-password", locale);
      await expect(page.getByTestId("create-key-button")).toBeVisible();
      const session = { Authorization: "Bearer " + await sessionBearer(page) };
      for (const route of routes) {
        const [method, path] = route.split(" ");
        const forbidden = await page.request.fetch(GATEWAY + path, { method, headers: session, data: {} });
        expect(forbidden.status(), route).toBe(403);
      }
      const me = await page.request.get(GATEWAY + "/auth/me", { headers: session });
      expect(me.status()).toBe(200);
      expect(await me.json()).toMatchObject({ user_id: userID, user_role: "internal_user" });
      const budget = await page.request.get(GATEWAY + "/v2/user/info", { headers: session });
      expect(budget.status()).toBe(200);
      expect((await budget.json()).user_info).toMatchObject({ max_budget: 500, spend: 0 });
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
      await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
      await page.getByRole("option").filter({ hasText: /\/v1\/chat\/completions$/ }).click();
      const prompt = "Verify ordinary chat after unavailable identity operations.";
      await page.getByTestId("chat-composer-input").fill(prompt);
      const chat = page.waitForResponse(r => ["/v1/chat/completions", "/chat/completions"].some(endpoint => matchesGatewayResponse(r.url(), r.request().method(), endpoint, "POST")));
      await page.getByTestId("chat-send-button").click();
      expect((await chat).status()).toBe(200);
      await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
      await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
      const called = await api.get(UPSTREAM + "/e2e/request-count");
      expect(called.status()).toBe(200);
      expect(await called.json()).not.toEqual(calls);
    } finally {
      try {
        if (userID) {
          const deleted = await api.post(GATEWAY + "/user/delete", { headers, data: { user_id: userID } });
          expect(deleted.status(), "临时用户及Key必须清理成功").toBe(200);
        }
      } finally { await api.dispose(); }
    }
  });
}
