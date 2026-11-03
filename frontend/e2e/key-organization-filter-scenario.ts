import { randomUUID } from "node:crypto";
import { expect, test } from "@playwright/test";
import { GATEWAY, login, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 按 locale 注册真实组织筛选闭环，供双语独立场景调用，无返回值。
 * 前置隔离 PG、网关和本地模型；创建两个组织的用户/Key，验证筛选、分页、重载与推理，finally 清理全部夹具。 */
export function registerKeyOrganizationFilter(locale: "en" | "zh-CN") {
  test("key organization filter and permission intersection " + locale, async ({ page }) => {
    test.setTimeout(120000);
    page.setDefaultTimeout(15000);
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const prefix = "OrgFilter 用户 " + locale + "-" + randomUUID();
    const password = "org-filter-test-password";
    const organizations: string[] = [], teams: string[] = [], users: string[] = [];
    const keys: { id: string; token: string; alias: string }[] = [];
    const emails = [randomUUID() + "@example.test", randomUUID() + "@example.test"];
    /** 通过管理员真实接口创建本场景资源；path/data 为请求，返回成功 JSON；失败立即阻断，finally 按记录ID清理。 */
    const create = async (path: string, data: unknown) => {
      const response = await page.request.post(GATEWAY + path, { headers, data });
      expect(response.status(), path + ": " + await response.text()).toBe(200);
      return response.json();
    };
    /** 等待浏览器真实列表回复并检查过滤后的数量；参数条件用于排除无关请求，不拦截或伪造响应。 */
    const listResponse = (params: Record<string, string>) => page.waitForResponse(response => {
      const url = new URL(response.url());
      return url.pathname === "/key/list" && Object.entries(params).every(([key, value]) => url.searchParams.get(key) === value);
    });
    try {
      for (let i = 0; i < 2; i++) {
        const org = await create("/organization/new", { organization_alias: prefix + i });
        organizations.push(org.organization_id);
        const team = await create("/team/new", { organization_id: org.organization_id, team_alias: prefix + i });
        teams.push(team.team_id);
        const user = await create("/user/new", { user_email: emails[i], password, team_id: team.team_id, max_budget: 500 });
        users.push(user.user_id);
      }
      // 第二组织的管理成员身份仅用于显示筛选选项，不得使个人列表出现另一用户的 Key。
      await create("/organization/member_add", { organization_id: organizations[1], member: { user_email: emails[0], role: "org_admin" } });
      for (const i of [0, 0, 1]) {
        const alias = prefix + " key " + keys.length;
        const key = await create("/key/generate", { user_id: users[i], key_alias: alias, max_budget: 10 });
        keys.push({ id: key.token_id, token: key.key, alias });
      }
      await login(page, emails[0], password, locale);
      await expect(page.getByText(tr("nav.apiKeys"), { exact: true }).first()).toBeVisible();
      await stableGoto(page, "/api-keys?page_size=1&sort_by=key_alias&sort_order=asc");
      await expect(page.getByRole("row").filter({ hasText: keys[0].alias })).toBeVisible();
      await expect(page.getByRole("row").filter({ hasText: keys[2].alias })).toHaveCount(0);
      await expect(page.getByTestId("datatable-filters-trigger")).toContainText(tr("Filters"));
      await page.getByTestId("datatable-filters-trigger").click();
      await page.getByPlaceholder(tr("Select an organization…"), { exact: true }).fill(prefix + "0");
      await page.getByRole("option").filter({ hasText: prefix + "0" }).click();
      let pending = listResponse({ organization_id: organizations[0], page: "1" });
      await page.getByRole("button", { name: tr("common.applyFilters"), exact: true }).click();
      let response = await pending;
      expect(response.status(), await response.text()).toBe(200);
      expect(await response.json()).toMatchObject({ total_count: 2, size: 1, total_pages: 2, current_page: 1 });
      await expect(page.getByRole("row").filter({ hasText: keys[0].alias })).toBeVisible();
      await expect(page.getByTestId("pagination-range")).toHaveText(translate(locale, "consoleDirectory.pagination.range", { start: 1, end: 1, count: 2 }));
      pending = listResponse({ organization_id: organizations[0], page: "2" });
      await page.getByRole("button", { name: tr("consoleDirectory.pagination.next"), exact: true }).click();
      response = await pending;
      expect(await response.json()).toMatchObject({ total_count: 2, current_page: 2 });
      await expect(page.getByRole("row").filter({ hasText: keys[1].alias })).toBeVisible();
      await expect(page.getByRole("row").filter({ hasText: keys[0].alias })).toHaveCount(0);
      await page.getByTestId("datatable-filters-trigger").click();
      await page.getByPlaceholder(tr("Select an organization…"), { exact: true }).fill(prefix + "1");
      await page.getByRole("option").filter({ hasText: prefix + "1" }).click();
      pending = listResponse({ organization_id: organizations[1], page: "1" });
      await page.getByRole("button", { name: tr("common.applyFilters"), exact: true }).click();
      response = await pending;
      expect(response.status(), await response.text()).toBe(200);
      expect(await response.json()).toMatchObject({ keys: [], total_count: 0, total_pages: 1, current_page: 1 });
      await expect(page.getByText(tr("No keys found"), { exact: true })).toBeVisible();
      pending = listResponse({ organization_id: organizations[1] });
      await page.reload();
      expect((await pending).status()).toBe(200);
      await expect(page.getByText(tr("No keys found"), { exact: true })).toBeVisible();
      await page.getByTestId("datatable-filters-trigger").click();
      await page.getByRole("button", { name: tr("Reset"), exact: true }).click();
      await page.getByRole("button", { name: tr("common.applyFilters"), exact: true }).click();
      await expect(page.getByRole("row").filter({ hasText: keys[0].alias })).toBeVisible();
      await page.getByTestId("datatable-filters-trigger").click();
      await page.getByPlaceholder(tr("Select an organization…"), { exact: true }).fill(prefix + "0");
      await page.getByRole("option").filter({ hasText: prefix + "0" }).click();
      await page.getByPlaceholder(tr("Enter Key ID…"), { exact: true }).fill(keys[1].id);
      pending = listResponse({ organization_id: organizations[0], key_hash: keys[1].id });
      await page.getByRole("button", { name: tr("common.applyFilters"), exact: true }).click();
      response = await pending;
      expect(await response.json()).toMatchObject({ total_count: 1, total_pages: 1 });
      await expect(page.getByRole("row").filter({ hasText: keys[1].alias })).toBeVisible();
      await expect(page.getByRole("row").filter({ hasText: keys[0].alias })).toHaveCount(0);
      const detail = await page.request.get(GATEWAY + "/key/info?key=" + keys[1].id, { headers });
      expect(detail.status()).toBe(200);
      expect((await detail.json()).info).toMatchObject({ key_alias: keys[1].alias, max_budget: 10, spend: 0, blocked: false });
      const chat = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + keys[1].token },
        data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Verify key organization filtering. " + locale }] },
      });
      expect(chat.status(), await chat.text()).toBe(200);
      expect((await chat.json()).choices[0].message.content).toContain("e2e-ok");
    } finally {
      test.setTimeout(test.info().timeout + 30000);
      // 清理失败必须使测试失败；独立 schema 由运行器兜底删除，不触及线上或其他场景。
      for (const key of keys) {
        await create("/key/delete", { keys: [key.id] });
        expect((await page.request.get(GATEWAY + "/key/info?key=" + key.id, { headers })).status()).toBe(404);
      }
      for (const user_id of users) await create("/user/delete", { user_id });
      for (const team_id of teams) await create("/team/delete", { team_id });
      for (const organization_id of organizations) {
        const deleted = await page.request.delete(GATEWAY + "/organization/delete", { headers, data: { organization_id } });
        expect(deleted.status(), await deleted.text()).toBe(200);
      }
    }
  });
}
