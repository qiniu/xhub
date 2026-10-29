import { expect, test } from "@playwright/test";

import { chooseKeyTeam, GATEWAY, login, loginAdmin, sessionBearer, stableGoto, t, watchGateway } from "./helpers";
/** 用途：注册独立场景「personal keys remain owned by the signed-in user through create, lookup, use and delete」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC101() {


test("personal keys remain owned by the signed-in user through create, lookup, use and delete", async ({ page }) => {
  test.setTimeout(120_000);
  const guard = watchGateway(page);
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + await sessionBearer(page) };
  const adminIdentity = await page.request.get(GATEWAY + "/auth/me", { headers });
  expect(adminIdentity.ok()).toBeTruthy();
  const adminUser = await adminIdentity.json();
  const baselineResponse = await page.request.get(GATEWAY + "/key/list?scope=personal&size=1", { headers });
  expect(baselineResponse.ok()).toBeTruthy();
  const baselineCount = (await baselineResponse.json()).total_count;
  const teamResponse = await page.request.get(GATEWAY + "/team/list", { headers });
  expect(teamResponse.ok()).toBeTruthy();
  const teams = await teamResponse.json();
  const teamID = teams.find((team: { team_alias: string }) => team.team_alias === "e2e-fixture-team").team_id;
  const createdUser = await page.request.post(GATEWAY + "/user/new", {
    headers, data: { user_email: "personal-member@xhub.local", password: "personal-member-password", user_role: "user", team_id: teamID, team_role: "member" },
  });
  expect(createdUser.ok(), await createdUser.text()).toBeTruthy();
  const member = await createdUser.json();
  const secondUserResponse = await page.request.post(GATEWAY + "/user/new", {
    headers, data: { user_email: "personal-second-user@xhub.local", password: "personal-second-user-password", user_role: "user", team_id: teamID },
  });
  expect(secondUserResponse.ok(), await secondUserResponse.text()).toBeTruthy();
  const secondUser = await secondUserResponse.json();
  const keys: string[] = [];
  let ownSecret = "";
  try {
    const foreignResponse = await page.request.post(GATEWAY + "/key/generate", {
      headers, data: { team_id: teamID, user_id: member.user_id, key_alias: "personal-member-key" },
    });
    expect(foreignResponse.ok(), await foreignResponse.text()).toBeTruthy();
    const foreign = await foreignResponse.json();
    keys.push(foreign.token_id);
    const secondKeyResponse = await page.request.post(GATEWAY + "/key/generate", {
      headers, data: { user_id: secondUser.user_id, key_alias: "personal-second-user-key" },
    });
    expect(secondKeyResponse.ok(), await secondKeyResponse.text()).toBeTruthy();
    keys.push((await secondKeyResponse.json()).token_id);

    await page.getByTestId("create-key-button").click();
    await chooseKeyTeam(page);
    await page.getByLabel(t("Key Name")).fill("personal-admin-key");
    const issued = page.waitForResponse((response) => response.url().includes("/key/generate") && response.request().method() === "POST");
    await page.getByRole("button", { name: t("pages.apiKeys.createSubmit"), exact: true }).click();
    const own = await (await issued).json();
    expect(own.user_id, "个人 Key 必须属于当前登录用户").toBe(adminUser.user_id);
    expect(own.team_id, "团队归属由当前用户成员关系推导，不由创建表单指定").toBe(adminUser.teams[0]?.team_id ?? null);
    keys.push(own.token_id);
    ownSecret = own.key;
    await expect(page.getByText(t("pages.apiKeys.saveKey"))).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByText("personal-admin-key", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("personal-member-key", { exact: true })).toHaveCount(0);
    await expect(page.getByText("personal-second-user-key", { exact: true })).toHaveCount(0);

    const personal = await page.request.get(GATEWAY + "/key/list?scope=personal&size=1", { headers });
    expect(personal.ok()).toBeTruthy();
    const listed = await personal.json();
    expect(listed.total_count).toBe(baselineCount + 1);
    expect(listed.keys[0].token_id).toBe(own.token_id);
    const attacked = await page.request.get(GATEWAY + "/key/list?scope=personal&user_id=" + member.user_id, { headers });
    expect((await attacked.json()).keys).toEqual([]);

    await stableGoto(page, "/api-keys?filter_user=" + member.user_id);
    await expect(page.getByText("personal-admin-key", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("personal-member-key", { exact: true })).toHaveCount(0);
    await stableGoto(page, "/api-keys?key=" + foreign.token_id);
    await expect(page.getByText(t("Key not found"), { exact: true })).toBeVisible();
    const denied = await page.request.get(GATEWAY + "/key/info?scope=personal&key=" + foreign.token_id, { headers });
    expect(denied.status()).toBe(404);

    const inference = await page.request.post(GATEWAY + "/v1/chat/completions", {
      headers: { Authorization: "Bearer " + ownSecret }, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "personal key e2e" }] },
    });
    expect(inference.ok(), await inference.text()).toBeTruthy();
    expect((await inference.json()).choices[0].message.content).toMatch(/ok/);

    await login(page, "personal-member@xhub.local", "personal-member-password");
    await expect(page.getByTestId("create-key-button")).toBeVisible();
    await stableGoto(page, "/api-keys");
    await expect(page.getByText("personal-member-key", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("personal-admin-key", { exact: true })).toHaveCount(0);
    await expect(page.getByText("personal-second-user-key", { exact: true })).toHaveCount(0);
    const memberHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
    const memberList = await page.request.get(GATEWAY + "/key/list?scope=personal", { headers: memberHeaders });
    expect((await memberList.json()).keys.map((key: { token_id: string }) => key.token_id)).toEqual(expect.arrayContaining([foreign.token_id]));

    await loginAdmin(page);
    await stableGoto(page, "/api-keys");
    await page.getByText("personal-admin-key", { exact: true }).first().click();
    await page.getByRole("button", { name: t("More key actions"), exact: true }).click();
    await page.getByRole("menuitem", { name: t("Delete Key"), exact: true }).click();
    await page.getByRole("dialog").getByPlaceholder("personal-admin-key", { exact: true }).fill("personal-admin-key");
    await page.getByRole("dialog").getByRole("button", { name: t("common.delete"), exact: true }).click();
    await expect(page.getByTestId("create-key-button")).toBeVisible();
    await expect(page.getByText("personal-admin-key", { exact: true })).toHaveCount(0);
    const revoked = await page.request.post(GATEWAY + "/v1/chat/completions", {
      headers: { Authorization: "Bearer " + ownSecret }, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "revoked" }] },
    });
    expect(revoked.status()).toBe(401);
    keys.splice(keys.indexOf(own.token_id), 1);
    await guard.assertOk();
  } finally {
    // 即使浏览器断言失败也主动撤销本测试密钥；隔离脚本退出时删除整个专属 schema，包含新用户及辅助团队。
    if (keys.length) {
      const cleanup = await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys }, timeout: 10_000 });
      expect(cleanup.ok(), await cleanup.text()).toBeTruthy();
    }
  }
});
}
/** 用途：注册独立场景「ordinary user with a team creates and uses a personal key without choosing a team」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC102() {
const hasTeam: boolean = true;

test("ordinary user with a team creates and uses a personal key without choosing a team", async ({ page }) => {
    test.setTimeout(120_000);
    const guard = watchGateway(page);
    await loginAdmin(page);
    const adminHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
    let teamID: string | undefined;
    if (hasTeam) {
      const teamsResponse = await page.request.get(GATEWAY + "/team/list", { headers: adminHeaders });
      expect(teamsResponse.ok()).toBeTruthy();
      teamID = (await teamsResponse.json()).find((team: { team_alias: string }) => team.team_alias === "e2e-fixture-team").team_id;
    }
    const email = `personal-${hasTeam ? "member-direct" : "solo"}@xhub.local`;
    const password = "personal-direct-password";
    const userResponse = await page.request.post(GATEWAY + "/user/new", {
      headers: adminHeaders, data: { user_email: email, password, user_role: "user", team_id: teamID, team_role: hasTeam ? "member" : undefined },
    });
    expect(userResponse.ok(), await userResponse.text()).toBeTruthy();
    const user = await userResponse.json();
    let id = "";
    try {
      await login(page, email, password);
      await expect(page.getByTestId("create-key-button")).toBeVisible({ timeout: 20_000 });
      await stableGoto(page, "/api-keys");
      const headers = { Authorization: "Bearer " + await sessionBearer(page) };
      const identityResponse = await page.request.get(GATEWAY + "/auth/me", { headers });
      expect(identityResponse.ok()).toBeTruthy();
      expect((await identityResponse.json()).teams.length).toBe(hasTeam ? 1 : 0);
      await page.getByTestId("create-key-button").click();
      await chooseKeyTeam(page);
      const alias = `personal-direct-${hasTeam ? "member" : "solo"}`;
      await page.getByLabel(t("Key Name")).fill(alias);
      await page.getByRole("dialog").getByRole("combobox", { name: t("Select models"), exact: true }).click();
      await page.getByRole("option", { name: t("All Proxy Models"), exact: true }).click();
      await page.keyboard.press("Escape");
      const responsePromise = page.waitForResponse((response) => response.url().includes("/key/generate") && response.request().method() === "POST");
      await page.getByRole("button", { name: t("pages.apiKeys.createSubmit"), exact: true }).click();
      const response = await responsePromise;
      expect(response.status()).toBe(200);
      expect(response.request().postDataJSON()).toMatchObject({ user_id: user.user_id, team_id: null, models: ["all-proxy-models"] });
      const key = await response.json();
      id = key.token_id;
      expect(key.team_id).toBe(hasTeam ? teamID : null);
      expect(key.user_id).toBe(user.user_id);
      await expect(page.getByText(t("pages.apiKeys.saveKey"))).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(page.getByText(alias, { exact: true }).first()).toBeVisible();
      const inferenceBody = { model: "gpt-4o-mini", messages: [{ role: "user", content: alias }] };
      const inference = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key.key }, data: inferenceBody,
      });
      expect(inference.ok(), await inference.text()).toBeTruthy();
      expect((await inference.json()).choices[0].message.content).toMatch(/ok/);
      await page.getByText(alias, { exact: true }).first().click();
      await page.getByRole("button", { name: t("More key actions"), exact: true }).click();
      await page.getByRole("menuitem", { name: t("Delete Key"), exact: true }).click();
      await page.getByRole("dialog").getByPlaceholder(alias, { exact: true }).fill(alias);
      await page.getByRole("dialog").getByRole("button", { name: t("common.delete"), exact: true }).click();
      await expect(page.getByTestId("create-key-button")).toBeVisible();
      await expect(page.getByText(alias, { exact: true })).toHaveCount(0);
      const revoked = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key.key }, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "deleted" }] },
      });
      expect(revoked.status()).toBe(401);
      id = "";
      await guard.assertOk();
    } finally {
      if (id) {
        const cleanup = await page.request.post(GATEWAY + "/key/delete", { headers: adminHeaders, data: { keys: [id] } });
        expect(cleanup.ok(), await cleanup.text()).toBeTruthy();
      }
      const removed = await page.request.post(GATEWAY + "/user/delete", { headers: adminHeaders, data: { user_id: user.user_id } });
      expect(removed.status(), await removed.text()).toBe(200);
    }
  });
}
/** 用途：注册独立场景「ordinary user without a team creates and uses a personal key without choosing a team」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC103() {
const hasTeam: boolean = false;

test("ordinary user without a team creates and uses a personal key without choosing a team", async ({ page }) => {
    test.setTimeout(120_000);
    const guard = watchGateway(page);
    await loginAdmin(page);
    const adminHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
    let teamID: string | undefined;
    if (hasTeam) {
      const teamsResponse = await page.request.get(GATEWAY + "/team/list", { headers: adminHeaders });
      expect(teamsResponse.ok()).toBeTruthy();
      teamID = (await teamsResponse.json()).find((team: { team_alias: string }) => team.team_alias === "e2e-fixture-team").team_id;
    }
    const email = `personal-${hasTeam ? "member-direct" : "solo"}@xhub.local`;
    const password = "personal-direct-password";
    const userResponse = await page.request.post(GATEWAY + "/user/new", {
      headers: adminHeaders, data: { user_email: email, password, user_role: "user", team_id: teamID, team_role: hasTeam ? "member" : undefined },
    });
    expect(userResponse.ok(), await userResponse.text()).toBeTruthy();
    const user = await userResponse.json();
    let id = "";
    try {
      await login(page, email, password);
      await expect(page.getByTestId("create-key-button")).toBeVisible({ timeout: 20_000 });
      await stableGoto(page, "/api-keys");
      const headers = { Authorization: "Bearer " + await sessionBearer(page) };
      const identityResponse = await page.request.get(GATEWAY + "/auth/me", { headers });
      expect(identityResponse.ok()).toBeTruthy();
      expect((await identityResponse.json()).teams.length).toBe(hasTeam ? 1 : 0);
      await page.getByTestId("create-key-button").click();
      await chooseKeyTeam(page);
      const alias = `personal-direct-${hasTeam ? "member" : "solo"}`;
      await page.getByLabel(t("Key Name")).fill(alias);
      await page.getByRole("dialog").getByRole("combobox", { name: t("Select models"), exact: true }).click();
      await page.getByRole("option", { name: t("All Proxy Models"), exact: true }).click();
      await page.keyboard.press("Escape");
      const responsePromise = page.waitForResponse((response) => response.url().includes("/key/generate") && response.request().method() === "POST");
      await page.getByRole("button", { name: t("pages.apiKeys.createSubmit"), exact: true }).click();
      const response = await responsePromise;
      expect(response.status()).toBe(200);
      expect(response.request().postDataJSON()).toMatchObject({ user_id: user.user_id, team_id: null, models: ["all-proxy-models"] });
      const key = await response.json();
      id = key.token_id;
      expect(key.team_id).toBe(hasTeam ? teamID : null);
      expect(key.user_id).toBe(user.user_id);
      await expect(page.getByText(t("pages.apiKeys.saveKey"))).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(page.getByText(alias, { exact: true }).first()).toBeVisible();
      const inferenceBody = { model: "gpt-4o-mini", messages: [{ role: "user", content: alias }] };
      const inference = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key.key }, data: inferenceBody,
      });
      expect(inference.ok(), await inference.text()).toBeTruthy();
      expect((await inference.json()).choices[0].message.content).toMatch(/ok/);
      await page.getByText(alias, { exact: true }).first().click();
      await page.getByRole("button", { name: t("More key actions"), exact: true }).click();
      await page.getByRole("menuitem", { name: t("Delete Key"), exact: true }).click();
      await page.getByRole("dialog").getByPlaceholder(alias, { exact: true }).fill(alias);
      await page.getByRole("dialog").getByRole("button", { name: t("common.delete"), exact: true }).click();
      await expect(page.getByTestId("create-key-button")).toBeVisible();
      await expect(page.getByText(alias, { exact: true })).toHaveCount(0);
      const revoked = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key.key }, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "deleted" }] },
      });
      expect(revoked.status()).toBe(401);
      id = "";
      await guard.assertOk();
    } finally {
      if (id) {
        const cleanup = await page.request.post(GATEWAY + "/key/delete", { headers: adminHeaders, data: { keys: [id] } });
        expect(cleanup.ok(), await cleanup.text()).toBeTruthy();
      }
      const removed = await page.request.post(GATEWAY + "/user/delete", { headers: adminHeaders, data: { user_id: user.user_id } });
      expect(removed.status(), await removed.text()).toBe(200);
    }
  });
}
