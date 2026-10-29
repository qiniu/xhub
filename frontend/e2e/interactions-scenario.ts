import { expect, test } from "@playwright/test";

import { loginAdmin, sessionBearer, stableGoto, uiPath, t, GATEWAY, watchGateway } from "./helpers";

import { recordChain } from "./report";
/** 用途：注册独立场景「click every internal sidebar destination」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC030() {


test("click every internal sidebar destination", async ({ page }) => {
  test.setTimeout(180_000);
  const guard = watchGateway(page);
  await loginAdmin(page);
  const sidebar = page.locator('[data-slot="sidebar"]');
  const hrefs = await sidebar.getByRole("link").evaluateAll((links) =>
    [...new Set(links.map((link) => link.getAttribute("href") || ""))]
      .filter((href) => href.startsWith("/") && href !== "/ui/"),
  );
  expect(hrefs.length).toBeGreaterThan(10);
  for (const href of hrefs) {
    await test.step("sidebar " + href, async () => {
      await sidebar.locator('a[href="' + href + '"]').first().click();
      await expect.poll(() => new URL(page.url()).pathname.replace(/\/$/, "")).toBe(href.replace(/\/$/, ""));
      await expect(page.getByTestId("admin-shell")).toBeVisible();
      await expect(page.getByText(/Dashboard error:|This page couldn’t load/)).toHaveCount(0);
      recordChain("sidebar pass " + href);
    });
  }
  await guard.assertOk();
});
}
/** 用途：注册独立场景「click each top level tab on /models-and-endpoints」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC031() {
const route: string = "/models-and-endpoints";

test("click each top level tab on /models-and-endpoints", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    await stableGoto(page, route);
    const tabs = page.getByRole("tablist").first().getByRole("tab");
    const names = await tabs.allTextContents();
    for (const name of names) {
      const tab = page.getByRole("tablist").first().getByRole("tab", { name: name.trim(), exact: true });
      await test.step(name, async () => {
        await tab.click();
        await expect(tab).toHaveAttribute("aria-selected", "true");
        await expect(page.getByText(/Dashboard error:|This page couldn’t load/)).toHaveCount(0);
        recordChain("tab pass " + route + " " + name.trim());
      });
    }
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「click each top level tab on /guardrails」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC032() {
const route: string = "/guardrails";

test("click each top level tab on /guardrails", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    await stableGoto(page, route);
    const tabs = page.getByRole("tablist").first().getByRole("tab");
    const names = await tabs.allTextContents();
    for (const name of names) {
      const tab = page.getByRole("tablist").first().getByRole("tab", { name: name.trim(), exact: true });
      await test.step(name, async () => {
        await tab.click();
        await expect(tab).toHaveAttribute("aria-selected", "true");
        await expect(page.getByText(/Dashboard error:|This page couldn’t load/)).toHaveCount(0);
        recordChain("tab pass " + route + " " + name.trim());
      });
    }
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「click each top level tab on /usage」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC033() {
const route: string = "/usage";

test("click each top level tab on /usage", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    await stableGoto(page, route);
    const tabs = page.getByRole("tablist").first().getByRole("tab");
    const names = await tabs.allTextContents();
    for (const name of names) {
      const tab = page.getByRole("tablist").first().getByRole("tab", { name: name.trim(), exact: true });
      await test.step(name, async () => {
        await tab.click();
        await expect(tab).toHaveAttribute("aria-selected", "true");
        await expect(page.getByText(/Dashboard error:|This page couldn’t load/)).toHaveCount(0);
        recordChain("tab pass " + route + " " + name.trim());
      });
    }
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「click each top level tab on /logs」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC034() {
const route: string = "/logs";

test("click each top level tab on /logs", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    await stableGoto(page, route);
    const tabs = page.getByRole("tablist").first().getByRole("tab");
    const names = await tabs.allTextContents();
    for (const name of names) {
      const tab = page.getByRole("tablist").first().getByRole("tab", { name: name.trim(), exact: true });
      await test.step(name, async () => {
        await tab.click();
        await expect(tab).toHaveAttribute("aria-selected", "true");
        await expect(page.getByText(/Dashboard error:|This page couldn’t load/)).toHaveCount(0);
        recordChain("tab pass " + route + " " + name.trim());
      });
    }
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「click each top level tab on /price-data」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC035() {
const route: string = "/price-data";

test("click each top level tab on /price-data", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    await stableGoto(page, route);
    const tabs = page.getByRole("tablist").first().getByRole("tab");
    const names = await tabs.allTextContents();
    for (const name of names) {
      const tab = page.getByRole("tablist").first().getByRole("tab", { name: name.trim(), exact: true });
      await test.step(name, async () => {
        await tab.click();
        await expect(tab).toHaveAttribute("aria-selected", "true");
        await expect(page.getByText(/Dashboard error:|This page couldn’t load/)).toHaveCount(0);
        recordChain("tab pass " + route + " " + name.trim());
      });
    }
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「click each top level tab on /route-templates」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC036() {
const route: string = "/route-templates";

test("click each top level tab on /route-templates", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    await stableGoto(page, route);
    const tabs = page.getByRole("tablist").first().getByRole("tab");
    const names = await tabs.allTextContents();
    for (const name of names) {
      const tab = page.getByRole("tablist").first().getByRole("tab", { name: name.trim(), exact: true });
      await test.step(name, async () => {
        await tab.click();
        await expect(tab).toHaveAttribute("aria-selected", "true");
        await expect(page.getByText(/Dashboard error:|This page couldn’t load/)).toHaveCount(0);
        recordChain("tab pass " + route + " " + name.trim());
      });
    }
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「click each top level tab on /admin-panel」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC037() {
const route: string = "/admin-panel";

test("click each top level tab on /admin-panel", async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    await stableGoto(page, route);
    const tabs = page.getByRole("tablist").first().getByRole("tab");
    const names = await tabs.allTextContents();
    for (const name of names) {
      const tab = page.getByRole("tablist").first().getByRole("tab", { name: name.trim(), exact: true });
      await test.step(name, async () => {
        await tab.click();
        await expect(tab).toHaveAttribute("aria-selected", "true");
        await expect(page.getByText(/Dashboard error:|This page couldn’t load/)).toHaveCount(0);
        recordChain("tab pass " + route + " " + name.trim());
      });
    }
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「model search, pagination boundaries and cancelled create」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC038() {


test("model search, pagination boundaries and cancelled create", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  await stableGoto(page, "/models-and-endpoints");
  await expect(page.getByRole("button", { name: "上一页", exact: true })).toBeDisabled();
  await page.getByRole("textbox", { name: "搜索模型" }).fill("no-such-e2e-model");
  await expect(page.getByText("没有找到模型。请先在模型提供商中配置连接，再添加部署。", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "下一页", exact: true })).toBeDisabled();
  await page.getByRole("textbox", { name: "搜索模型" }).fill("gpt-4o-mini");
  await expect(page.getByRole("heading", { name: "gpt-4o-mini", exact: true })).toBeVisible();
  await page.getByRole("button", { name: t("pages.models.add"), exact: true }).click();
  const form = page.locator("form").filter({ has: page.getByLabel("模型提供商 *") });
  await expect(form.getByRole("button", { name: "添加模型", exact: true })).toBeDisabled();
  await expect(form.getByLabel("上游模型 *")).toBeDisabled();
  await form.getByRole("button", { name: "取消", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "搜索模型" })).toBeVisible();
  await guard.assertOk();
});
}
/** 用途：注册独立场景「key create cancellation leaves no saved resource」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC039() {


test("key create cancellation leaves no saved resource", async ({ page }) => {
  await loginAdmin(page);
  const alias = "e2e-cancelled-key";
  await page.getByTestId("create-key-button").click();
  await page.getByLabel(t("Key Name")).fill(alias);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const response = await page.request.get(GATEWAY + "/key/list", {
    headers: { Authorization: "Bearer " + await sessionBearer(page) },
  });
  expect(response.ok()).toBeTruthy();
  expect(await response.text()).not.toContain(alias);
});
}
/** 用途：注册独立场景「account menu opens and logout invalidates the browser session」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC040() {


test("account menu opens and logout invalidates the browser session", async ({ page }) => {
  await loginAdmin(page);
  await page.getByRole("button", { name: /^Account menu/ }).click();
  await expect(page.getByTestId("sidebar-account-menu-panel")).toBeVisible();
  await page.getByRole("button", { name: t("Logout"), exact: true }).click();
  await expect(page).toHaveURL(/\/login/);
  // 此处明确期望未登录跳转，不能使用要求业务页面成功的 stableGoto。
  await page.goto(uiPath("/api-keys"));
  await expect(page).toHaveURL(/\/login/);
});
}
/** 用途：注册独立场景「retired API routes reject writes and remain absent from navigation」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC041() {


test("retired API routes reject writes and remain absent from navigation", async ({ page }) => {
  await loginAdmin(page);
  const session = await sessionBearer(page);
  for (const route of ["/v1/agents", "/v1/memory", "/v1/workflows/runs"]) {
    const response = await page.request.post(GATEWAY + route, {
      headers: { Authorization: "Bearer " + session }, data: { name: "retired-e2e" },
    });
    expect(response.status(), route).toBe(410);
  }
  for (const route of ["agents", "skills", "memory", "workflows", "mcp-servers", "access-groups"]) {
    await expect(page.locator('a[href^="/ui/' + route + '"]')).toHaveCount(0);
  }
});
}
