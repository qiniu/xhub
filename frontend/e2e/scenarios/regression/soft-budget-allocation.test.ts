import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "../../helpers";
import { translate } from "../../../src/i18n/translate";

// 双语真实流程：用微小个人预算完成推理产生超支，再从页面创建兄弟团队并 CSV 导入新用户。
// 前置独立数据库和本地模型服务；验证旧用户 403、新用户真实推理成功、预算与消费持久化，finally 删除全部测试账号、团队及组织。
for (const locale of ["en", "zh-CN"] as const) {
  test("soft overspend does not block account administration " + locale, async ({ page }) => {
    test.setTimeout(120_000);
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const orgName = "soft-allocation-" + locale;
    const orgResponse = await page.request.post(GATEWAY + "/organization/new", { headers, data: { organization_alias: orgName } });
    expect(orgResponse.status(), await orgResponse.text()).toBe(200);
    const orgID = (await orgResponse.json()).organization_id;
    const users: string[] = [], teams: string[] = [];
    let modelID = "";
    try {
      // 显式配置本地模型单价，默认目录模型可能免费，不能依赖目录价格来制造超支前置条件。
      const priced = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: orgName,
        litellm_params: { model: "allocation-local", custom_llm_provider: "openai", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
      } });
      expect(priced.status(), await priced.text()).toBe(200);
      modelID = (await priced.json()).model_info.id;
      const teamResponse = await page.request.post(GATEWAY + "/team/new", { headers, data: { organization_id: orgID, team_alias: orgName + "-old" } });
      expect(teamResponse.status(), await teamResponse.text()).toBe(200);
      teams.push((await teamResponse.json()).team_id);
      const oldEmail = "overspent-" + locale + "@example.invalid";
      const oldResponse = await page.request.post(GATEWAY + "/user/new", { headers, data: {
        user_email: oldEmail, user_role: "user", password: "allocation-test-password", team_id: teams[0], max_budget: 0.000001,
      } });
      expect(oldResponse.status(), await oldResponse.text()).toBe(200);
      users.push((await oldResponse.json()).user_id);
      const signed = await page.request.post(GATEWAY + "/v2/login", { data: { username: oldEmail, password: "allocation-test-password" } });
      expect(signed.status(), await signed.text()).toBe(200);
      const oldHeaders = { Authorization: "Bearer " + (await signed.json()).key };
      const request = { model: orgName, messages: [{ role: "user", content: "Complete a soft budget request." }] };
      const call = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: oldHeaders, data: request });
      expect(call.status(), await call.text()).toBe(200);
      expect((await call.json()).choices[0].message.content).toBe("e2e-ok");
      // 消费异步落库；轮询真实用户接口确认超支后才执行管理步骤，不能把尚未结算误当修复成功。
      await expect.poll(async () => {
        const settled = await page.request.get(GATEWAY + "/user/info?user_id=" + users[0], { headers });
        expect(settled.status()).toBe(200);
        return (await settled.json()).user_info.spend;
      }, { timeout: 20_000 }).toBeGreaterThan(0.000001);
      const before = await page.request.get(GATEWAY + "/user/info?user_id=" + users[0], { headers });
      expect(before.status()).toBe(200);
      const oldInfo = (await before.json()).user_info;
      expect(oldInfo.spend).toBeGreaterThan(oldInfo.max_budget);
      const denied = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: oldHeaders, data: request });
      expect(denied.status(), await denied.text()).toBe(403);
      await stableGoto(page, "/teams");
      await page.getByTestId("create-team-button").click();
      const dialog = page.getByRole("dialog");
      await dialog.getByRole("combobox", { name: tr("Organization"), exact: true }).click();
      await page.getByRole("option", { name: new RegExp(orgName) }).click();
      await page.getByTestId("team-name-input").fill(orgName + "-new");
      const savedTeam = page.waitForResponse(r => r.url().endsWith("/team/new") && r.request().method() === "POST");
      await page.getByTestId("create-team-submit").click();
      const saved = await savedTeam;
      expect(saved.status(), await saved.text()).toBe(200);
      teams.push((await saved.json()).team_id);
      await expect(page.getByText(orgName + "-new", { exact: true }).first()).toBeVisible();
      await stableGoto(page, "/users");
      await page.getByRole("button", { name: tr("userCsv.title"), exact: true }).click();
      const csvDialog = page.getByRole("dialog");
      const email = "new-allocation-" + locale + "@example.invalid";
      const csv = "user_email,user_role,password,team_id,max_budget\n" + email + ",user,allocation-test-password," + teams[1] + ",500\n";
      await csvDialog.getByLabel(tr("userCsv.upload")).setInputFiles({ name: "new-users.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
      await expect(csvDialog.getByText(email, { exact: true })).toBeVisible();
      const imported = page.waitForResponse(r => r.url().endsWith("/user/new") && r.request().method() === "POST");
      await csvDialog.getByRole("button", { name: translate(locale, "userCsv.create", { count: 1 }), exact: true }).click();
      const response = await imported;
      expect(response.status(), await response.text()).toBe(200);
      users.push((await response.json()).user_id);
      const info = await page.request.get(GATEWAY + "/user/info?user_id=" + users[1], { headers });
      expect((await info.json()).user_info).toMatchObject({ max_budget: 500, spend: 0 });
      const login = await page.request.post(GATEWAY + "/v2/login", { data: { username: email, password: "allocation-test-password" } });
      expect(login.status(), await login.text()).toBe(200);
      const newCall = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + (await login.json()).key }, data: request });
      expect(newCall.status(), await newCall.text()).toBe(200);
      expect((await newCall.json()).choices[0].message.content).toBe("e2e-ok");
      const after = await page.request.get(GATEWAY + "/user/info?user_id=" + users[0], { headers });
      expect((await after.json()).user_info).toMatchObject({ max_budget: oldInfo.max_budget, spend: oldInfo.spend });
    } finally {
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(200);
      for (const user_id of users) expect((await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id } })).status()).toBe(200);
      for (const team_id of teams) expect((await page.request.post(GATEWAY + "/team/delete", { headers, data: { team_id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/organization/delete", { headers, data: { organization_id: orgID } })).status()).toBe(200);
    }
  });
}
