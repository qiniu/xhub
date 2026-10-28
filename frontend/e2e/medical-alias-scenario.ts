import { expect, test } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册旧文本分析入口的双语真实回归；locale 控制页面可访问名称，返回注册结果。
 * 前置隔离数据库和本地 HTTP 模型；登录后核对四个旧接口明确失败、零上游调用，再由页面完成正常对话。
 * 不 mock 路由、不创建业务配置；登录夹具和账单由运行器回收专属 schema，不能指向生产。
 */
export function registerMedicalAlias(locale: Locale) {
  test("medical aliases reject false success " + locale, async ({ page }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const before = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(before.status()).toBe(200);
    const baseline = await before.json();
    for (const path of ["/comprehendmedical", "/comprehendmedical/detect-entities-v2", "/comprehendmedical/info", "/comprehendmedical/delete"]) {
      const response = await page.request.post(GATEWAY + path, {
        headers, data: { id: "fixture-only", Text: "do-not-echo", LanguageCode: "en" },
      });
      expect(response.status(), path + ": " + await response.text()).toBe(501);
      expect(response.headers()["cache-control"]).toBe("no-store");
      expect(await response.text()).toContain("unsupported_operation");
      expect(await response.text()).not.toContain("do-not-echo");
    }
    const after = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(after.status()).toBe(200);
    expect(await after.json(), "旧操作不得调用供应商").toEqual(baseline);
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
    await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /\/v1\/chat\/completions$/ }).click();
    const prompt = "Verify normal chat after unsupported analysis operations.";
    await page.getByTestId("chat-composer-input").fill(prompt);
    const pending = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/chat/completions") && r.request().method() === "POST");
    await page.getByTestId("chat-send-button").click();
    const response = await pending;
    expect(response.status()).toBe(200);
    await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
    await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
    const called = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(called.status()).toBe(200);
    expect(await called.json(), "正常对话必须真实调用供应商").not.toEqual(baseline);
  });
}
