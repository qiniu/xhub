import { test, expect } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 双语删除完整流程：创建隔离用户/团队，经列表和详情真实删除并回读；核对不再请求角色占位接口，finally清理所有资源。 */
export function registerDeleteContractScenario(locale: "en" | "zh-CN") {
  test(`console deletion contracts ${locale}`, async ({ page }) => {
    await loginAdmin(page);
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    const tr = (key: string) => translate(locale, key);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const suffix = `${locale}-${Date.now()}`;
    const users: string[] = [];
    let org = "",
      team = "";
    const roleRequests: string[] = [];
    page.on("request", (request) => {
      if (request.url().includes("/user/available_roles")) roleRequests.push(request.url());
    });
    try {
      const organization = await page.request.post(GATEWAY + "/organization/new", {
        headers,
        data: { organization_alias: "delete-org-" + suffix },
      });
      expect(organization.status()).toBe(200);
      org = (await organization.json()).organization_id;
      const teamName = "delete-team-" + suffix;
      const createdTeam = await page.request.post(GATEWAY + "/team/new", {
        headers,
        data: { organization_id: org, team_alias: teamName },
      });
      expect(createdTeam.status()).toBe(200);
      team = (await createdTeam.json()).team_id;
      for (const mode of ["list", "detail"]) {
        const email = `delete-${mode}-${suffix}@example.invalid`;
        const created = await page.request.post(GATEWAY + "/user/new", {
          headers,
          data: { user_email: email, user_role: "user", password: "Delete-test-password" },
        });
        expect(created.status()).toBe(200);
        const id = (await created.json()).user_id;
        users.push(id);
        await stableGoto(page, "/users");
        const row = page.getByRole("row").filter({ hasText: email });
        await expect(row).toBeVisible();
        await expect(row.getByText(tr("Regular user"), { exact: true })).toBeVisible();
        if (mode === "detail") {
          await row.getByRole("button", { name: tr("pages.users.openUserActions") }).click();
          await page.getByTestId("user-action-edit").click();
          await page.getByRole("button", { name: tr("pages.users.deleteUser"), exact: true }).click();
        } else {
          await row.getByRole("button", { name: tr("pages.users.openUserActions") }).click();
          await page.getByRole("menuitem", { name: tr("pages.users.deleteUser"), exact: true }).click();
        }
        const dialog = page.getByRole("dialog");
        const deletedPromise = page.waitForResponse(
          (r) => r.url().endsWith("/user/delete") && r.request().method() === "POST",
        );
        await dialog.getByRole("button", { name: tr("common.delete"), exact: true }).click();
        const deleted = await deletedPromise;
        expect(deleted.request().postDataJSON()).toEqual({ user_id: id });
        expect(deleted.status()).toBe(200);
        await expect(dialog).toBeHidden();
        expect((await page.request.get(GATEWAY + "/user/info?user_id=" + id, { headers })).status()).toBe(404);
        await stableGoto(page, "/users");
        await expect(page.getByRole("row").filter({ hasText: email })).toHaveCount(0);
      }
      expect(roleRequests).toEqual([]);
      await stableGoto(page, "/teams");
      const row = page.getByRole("row").filter({ hasText: teamName });
      await row.getByRole("button", { name: tr("Open team actions") }).click();
      await page.getByRole("menuitem", { name: tr("Delete team"), exact: true }).click();
      const dialog = page.getByRole("dialog");
      await dialog.getByPlaceholder(teamName, { exact: true }).fill(teamName);
      const deletedPromise = page.waitForResponse(
        (r) => r.url().endsWith("/team/delete") && r.request().method() === "POST",
      );
      await dialog.getByRole("button", { name: tr("common.delete"), exact: true }).click();
      const deleted = await deletedPromise;
      expect(deleted.request().postDataJSON()).toEqual({ team_id: team });
      expect(deleted.status()).toBe(200);
      await expect(dialog).toBeHidden();
      await expect(page.getByRole("row").filter({ hasText: teamName })).toHaveCount(0);
      expect((await page.request.get(GATEWAY + "/team/info?team_id=" + team, { headers })).status()).toBe(404);
    } finally {
      for (const user_id of users) await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id } });
      if (team) await page.request.post(GATEWAY + "/team/delete", { headers, data: { team_id: team } });
      if (org) await page.request.delete(GATEWAY + "/organization/delete?organization_id=" + org, { headers });
    }
  });
}
