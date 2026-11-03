import { expect, type Page } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY } from "./helpers";

/** 在真实管理员表单设置刷新并故障恢复；连接已启用，访客已发起 SAML 登录但还未提交断言。
 * 验证开关默认关闭、后台下载失败不覆盖 XML、成功刷新不改变 revision；不创建额外数据，外层登录流程清理连接。
 */
export async function exerciseSAMLRefresh(
  page: Page,
  options: { locale: Locale; name: string; id: string; source: string; headers: Record<string, string> },
) {
  const tr = (key: string) => translate(options.locale, key);
  const base = GATEWAY + "/admin/enterprise/sso/" + options.id;
  const beforeResponse = await page.request.get(base, { headers: options.headers });
  expect(beforeResponse.status()).toBe(200);
  const before = await beforeResponse.json();
  await page
    .getByRole("button", {
      name: translate(options.locale, "enterprise.configureName", { name: options.name }),
      exact: true,
    })
    .click();
  const panel = page.getByRole("region", { name: tr("samlRefresh.title") });
  await expect(panel.getByRole("switch", { name: tr("samlRefresh.enabled") })).not.toBeChecked();
  await panel.getByLabel(tr("samlRefresh.source")).fill(options.source + "/fail");
  await panel.getByLabel(tr("samlRefresh.interval")).fill("6");
  await panel.getByRole("switch", { name: tr("samlRefresh.enabled") }).check();
  await panel.getByRole("button", { name: tr("samlRefresh.save") }).click();
  await expect(panel.getByText(tr("samlRefresh.saved"), { exact: true })).toBeVisible();
  const failed = page.waitForResponse((r) => r.url().endsWith("/refresh") && r.request().method() === "POST");
  await panel.getByRole("button", { name: tr("samlRefresh.now") }).click();
  expect((await failed).status()).toBe(502);
  await expect(panel.getByText(tr("samlRefresh.errors.fetch_failed"), { exact: true })).toBeVisible();
  const afterFailure = await (await page.request.get(base, { headers: options.headers })).json();
  expect(afterFailure.idp_metadata).toBe(before.idp_metadata);
  await panel.getByLabel(tr("samlRefresh.source")).fill(options.source);
  await panel.getByRole("button", { name: tr("samlRefresh.save") }).click();
  await expect(panel.getByText(tr("samlRefresh.saved"), { exact: true })).toBeVisible();
  const refreshed = page.waitForResponse((r) => r.url().endsWith("/refresh") && r.request().method() === "POST");
  await panel.getByRole("button", { name: tr("samlRefresh.now") }).click();
  const response = await refreshed;
  expect(response.status(), await response.text()).toBe(200);
  const state = await response.json();
  expect(state.last_success).toBeGreaterThan(0);
  expect(state.expires_at).toBeGreaterThan(Math.floor(Date.now() / 1000));
  expect(state.error_code).toBe("");
  await expect(panel.getByText(tr("samlRefresh.refreshed"), { exact: true })).toBeVisible();
  const after = await (await page.request.get(base, { headers: options.headers })).json();
  expect(after.revision).toBe(before.revision);
  await panel.getByRole("switch", { name: tr("samlRefresh.enabled") }).uncheck();
  await panel.getByRole("button", { name: tr("samlRefresh.save") }).click();
  await expect(panel.getByText(tr("samlRefresh.saved"), { exact: true })).toBeVisible();
  await panel.getByRole("button", { name: tr("samlRefresh.load") }).click();
  await expect(panel.getByRole("switch", { name: tr("samlRefresh.enabled") })).not.toBeChecked();
  await page.keyboard.press("Escape");
}
