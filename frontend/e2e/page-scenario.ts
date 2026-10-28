import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, loginAdmin, t, uiPath, watchGateway } from "./helpers";
import { recordPage } from "./report";
import { discoverPages } from "./routes";
import { requirePageSuccess } from "./assertions";

/** 用途：从已登录控制台 cookie 提取会话 bearer；参数为当前页面；返回会话 key，缺少 cookie 或非法 JWT 时抛错；供真实后台读接口使用，无写副作用。 */
async function sessionBearer(page: Page): Promise<string> {
  const cookies = await page.context().cookies();
  const token = cookies.find((c) => c.name === "token")?.value;
  const payload = JSON.parse(Buffer.from(token!.split(".")[1], "base64url").toString());
  return payload.key as string;
}

/** 用途：确认页面没有进入统一错误边界；参数为页面与路由标签；返回无；供全路由巡检调用，只读 DOM。 */
async function noDashboardError(page: Page, route: string) {
  await expect(page.getByText("This page couldn’t load"), route).toHaveCount(0);
  await expect(page.getByText(/Dashboard error:/), route).toHaveCount(0);
}

/** 用途：注册独立业务路径巡检；参数为页面与实际路径，无返回。
 * 每个路径独立登录并验证 HTTP/DOM/API，异常独立报告；上下文和隔离 schema 由运行器清理。 */
export function registerPageScenario(item: ReturnType<typeof discoverPages>[number], businessPath: string) {
 test("页面可用 " + businessPath, async ({ page }) => {
  test.setTimeout(300_000);
  const guard = watchGateway(page);
  const errors: string[] = [];
  const failedReads: string[] = [];
  let usageActivitySettled = false;
  const observed: Record<string, any> = {};
  page.on("pageerror", (err) => errors.push(`${page.url()}: ${err.message}`));
  page.on("response", async (res) => {
    const url = res.url();
    if (url.includes("/user/daily/activity") && res.status() < 400) usageActivitySettled = true;
    // 空态必须服从页面实际读到的 API；共享管理员已有用量时改验真实表格、图表或统计卡。
    if (res.status() < 400) {
      const path = new URL(url).pathname;
      if (path === "/user/daily/activity") observed.usage = await res.json().catch(() => undefined);
      if (path === "/spend/logs/ui") observed.logs = await res.json().catch(() => undefined);
      if (path === "/guardrails/usage/overview") observed.guardrails = await res.json().catch(() => undefined);
      if (path === "/user/daily/activity/aggregated") observed.chatUsage = await res.json().catch(() => undefined);
    }
    if (!/\/user\/daily\/activity|\/guardrails\/usage\/overview|\/key\/list/.test(url)) return;
    if (res.status() >= 400) failedReads.push(`${res.status()} ${res.request().method()} ${url}`);
  });
  await loginAdmin(page);
  const authHeaders = { Authorization: "Bearer " + (await sessionBearer(page)) };

    if (item.route === "/usage") usageActivitySettled = false;
    const response = await page.goto(businessPath.startsWith("/docs") ? businessPath : uiPath(businessPath));
    requirePageSuccess(response?.status(), businessPath, page.url());
    await noDashboardError(page, item.route);
    await expect(page.getByRole("heading", {name:/^(404|page not found|页面不存在)$/i})).toHaveCount(0);
    await expect(
      page
        .getByRole("heading")
        .or(page.getByRole("table"))
        .or(page.getByRole("tab"))
        .or(page.getByText(t("pages.costTracking.calculator")))
        .first(),
      item.route,
    ).toBeVisible({ timeout: 15_000 });
    if (!item.removed && item.route !== "/login") {
      await expect(page, item.route).not.toHaveURL(/\/login/);
    }
    if (item.route === "/usage") {
      const usageResponse = await page.request.get(GATEWAY + "/user/daily/activity", { headers: authHeaders });
      expect(usageResponse.status(), await usageResponse.text()).toBe(200);
      observed.usage = await usageResponse.json();
      await expect.poll(() => usageActivitySettled, { timeout: 15_000 }).toBe(true);
      const settled = page.getByTestId("daily-spend-settled");
      await expect(settled).toBeVisible({ timeout: 15_000 });
      await expect(page.getByText(t("Loading chart data..."))).toHaveCount(0);
      const requests = Number(observed.usage?.metadata?.total_api_requests ?? 0);
      if (requests === 0) {
        await expect(settled.getByText(t("No data"))).toBeVisible();
        await expect(settled.locator("svg")).toHaveCount(0);
      } else {
        await expect(settled.locator("svg").first()).toBeVisible();
        await expect(settled.getByText(t("No data"))).toHaveCount(0);
      }
    }
    if (item.route === "/old-usage") {
      await expect(page.getByTestId("old-usage-body")).toContainText(/"spend"|用量读取失败/, { timeout: 15_000 });
    }
    if (item.route === "/logs") {
      const logsResponse = await page.request.get(GATEWAY + "/spend/logs/ui?page=1&page_size=1", { headers: authHeaders });
      expect(logsResponse.status(), await logsResponse.text()).toBe(200);
      observed.logs = await logsResponse.json();
      await expect.poll(() => observed.logs !== undefined, { timeout: 15_000 }).toBe(true);
      const rows = observed.logs?.data ?? observed.logs ?? [];
      if (rows.length === 0) await expect(page.getByText(t("No requests yet")).first()).toBeVisible({ timeout: 15_000 });
      else await expect(page.getByRole("table")).toBeVisible({ timeout: 15_000 });
    }
    if (item.route === "/guardrails-monitor") {
      const guardrailsResponse = await page.request.get(GATEWAY + "/guardrails/usage/overview", { headers: authHeaders });
      expect(guardrailsResponse.status(), await guardrailsResponse.text()).toBe(200);
      observed.guardrails = await guardrailsResponse.json();
      await expect.poll(() => observed.guardrails !== undefined, { timeout: 15_000 }).toBe(true);
      const rows = observed.guardrails?.rows ?? [];
      if (rows.length === 0) await expect(page.getByText("No data for this period").first()).toBeVisible({ timeout: 15_000 });
      else await expect(page.getByRole("table")).toBeVisible({ timeout: 15_000 });
      await expect(page.getByText(t("Failed to load data. Try again."))).toHaveCount(0);
    }
    if (item.route === "/chat/api-keys") {
      await expect(
        page
          .getByText(t("No keys found"))
          .or(page.getByRole("cell", { name: /^\$/ }))
          .first(),
      ).toBeVisible({ timeout: 15_000 });
    }
    if (item.route === "/chat/logs") {
      await expect(
        page.getByText(t("No logs for this period")).or(page.getByRole("table")).first(),
      ).toBeVisible({ timeout: 15_000 });
    }
    if (item.route === "/chat/usage") {
      const chatUsageResponse = await page.request.get(GATEWAY + "/user/daily/activity/aggregated", { headers: authHeaders });
      expect(chatUsageResponse.status(), await chatUsageResponse.text()).toBe(200);
      observed.chatUsage = await chatUsageResponse.json();
      await expect.poll(() => observed.chatUsage !== undefined, { timeout: 15_000 }).toBe(true);
      const requests = Number(observed.chatUsage?.metadata?.total_api_requests ?? 0);
      if (requests === 0) await expect(page.getByText(t("No usage data for this period"))).toBeVisible({ timeout: 15_000 });
      else {
        await expect(page.getByText(t("API Requests"), { exact: true })).toBeVisible({ timeout: 15_000 });
        await expect(page.getByText(t("No usage data for this period"))).toHaveCount(0);
      }
    }
    requirePageSuccess(response?.status(), businessPath, page.url());
  expect(errors, errors.join("\n")).toEqual([]);
  expect(failedReads, failedReads.join("\n")).toEqual([]);
  await guard.assertOk();
  recordPage(item.route, "pass", businessPath);
 });
}

