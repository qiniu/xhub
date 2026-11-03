import { expect, test as base } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { matchesGatewayResponse } from "./gateway-response.cjs";
import type { Locale } from "../src/i18n/translate";

type Resources = { headers: Record<string, string>; name: string };

/** 预算/IAM场景独立清理夹具；参数由Playwright注入，返回唯一名称和登录头。
 * 页面超时或失败后仍查回以唯一名称创建的资源；按预算、团队、组织顺序清理，任一失败阻止通过。
 * 仅清理本场景名称；每类失败后继续其他类别，私有schema由运行脚本最终回收。
 */
export const test = base.extend<{ budgetResources: Resources }>({
  budgetResources: [async ({ playwright }, runTest) => {
    const api = await playwright.request.newContext({ timeout: 10_000 });
    const state: Resources = { headers: {}, name: `budget-iam-${Date.now()}-${Math.random().toString(36).slice(2)}` };
    try { await runTest(state); }
    finally {
      const failures: unknown[] = [];
      try {
        if (state.headers.Authorization) {
          for (const resource of [
            { list: "/budget/list", alias: "budget_id", id: "budget_id", remove: "/budget/delete", method: "POST" },
            { list: "/team/list", alias: "team_alias", id: "team_id", remove: "/team/delete", method: "POST" },
            { list: "/organization/list", alias: "organization_alias", id: "organization_id", remove: "/organization/delete", method: "DELETE" },
          ]) {
            try {
              const response = await api.get(GATEWAY + resource.list, { headers: state.headers });
              expect(response.status(), "清理前列表必须可读 " + resource.list).toBe(200);
              const body = await response.json();
              const rows = Array.isArray(body) ? body : body.data;
              expect(Array.isArray(rows), "清理响应必须是资源列表").toBe(true);
              for (const row of rows.filter((item: Record<string, unknown>) => item[resource.alias] === state.name)) {
                try {
                  const deleted = await api.fetch(GATEWAY + resource.remove, { method: resource.method, headers: state.headers, data: { [resource.id]: row[resource.id] } });
                  expect(deleted.status(), "资源清理必须成功 " + resource.remove).toBe(200);
                } catch (error) { failures.push(error); }
              }
            } catch (error) { failures.push(error); }
          }
        }
        expect(failures, "所有资源清理均须成功").toEqual([]);
      } finally { await api.dispose(); }
    }
  }, { timeout: 30_000 }],
});

/** 注册真实预算页面与IAM兼容边界；locale为控制台语言，返回无。
 * 页面创建/编辑/刷新预算，真实组织团队主接口回读，旧操作501且状态不变，模型实际返回e2e-ok。
 * 原预算页面中文控件保持现状，本次不修改UI文案；原始中英文输入不变，独立夹具负责成功/失败清理。
 */
export function registerBudgetIAM(locale: Locale) {
  test("budget configuration and IAM boundary " + locale, async ({ page, budgetResources: resources }) => {
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = resources.headers = { Authorization: "Bearer " + await sessionBearer(page) };
    await stableGoto(page, "/budgets");
    await page.getByLabel("名称", { exact: true }).fill(resources.name);
    const created = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/budget/new", "POST"));
    await page.getByRole("button", { name: "创建", exact: true }).click();
    const response = await created;
    expect(response.status(), await response.text()).toBe(200);
    expect(await response.json()).toMatchObject({ budget_id: resources.name, max_budget: null });
    await expect(page.getByTestId("column-list")).toContainText(resources.name);
    // 更新沿用页面现有行为，budget_duration只作为配置保存，不意味着真实定时刷新执行。
    await page.getByLabel("名称", { exact: true }).fill("Original 用户 input");
    const updated = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/budget/update", "POST"));
    await page.getByRole("button", { name: "更新", exact: true }).click();
    expect((await updated).status()).toBe(200);
    await page.reload();
    await expect(page.getByTestId("column-list")).toContainText("Original 用户 input");
    const saved = await page.request.post(GATEWAY + "/budget/info", { headers, data: { budget_id: resources.name } });
    expect(saved.status()).toBe(200);
    expect(await saved.json()).toMatchObject({ max_budget: 1, budget_duration: "Original 用户 input" });
    const invalid = await page.request.post(GATEWAY + "/budget/update", { headers: { ...headers, "Content-Type": "application/json" }, data: '{"budget_id":' });
    expect(invalid.status()).toBe(400);
    const zero = await page.request.post(GATEWAY + "/budget/update", { headers, data: { budget_id: resources.name, max_budget: 0 } });
    expect(zero.status()).toBe(200);
    expect(await zero.json()).toMatchObject({ max_budget: 0, budget_duration: "Original 用户 input" });
    const orgResponse = await page.request.post(GATEWAY + "/organization/new", { headers, data: { organization_alias: resources.name } });
    expect(orgResponse.status(), await orgResponse.text()).toBe(200);
    const org = await orgResponse.json();
    const teamResponse = await page.request.post(GATEWAY + "/team/new", { headers, data: { organization_id: org.organization_id, team_alias: resources.name } });
    expect(teamResponse.status(), await teamResponse.text()).toBe(200);
    const team = await teamResponse.json();
    for (const operation of [
      { method: "POST", path: "/team/block" },
      { method: "PATCH", path: "/organization/member_update" },
    ]) {
      const result = await page.request.fetch(GATEWAY + operation.path, { method: operation.method, headers, data: { team_id: team.team_id, organization_id: org.organization_id, max_budget: 0 } });
      expect(result.status()).toBe(501);
      expect(await result.json()).toMatchObject({ error: { type: "unsupported_iam_operation" } });
    }
    const orgRead = await page.request.get(GATEWAY + "/organization/info?organization_id=" + org.organization_id, { headers });
    expect(orgRead.status()).toBe(200);
    expect(await orgRead.json()).toMatchObject({ organization_alias: resources.name, max_budget: org.max_budget });
    const teamRead = await page.request.get(GATEWAY + "/team/info?team_id=" + team.team_id, { headers });
    expect(teamRead.status()).toBe(200);
    expect((await teamRead.json()).team_info).toMatchObject({ team_alias: resources.name, max_budget: team.max_budget });
    const chat = await page.request.post(GATEWAY + "/v1/chat/completions", { headers, data: {
      model: "gpt-4o-mini", messages: [{ role: "user", content: "Reply with a short greeting." }],
    } });
    expect(chat.status(), await chat.text()).toBe(200);
    expect((await chat.json()).choices[0].message.content).toBe("e2e-ok");
    const deleted = await page.request.post(GATEWAY + "/budget/delete", { headers, data: { budget_id: resources.name } });
    expect(deleted.status()).toBe(200);
    expect((await page.request.post(GATEWAY + "/budget/info", { headers, data: { budget_id: resources.name } })).status()).toBe(404);
    await page.reload();
    await expect(page.getByTestId("column-list")).not.toContainText(resources.name);
  });
}
