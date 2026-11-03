import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

import { gotoLogin, loginAdmin, uiPath, watchGateway, GATEWAY, sessionBearer, stableGoto } from "./helpers";

import { recordPage } from "./report";

import { translate } from "../src/i18n/translate";


const t = (key: string) => translate("zh-CN", key);
/** 用途：注册独立场景「empty password shows validation」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC054() {


test("empty password shows validation", async ({ page }) => {
    await gotoLogin(page);
    const dir = process.env.E2E_SHOTS;
    if (dir) await page.screenshot({ path: `${dir}/ui-login.png` });
    await page.getByPlaceholder(t("login.usernamePlaceholder")).fill("admin");
    await page.getByRole("button", { name: t("login.submit"), exact: true }).click();
    await expect(page.getByText(t("login.passwordRequired"))).toBeVisible();
    await expect(page).toHaveURL(/\/login/);
    recordPage("/login", "pass");
  });
}
/** 用途：注册独立场景「wrong password shows error」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC055() {


test("wrong password shows error", async ({ page }) => {
    const guard = watchGateway(page);
    await gotoLogin(page);
    await page.getByPlaceholder(t("login.usernamePlaceholder")).fill("admin");
    await page.getByPlaceholder(t("login.passwordPlaceholder")).fill("not-the-master");
    await page.getByRole("button", { name: t("login.submit"), exact: true }).click();
    await expect(page.getByText(/Invalid credentials|login failed|UI_USERNAME/i)).toBeVisible();
    await expect(page).toHaveURL(/\/login/);
    await guard.assertOk();
  });
}
/** 用途：注册独立场景「admin + master key lands on Virtual Keys」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC056() {


test("admin + master key lands on Virtual Keys", async ({ page }) => {
    await loginAdmin(page);
    const dir = process.env.E2E_SHOTS;
    if (dir) await page.screenshot({ path: `${dir}/ui-keys.png` });
    await expect(page.getByText(t("nav.groups.mine"), { exact: true }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: t("pages.apiKeys.create") })).toBeVisible();
  });
}
/** 用途：注册独立场景「unauthenticated dashboard redirects to login」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC057() {


test("unauthenticated dashboard redirects to login", async ({ page }) => {
    await page.goto(uiPath("/api-keys"));
    await expect(page).toHaveURL(/\/login/, { timeout: 15_000 });
    await expect(page.getByRole("heading", { name: t("login.title") })).toBeVisible();
  });
}
/** 用途：注册独立场景「password login does not offer SSO」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC058() {


test("password login does not offer SSO", async ({ page }) => {
    await gotoLogin(page);
    await expect(page.getByRole("button", { name: t("login.submit") })).toBeVisible();
    await expect(page.getByRole("button", { name: t("login.sso") })).toHaveCount(0);
  });
}
/** 用途：注册独立场景「配置的 SSO 在登录页按启用状态展示并支持语言切换」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC053() {


test("配置的 SSO 在登录页按启用状态展示并支持语言切换", async ({ page, browser }) => {
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + await sessionBearer(page) };
  // 启用 SSO 要求目录身份锚点；使用本轮私有身份，不能以空组织代替已就绪的企业集成。
  const fixture = JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8"));
  const created = await page.request.post(fixture.control + "/identity");
  expect(created.status(), await created.text()).toBe(200);
  const org = await created.json();
  const anonymous = await browser.newContext({ baseURL: new URL(page.url()).origin });
  const visitor = await anonymous.newPage();
  const name = "研发 Login E2E";
  let connectionID = "";
  try {
    await stableGoto(page, "/admin-panel");
    await page.getByRole("tab", { name: "单点登录设置" }).click();
    await page.getByRole("button", { name: "配置单点登录", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "配置单点登录" });
    await dialog.getByLabel("组织 ID").selectOption(org.organization_id);
    await dialog.getByLabel("单点登录名称").fill(name);
    await dialog.getByLabel("XHub 公开访问地址").fill("https://console.example.com");
    await dialog.getByRole("button", { name: "生成回调和返回地址" }).click();
    await dialog.getByLabel("OIDC 签发者").fill(fixture.issuer);
    await dialog.getByLabel("客户端 ID", { exact: true }).fill("xhub-browser");
    await dialog.getByLabel("客户端密钥").fill("login-fixture-secret");
    await dialog.getByRole("button", { name: "保存集成配置" }).click();
    await expect(dialog).not.toBeVisible();
    const rows = await page.request.get(GATEWAY + "/admin/enterprise/sso", { headers });
    expect(rows.status()).toBe(200);
    connectionID = (await rows.json()).find((row: { name: string }) => row.name === name).connection_id;
    await gotoLogin(visitor);
    const chineseName = translate("zh-CN", "enterprise.login", { name });
    await expect(visitor.getByRole("link", { name: chineseName, exact: true })).toHaveCount(0);
    await page.getByRole("switch", { name: `启用 ${name} 的单点登录` }).click();
    await expect(page.getByRole("switch", { name: `启用 ${name} 的单点登录` })).toBeChecked();
    await visitor.reload();
    const link = visitor.getByRole("link", { name: chineseName, exact: true });
    const url = `/gw/auth/enterprise/${connectionID}/start`;
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute("href", url);
    const options = await visitor.request.get(GATEWAY + "/auth/enterprise/options");
    expect(options.status()).toBe(200);
    expect(await options.json()).toContainEqual({ id: connectionID, name, url });
    expect(await options.text()).not.toContain("login-fixture-secret");
    await visitor.getByRole("button", { name: "English", exact: true }).click();
    const englishName = translate("en", "enterprise.login", { name });
    await expect(visitor.getByRole("link", { name: englishName, exact: true })).toHaveAttribute("href", url);
    await expect(visitor.getByRole("button", { name: translate("en", "login.submit"), exact: true })).toBeVisible();
    await page.getByRole("switch", { name: `启用 ${name} 的单点登录` }).click();
    await expect(page.getByRole("switch", { name: `启用 ${name} 的单点登录` })).not.toBeChecked();
    await visitor.reload();
    await expect(visitor.getByRole("link", { name: englishName, exact: true })).toHaveCount(0);
    const disabled = await visitor.request.get(GATEWAY + "/auth/enterprise/options");
    expect(disabled.status()).toBe(200);
    expect((await disabled.json()).some((row: { id: string }) => row.id === connectionID)).toBe(false);
  } finally {
    await anonymous.close();
    if (connectionID) {
      const removed = await page.request.delete(GATEWAY + "/admin/enterprise/sso/" + connectionID, { headers });
      expect(removed.status(), await removed.text()).toBe(204);
    }
    expect((await page.request.delete(GATEWAY + "/admin/directory/connections/" + org.connection_id, { headers })).status()).toBe(204);
    const removedOrg = await page.request.post(fixture.control + "/identity/cleanup?organization_id=" + org.organization_id);
    expect(removedOrg.status(), await removedOrg.text()).toBe(200);
  }
});
}
