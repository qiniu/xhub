/** 目的：独立验证 /audit-logs 的页面与读接口；前置隔离网关与管理员，异常独立报告；上下文和 schema 由运行器清理。 */
import { discoverPages } from "../../routes";
import { registerPageScenario } from "../../page-scenario";
registerPageScenario(discoverPages().find(page => page.route === '/audit-logs')!, '/audit-logs');

import { expect, test } from "@playwright/test";
import { loginAdmin, stableGoto, watchGateway } from "../../helpers";
import { translate } from "../../../src/i18n/translate";

/** 目的：验证审计页真实加载、语言切换、动作筛选和无匹配搜索；前置隔离后台与管理员会话；结果为成功读接口、正确筛选参数和本地化状态；仅写隔离夹具，运行器清理 schema 与上下文。 */
test("审计日志 Hook 回归：中英文筛选与真实后台查询", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  const initialRead = page.waitForResponse(res => res.url().includes("/audit/logs?") && res.request().method() === "GET");
  await stableGoto(page, "/audit-logs");
  expect((await initialRead).status()).toBe(200);
  await expect(page.getByRole("heading", { name: translate("zh-CN", "pages.logs.audit"), exact: true })).toBeVisible();
  await page.getByRole("button", { name: "English", exact: true }).click();
  await expect(page.getByRole("heading", { name: translate("en", "pages.logs.audit"), exact: true })).toBeVisible();
  await page.getByTestId("datatable-filters-trigger").click();
  await page.getByRole("combobox").first().click();
  await page.getByRole("option", { name: translate("en", "audit.filters.created"), exact: true }).click();
  const filteredRead = page.waitForResponse(res => res.url().includes("/audit/logs?") && new URL(res.url()).searchParams.get("action") === "created");
  await page.getByRole("button", { name: translate("en", "common.applyFilters"), exact: true }).click();
  const filteredResponse = await filteredRead;
  expect(filteredResponse.status()).toBe(200);
  const filtered = await filteredResponse.json();
  expect(filtered.total).toBeGreaterThan(0);
  expect(filtered.audit_logs.every((row: { action: string }) => /(^created$|\.create$)/.test(row.action))).toBe(true);
  await expect(page.getByTestId("filter-chip-action")).toContainText(translate("en", "audit.filters.created"));
  const query = "用户 audit-hook-no-match";
  const emptyRead = page.waitForResponse(res => res.url().includes("/audit/logs?") && new URL(res.url()).searchParams.get("search") === query);
  await page.getByPlaceholder(translate("en", "audit.search")).fill(query);
  expect((await (await emptyRead).json()).total).toBe(0);
  await expect(page.getByText(translate("en", "No matching audit logs"), { exact: true })).toBeVisible();
  await page.getByRole("button", { name: translate("en", "language.zhCN"), exact: true }).click();
  await expect(page.getByPlaceholder(translate("zh-CN", "audit.search"))).toHaveValue(query);
  await expect(page.getByTestId("filter-chip-action")).toContainText(translate("zh-CN", "audit.filters.created"));
  await expect(page.getByText(translate("zh-CN", "No matching audit logs"), { exact: true })).toBeVisible();
  await page.getByTestId("datatable-filters-trigger").click();
  await expect(page.getByRole("combobox").first()).toContainText(translate("zh-CN", "audit.filters.created"));
  await expect(page.getByRole("button", { name: translate("zh-CN", "common.applyFilters"), exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: translate("zh-CN", "Reset"), exact: true })).toBeVisible();
  await guard.assertOk();
});
