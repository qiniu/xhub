/** 目的：独立验证 /logs 的页面与读接口；前置隔离网关与管理员，异常独立报告；上下文和 schema 由运行器清理。 */
import { discoverPages } from "../../routes";
import { registerPageScenario } from "../../page-scenario";
registerPageScenario(discoverPages().find(page => page.route === '/logs')!, '/logs');

import { expect, test } from "@playwright/test";
import { loginAdmin, stableGoto, watchGateway } from "../../helpers";
import { translate } from "../../../src/i18n/translate";

/** 目的：验证请求日志状态和缓存选项不在回调内调用 Hook；前置隔离后台和管理员；中英文选择保留原始参数并成功查询真实后台；运行器清理上下文和隔离 schema。 */
test("请求日志 Hook 回归：中英文状态与缓存筛选", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  await stableGoto(page, "/logs");
  await page.getByRole("button", { name: "English", exact: true }).click();
  await page.getByTestId("datatable-filters-trigger").click();
  await page.getByText(translate("en", "All Statuses"), { exact: true }).click();
  await page.getByRole("option", { name: translate("en", "Success"), exact: true }).click();
  await page.getByText(translate("en", "All Requests"), { exact: true }).click();
  await page.getByRole("option", { name: translate("en", "Cache Miss"), exact: true }).click();
  const read = page.waitForResponse(res => res.url().includes("/spend/logs/ui?") && new URL(res.url()).searchParams.get("cache_hit_filter") === "miss");
  // 共享抽屉仍通过稳定测试标识提交；本用例聚焦日志选项及后台协议。
  await page.getByTestId("filter-drawer-apply").click();
  const response = await read;
  expect(response.status()).toBe(200);
  expect(new URL(response.url()).searchParams.get("status_filter")).toBe("success");
  await page.getByRole("button", { name: translate("en", "language.zhCN"), exact: true }).click();
  await page.getByTestId("datatable-filters-trigger").click();
  await expect(page.getByText(translate("zh-CN", "Success"), { exact: true }).last()).toBeVisible();
  await expect(page.getByText(translate("zh-CN", "Cache Miss"), { exact: true }).last()).toBeVisible();
  await guard.assertOk();
});
