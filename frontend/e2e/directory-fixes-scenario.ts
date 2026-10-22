import { test, expect } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 注册目录修复双语真实流程：创建隔离用户与团队，验证删除分页、搜索、语言切换及取消；finally删除测试资源。 */
export function registerDirectoryFixesScenario(locale: "en" | "zh-CN") {
  test(`directory search pagination and translations ${locale}`, async ({ page }) => {
    test.setTimeout(180000);
    page.setDefaultTimeout(20000);
    await loginAdmin(page);
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    const tr = (key: string, vars?: Record<string, string | number>) => translate(locale, key, vars);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const prefix = `directory-${locale}-${Date.now()}`;
    const users: string[] = [],
      teams: string[] = [];
    let org = "";
    try {
      for (let i = 0; i < 26; i++) {
        const result = await page.request.post(GATEWAY + "/user/new", {
          headers,
          data: {
            user_email: `${prefix}-${i}@example.invalid`,
            user_role: "user",
            password: "Directory-test-pass123!",
          },
        });
        expect(result.status(), await result.text()).toBe(200);
        users.push((await result.json()).user_id);
      }
      await stableGoto(page, "/users");
      await page.getByPlaceholder(tr("pages.users.searchByEmailOrId")).fill(prefix);
      await expect(page.getByTestId("pagination-range")).toHaveText(
        tr("consoleDirectory.pagination.range", { start: 1, end: 25, count: 26 }),
      );
      await page.getByRole("button", { name: tr("consoleDirectory.pagination.last") }).click();
      await expect(page.getByTestId("pagination-range")).toHaveText(
        tr("consoleDirectory.pagination.range", { start: 26, end: 26, count: 26 }),
      );
      // 在末页删除唯一行，必须自动回到真实末页，无需刷新；随后连续再删一行验证统计。
      for (const count of [25, 24]) {
        const row = page.getByRole("row").filter({ hasText: prefix }).first();
        await row.getByRole("button", { name: tr("pages.users.openUserActions") }).click();
        await page.getByRole("menuitem", { name: tr("pages.users.deleteUser"), exact: true }).click();
        const deleted = page.waitForResponse(
          (r) => r.url().endsWith("/user/delete") && r.request().method() === "POST",
        );
        await page
          .getByRole("dialog")
          .getByRole("button", { name: tr("common.delete"), exact: true })
          .click();
        const response = await deleted;
        expect(response.status()).toBe(200);
        const removed = response.request().postDataJSON().user_id;
        users.splice(users.indexOf(removed), 1);
        await expect(page.getByRole("dialog")).toBeHidden();
        await expect(page.getByTestId("pagination-range")).toHaveText(
          tr("consoleDirectory.pagination.range", { start: 1, end: count, count }),
        );
        expect((await page.request.get(GATEWAY + "/user/info?user_id=" + removed, { headers })).status()).toBe(404);
      }
      await expect(page.getByRole("button", { name: tr("consoleDirectory.pagination.next") })).toBeDisabled();
      // 模拟另一位管理员先删除同一用户；当前页面再删应收到真实404，保留原行/总数直到显式刷新。
      const staleRow = page.getByRole("row").filter({ hasText: prefix }).first();
      const staleActions = staleRow.getByRole("button", { name: tr("pages.users.openUserActions") });
      const staleID = (await staleActions.getAttribute("data-testid"))!.replace("user-actions-", "");
      expect(
        (await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: staleID } })).status(),
      ).toBe(200);
      users.splice(users.indexOf(staleID), 1);
      await staleActions.click();
      await page.getByRole("menuitem", { name: tr("pages.users.deleteUser"), exact: true }).click();
      const rejected = page.waitForResponse((r) => r.url().endsWith("/user/delete") && r.request().method() === "POST");
      await page
        .getByRole("dialog")
        .getByRole("button", { name: tr("common.delete"), exact: true })
        .click();
      expect((await rejected).status()).toBe(404);
      await expect(page.getByRole("dialog")).toBeHidden();
      await expect(page.getByTestId("user-actions-" + staleID)).toBeVisible();
      await expect(page.getByTestId("pagination-range")).toHaveText(
        tr("consoleDirectory.pagination.range", { start: 1, end: 24, count: 24 }),
      );
      const createdOrg = await page.request.post(GATEWAY + "/organization/new", {
        headers,
        data: { organization_alias: prefix },
      });
      expect(createdOrg.status()).toBe(200);
      org = (await createdOrg.json()).organization_id;
      const names = [`研发 Alpha ${prefix}`, `运营 Beta ${prefix}`];
      for (const name of names) {
        const result = await page.request.post(GATEWAY + "/team/new", {
          headers,
          data: { organization_id: org, team_alias: name },
        });
        expect(result.status()).toBe(200);
        teams.push((await result.json()).team_id);
      }
      await stableGoto(page, "/teams");
      const search = page.getByPlaceholder(tr("Search teams by name or ID…"));
      await search.fill(prefix);
      await expect(page.getByRole("row").filter({ hasText: prefix })).toHaveCount(2);
      for (const query of ["研发", "ALPHA", teams[0].slice(0, 8)]) {
        const requested = page.waitForResponse(
          (r) => r.url().includes("/v2/team/list") && new URL(r.url()).searchParams.get("search") === query,
        );
        await search.fill(query);
        const result = await requested;
        expect(result.status()).toBe(200);
        expect((await result.json()).total).toBe(1);
        await expect(page.getByRole("row").filter({ hasText: names[0] })).toHaveCount(1);
        await expect(page.getByRole("row").filter({ hasText: names[1] })).toHaveCount(0);
      }
      await search.fill("no-such-" + prefix);
      await expect(page.getByText(tr("consoleDirectory.noTeams"), { exact: true })).toBeVisible();
      await search.fill("");
      await expect(page.getByRole("row").filter({ hasText: names[1] })).toHaveCount(1);
      // 切换语言后分页与团队名称立即更新/保持；再切回原语言验证完整删除提示和取消。
      const other = locale === "en" ? "zh-CN" : "en";
      await page
        .getByRole("button", { name: other === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
        .click();
      await expect(
        page.getByRole("button", { name: translate(other, "consoleDirectory.pagination.first") }),
      ).toBeVisible();
      await expect(page.getByRole("row").filter({ hasText: names[0] })).toHaveCount(1);
      await page
        .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
        .click();
      for (const confirm of [false, true]) {
        await page
          .getByRole("row")
          .filter({ hasText: names[0] })
          .getByRole("button", { name: tr("Open team actions") })
          .click();
        await page.getByRole("menuitem", { name: tr("Delete team"), exact: true }).click();
        const dialog = page.getByRole("dialog");
        await expect(dialog.getByText(tr("consoleDirectory.teamDelete.confirm"), { exact: true })).toBeVisible();
        await expect(dialog.getByText(names[0], { exact: true }).first()).toBeVisible();
        if (!confirm) {
          await dialog.getByRole("button", { name: tr("common.cancel"), exact: true }).click();
          expect((await page.request.get(GATEWAY + "/team/info?team_id=" + teams[0], { headers })).status()).toBe(200);
        } else {
          await dialog.getByPlaceholder(names[0], { exact: true }).fill(names[0]);
          const deleted = page.waitForResponse(
            (r) => r.url().endsWith("/team/delete") && r.request().method() === "POST",
          );
          await dialog.getByRole("button", { name: tr("common.delete"), exact: true }).click();
          expect((await deleted).status()).toBe(200);
          await expect(page.getByRole("row").filter({ hasText: names[0] })).toHaveCount(0);
          teams.shift();
        }
        await expect(dialog).toBeHidden();
      }
    } finally {
      for (const user_id of users)
        expect((await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id } })).status()).toBe(200);
      for (const team_id of teams)
        expect((await page.request.post(GATEWAY + "/team/delete", { headers, data: { team_id } })).status()).toBe(200);
      if (org)
        expect(
          (
            await page.request.delete(GATEWAY + "/organization/delete", { headers, data: { organization_id: org } })
          ).status(),
        ).toBe(200);
    }
  });
}
