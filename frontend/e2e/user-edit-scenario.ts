import { expect, test } from "@playwright/test";

import { GATEWAY, login, loginAdmin, sessionBearer, stableGoto, t, watchGateway } from "./helpers";
import { requireAuthorizationError } from "./assertions";
/** 用途：注册独立场景「users can be edited and their team access follows membership changes」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC245() {


test("users can be edited and their team access follows membership changes", async ({ page }) => {
  test.setTimeout(120_000);
  const guard = watchGateway(page);
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + await sessionBearer(page) };
  const teamResponse = await page.request.get(GATEWAY + "/team/list", { headers });
  expect(teamResponse.ok()).toBeTruthy();
  const teams = await teamResponse.json();
  const team = teams.find((row: { team_alias: string }) => row.team_alias === "e2e-fixture-team");
  const created = await page.request.post(GATEWAY + "/user/new", { headers, data: { user_email: "user-edit@xhub.local", user_alias: "编辑前", password: "user-edit-password", user_role: "user" } });
  expect(created.ok(), await created.text()).toBeTruthy();
  const account = await created.json();
  try {
    await stableGoto(page, "/users");
    await page.getByTestId(`user-actions-${account.user_id}`).click();
    await page.getByRole("menuitem", { name: t("pages.users.editUser"), exact: true }).click();
    await expect(page.getByLabel(t("pages.users.alias"))).toHaveValue("编辑前");
    await page.getByLabel(t("pages.users.alias")).fill("编辑后");
    await page.getByLabel(t("userBudgetReset.label")).selectOption("15");
    await page.getByLabel(t("pages.users.email")).fill("user-edited@xhub.local");
    await page.getByLabel(t("pages.users.maxBudget")).fill("25");
    const saved = page.waitForResponse((response) => response.url().includes("/user/update") && response.request().method() === "POST");
    await page.getByRole("button", { name: t("pages.users.saveChanges"), exact: true }).click();
    expect((await saved).status()).toBe(200);
    await expect(page.getByRole("heading", { name: "user-edited@xhub.local" })).toBeVisible();
    await expect(page.getByText("编辑后", { exact: true })).toBeVisible();
    await page.reload();
    await page.getByRole("button", { name: t("pages.users.editUser"), exact: true }).click();
    await expect(page.getByLabel(t("pages.users.alias"))).toHaveValue("编辑后");
    await expect(page.getByLabel(t("userBudgetReset.label"))).toHaveValue("15");
    await page.getByLabel(t("pages.users.alias")).fill("未保存");
    await page.getByRole("button", { name: t("common.cancel"), exact: true }).click();
    await expect(page.getByText("编辑后", { exact: true })).toBeVisible();
    // 入队前创建独立个人密钥，避免创建时自动派生团队绑定；离队后应继续按个人预算鉴权。
    const keyResponse = await page.request.post(GATEWAY + "/key/generate", { headers, data: { user_id: account.user_id, key_alias: "user-edit-key" } });
    expect(keyResponse.status(), await keyResponse.text()).toBe(200);
    const key = await keyResponse.json();
    await page.getByRole("tab", { name: t("common.overview"), exact: true }).click();
    await page.getByRole("button", { name: t("pages.users.addTeam"), exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("combobox", { name: t("pages.users.team"), exact: true }).click();
    await page.getByRole("option", { name: team.team_alias, exact: true }).click();
    await dialog.getByRole("button", { name: t("pages.users.addToTeam"), exact: true }).click();
    await expect(dialog).toHaveCount(0);
    const roleControl = page.getByRole("combobox", { name: `${t("pages.users.memberRole")} — ${team.team_alias}`, exact: true });
    await expect(roleControl).toContainText(t("Team member"));
    await roleControl.click();
    await page.getByRole("option", { name: t("Team admin"), exact: true }).click();
    await expect(roleControl).toContainText(t("Team admin"));
    const memberships = await page.request.get(GATEWAY + "/team/list?user_id=" + account.user_id, { headers });
    expect((await memberships.json()).find((row: { team_id: string }) => row.team_id === team.team_id).user_role).toBe("team_admin");
    await roleControl.click();
    await page.getByRole("option", { name: t("Team member"), exact: true }).click();
    await expect(roleControl).toContainText(t("Team member"));
    const inference = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + key.key }, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "membership access" }] } });
    expect(inference.ok(), await inference.text()).toBeTruthy();
    expect((await inference.json()).choices[0].message.content).toMatch(/ok/);
    await page.getByRole("button", { name: t("Remove from {value0}").replace("{value0}", team.team_alias), exact: true }).click();
    // 页面会先隐藏成员行，必须等真实删除接口提交，避免推理与成员删除事务并发导致不一致快照。
    const removed = page.waitForResponse(r => r.url().endsWith("/team/member_delete") && r.request().method() === "POST");
    await page.getByRole("dialog").getByRole("button", { name: t("common.delete"), exact: true }).click();
    expect((await removed).status()).toBe(200);
    await expect(roleControl).toHaveCount(0);
    // 离队保留个人凭证；本账号已有个人预算，推理应继续成功，原团队管理权则必须消失。
    const personal = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + key.key }, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "personal access after leaving" }] } });
    expect(personal.status(), await personal.text()).toBe(200);
    expect((await personal.json()).choices[0].message.content).toBe("e2e-ok");
    const remaining = await page.request.get(GATEWAY + "/team/list?user_id=" + account.user_id, { headers });
    expect(remaining.status()).toBe(200);
    expect((await remaining.json()).some((row: {team_id: string}) => row.team_id === team.team_id)).toBe(false);
    await page.getByRole("button", { name: t("Back to Users"), exact: true }).click();
    await expect(page.getByText("user-edited@xhub.local", { exact: true }).first()).toBeVisible();
    await login(page, "user-edited@xhub.local", "user-edit-password");
    await expect(page.getByText(t("nav.apiKeys")).first()).toBeVisible();
    const memberHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
    const denied = await page.request.get(GATEWAY + "/team/info?team_id=" + team.team_id, { headers: memberHeaders });
    requireAuthorizationError(denied.status(), await denied.json(), 404);
    await guard.assertOk();
  } finally {
    const cleanup = await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: account.user_id } });
    expect(cleanup.ok(), await cleanup.text()).toBeTruthy();
  }
});
}
