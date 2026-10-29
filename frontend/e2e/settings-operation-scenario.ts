import { matchesGatewayResponse } from "./gateway-response.cjs";
import { expect } from "@playwright/test";
import { test } from "./policy-config-service-scenario";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate, type Locale } from "../src/i18n/translate";

// 独立列举公开接口范围；不从服务端实现表读取，避免同时漏掉相同入口。
const settingsRoutes = [
  "GET /config/field/info",
  "GET /config/yaml",
  "GET /get/default_team_settings",
  "GET /get/internal_user_settings",
  "GET /get/ui_settings",
  "GET /get/user_banner",
  "GET /get_favicon",
  "GET /get_image",
  "GET /get_logo_url",
  "GET /settings",
  "GET /sso/get/ui_settings",
  "PATCH /update/default_team_settings",
  "PATCH /update/internal_user_settings",
  "PATCH /update/ui_settings",
  "PATCH /update/user_banner"
];

/** 注册C3设置入口的双语真实浏览器流程；locale选择语言，返回注册结果，无生产凭据。
 * 页面创建策略并刷新读回，15个旧设置接口返回明确错误且不改变策略/上游，随后页面正常对话。
 * 不恢复未挂载的旧模拟器；接口错误由真实HTTP核验，策略由独立fixture删除，schema由运行器清理。
 */
export function registerSettingsOperationBoundary(locale: Locale) {
  test("unsupported settings preserve configuration and chat " + locale, async ({ page, policyResources: resources }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = resources.headers = { Authorization: "Bearer " + await sessionBearer(page) };
    // 侧栏读取旧设置失败后仍可使用现有导航；真实页面不得把空KV当作配置生效。
    const sidebarSettings = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/get/ui_settings", "GET"));
    await page.reload();
    expect((await sidebarSettings).status()).toBe(501);
    await stableGoto(page, "/policies");
    // 当前生产策略页仅为配置表单，沿用现有名称；本批不替换页面或恢复旧模拟器。
    await page.getByLabel("名称", { exact: true }).fill(resources.name);
    const pending = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/policies", "POST"));
    await page.getByRole("button", { name: "创建", exact: true }).click();
    const created = await pending;
    expect(created.status()).toBe(200);
    const policy = await created.json();
    const readPath = GATEWAY + "/policies/" + encodeURIComponent(policy.policy_id);
    const original = await page.request.get(readPath, { headers });
    expect(original.status()).toBe(200);
    const saved = await original.json();
    const before = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(before.status()).toBe(200);
    const calls = await before.json();
    for (const route of settingsRoutes) {
      const [method, template] = route.split(" ");
      const path = template.replace(/\{[^}]+\}/g, policy.policy_id);
      const response = await page.request.fetch(GATEWAY + path, { method, headers, data: { private_input: "do-not-echo" } });
      expect(response.status(), route + ": " + await response.text()).toBe(501);
      expect(response.headers()["cache-control"]).toBe("no-store");
      expect(await response.json()).toMatchObject({ error: { type: "unsupported_settings_operation" } });
      expect(await response.text()).not.toContain("do-not-echo");
    }
    const after = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(after.status()).toBe(200);
    expect(await after.json()).toEqual(calls);
    const preserved = await page.request.get(readPath, { headers });
    expect(preserved.status()).toBe(200);
    expect(await preserved.json()).toEqual(saved);
    await page.reload();
    await expect(page.getByTestId("column-list")).toContainText(resources.name);
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
    await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /\/v1\/chat\/completions$/ }).click();
    const prompt = "Verify ordinary chat after unavailable legacy settings.";
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
