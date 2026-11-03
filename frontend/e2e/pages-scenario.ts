import { expect, test } from "@playwright/test";

import { DASHBOARD_PAGES, NAV_GROUPS, loginAdmin, stableGoto, t, watchGateway } from "./helpers";
/** 用途：注册独立场景「nav groups and Virtual Keys home」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC095() {


test("nav groups and Virtual Keys home", async ({ page }) => {
    await loginAdmin(page);
    for (const g of NAV_GROUPS) {
      await expect(page.getByText(g, { exact: true }).first()).toBeVisible();
    }
    await expect(page.getByText(t("nav.apiKeys")).first()).toBeVisible();
    await expect(page.getByText(t("nav.playground")).first()).toBeVisible();
  });
}
/** 用途：注册独立场景「all dashboard pages stay in the shell」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC096() {


test("all dashboard pages stay in the shell", async ({ page }) => {
    test.setTimeout(180_000);
    const guard = watchGateway(page);
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(`${page.url()}: ${err.message}`));
    await loginAdmin(page);
    for (const path of DASHBOARD_PAGES) {
      try {
        await stableGoto(page, path);
      } catch (err) {
        if (!page.url().includes("/login")) throw err;
        await loginAdmin(page);
        await stableGoto(page, path);
      }
      await expect(page, path).not.toHaveURL(/\/login/);
      await expect(page.getByText("This page couldn’t load")).toHaveCount(0);
      await expect(page.getByText(/Dashboard error:/), path).toHaveCount(0, { timeout: 3_000 });
      await expect(page.getByText(t("nav.groups.mine")).first()).toBeVisible();
    }
    expect(errors, errors.join("\n")).toEqual([]);
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「chat shell is not admin sidebar」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC097() {


test("chat shell is not admin sidebar", async ({ page }) => {
    await loginAdmin(page);
    await stableGoto(page, "/chat");
    await expect(page).toHaveURL(/\/chat/, { timeout: 15_000 });
    await expect(page.locator("[data-slot=sidebar-group-label]", { hasText: t("nav.groups.mine") })).toHaveCount(0);
  });
}
