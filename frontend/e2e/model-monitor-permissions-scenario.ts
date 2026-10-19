import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, login, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册普通用户监控权限闭环；locale 为中英文界面，无返回值。
 * 前置隔离数据库、真实后台和本地上游；验证登录、无团队参数目录、筛选、推理、统计隔离及即时收窄。
 * 所有测试资源在 finally 经真实接口删除；调用日志最终由运行器删除专属 schema，不使用外部供应商凭据。 */
export function registerModelMonitorPermissionsScenario(locale: "en" | "zh-CN") {
  test("model monitor respects account grants " + locale, async ({ page }, info) => {
    test.setTimeout(180_000);
    await loginAdmin(page);
    const adminHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
    const prefix = "monitor-permissions-" + locale + "-" + Date.now();
    const aliases = [prefix + "-a", prefix + "-b"], suppliers = [prefix + "-provider-a", prefix + "-provider-b"];
    const actual = prefix + "-actual", password = "monitor-test-password";
    const credentials: string[] = [], models: string[] = [], orgs: string[] = [], teams: string[] = [], users: string[] = [], keys: string[] = [];
    const secrets: string[] = [];
    /** 通过管理员接口创建本场景资源；path/data 为真实路由和正文，返回响应对象；失败定位路由，finally 回收已创建项。 */
    const create = async (path: string, data: unknown) => {
      const response = await page.request.post(GATEWAY + path, { headers: adminHeaders, data });
      expect(response.status(), path + ": " + await response.text()).toBe(200);
      return response.json();
    };
    try {
      for (let index = 0; index < 2; index++) {
        await create("/credentials", {
          credential_name: suppliers[index], credential_info: { custom_llm_provider: "openai" },
          credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
        });
        credentials.push(suppliers[index]);
        const model = await create("/model/new", {
          model_name: aliases[index], litellm_params: { model: actual, litellm_credential_name: suppliers[index], custom_llm_provider: "openai", input_cost_per_token: 0.01, output_cost_per_token: 0.02 },
          model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
        });
        models.push(model.model_info.id);
        const org = await create("/organization/new", { organization_alias: prefix + "-org-" + index, models: [aliases[index]] });
        orgs.push(org.organization_id);
        const team = await create("/team/new", { organization_id: orgs[index], team_alias: prefix + "-team-" + index });
        teams.push(team.team_id);
        const user = await create("/user/new", { user_email: prefix + "-" + index + "@example.test", password, team_id: teams[index], team_role: "user" });
        users.push(user.user_id);
        const key = await create("/key/generate", { user_id: users[index], key_alias: prefix + "-key-" + index });
        keys.push(key.token_id); secrets.push(key.key);
      }
      await login(page, prefix + "-0@example.test", password);
      await expect(page.getByText(translate("zh-CN", "nav.apiKeys"), { exact: true }).first()).toBeVisible();
      await page.getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true }).click();
      const tr = (key: string) => translate(locale, key);
      const userHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
      const want = [{ supplier: suppliers[0], model: actual }];
      const loaded = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/models/monitor"));
      await stableGoto(page, "/model-monitor");
      const initial = await loaded;
      expect(initial.status()).toBe(200);
      expect(new URL(initial.url()).searchParams.has("team_id"), "页面默认查询不依赖 team_id 才能执行权限过滤").toBe(false);
      expect((await initial.json()).options).toEqual(want);
      const supplierSelect = page.getByLabel(tr("monitor.supplier"), { exact: true });
      await expect(supplierSelect.getByRole("option", { name: suppliers[0], exact: true })).toHaveCount(1);
      await expect(supplierSelect.getByRole("option", { name: suppliers[1], exact: true })).toHaveCount(0);
      await expect(supplierSelect.getByRole("option")).toHaveCount(2);
      await supplierSelect.selectOption(suppliers[0]);
      await page.getByLabel(tr("monitor.model"), { exact: true }).selectOption(actual);
      const listed = await page.request.get(GATEWAY + "/v1/models", { headers: userHeaders });
      expect((await listed.json()).data.map((entry: { id: string }) => entry.id)).toEqual([aliases[0]]);
      for (let index = 0; index < 2; index++) {
        const response = await page.request.post(GATEWAY + "/v1/chat/completions", {
          headers: { Authorization: "Bearer " + secrets[index] },
          data: { model: aliases[index], messages: [{ role: "user", content: "Verify monitor visibility." }] },
        });
        expect(response.status(), await response.text()).toBe(200);
        expect((await response.json()).choices[0].message.content).toBe("e2e-ok");
      }
      await expect.poll(async () => {
        const response = await page.request.get(GATEWAY + "/models/monitor", { headers: userHeaders });
        return (await response.json()).summary.requests;
      }, { timeout: 80_000 }).toBe(1);
      await page.getByRole("button", { name: tr("monitor.refresh"), exact: true }).click();
      await expect(page.getByRole("region", { name: tr("monitor.requests"), exact: true })).toContainText("1");
      await expect(page.getByRole("rowheader", { name: actual, exact: true })).toHaveCount(1);
      const scoped = await (await page.request.get(GATEWAY + "/models/monitor", { headers: userHeaders })).json();
      expect(scoped.options).toEqual(want);
      expect(scoped.summary).toMatchObject({ requests: 1, total_tokens: 10 });
      expect(scoped.models).toHaveLength(1);
      expect(scoped.models[0]).toMatchObject({ supplier: suppliers[0], model: actual, requests: 1 });
      const foreign = await page.request.get(GATEWAY + "/models/monitor?team_id=" + teams[1], { headers: userHeaders });
      expect(foreign.status()).toBe(404);
      expect((await foreign.json()).error).toBeTruthy();
      const filtered = await page.request.get(GATEWAY + "/models/monitor?supplier=" + suppliers[1], { headers: userHeaders });
      expect((await filtered.json()).summary.requests).toBe(0);
      const blocked = await page.request.patch(GATEWAY + "/organization/update", { headers: adminHeaders, data: { organization_id: orgs[0], models: ["no-default-models"] } });
      expect(blocked.status(), await blocked.text()).toBe(200);
      const refreshed = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/models/monitor"));
      await stableGoto(page, "/model-monitor");
      const narrowed = await (await refreshed).json();
      expect(narrowed.options).toEqual([]);
      expect(narrowed.summary.requests, "组织收窄后仍保留本人历史统计").toBe(1);
      // 当前可用目录为空，但本人历史仍可筛选；从未授权且没有本人记录的供应商不能混入。
      await expect(page.getByLabel(tr("monitor.supplier"), { exact: true }).getByRole("option")).toHaveCount(2);
      await expect(page.getByLabel(tr("monitor.supplier"), { exact: true }).getByRole("option", { name: suppliers[1], exact: true })).toHaveCount(0);
      await expect(page.getByLabel(tr("monitor.model"), { exact: true }).getByRole("option")).toHaveCount(2);
      await expect(page.getByRole("rowheader", { name: actual, exact: true })).toHaveCount(1);
      const before = await (await page.request.get(UPSTREAM + "/e2e/request-count")).json();
      const denied = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + secrets[0] },
        data: { model: aliases[0], messages: [{ role: "user", content: "Verify revoked model access." }] },
      });
      expect(denied.status()).toBe(401);
      expect(await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).toEqual(before);
      await info.attach("monitor-permissions-contract", { body: JSON.stringify({ initial: want, scoped, narrowed }, null, 2), contentType: "application/json" });
    } finally {
      if (keys.length) expect((await page.request.post(GATEWAY + "/key/delete", { headers: adminHeaders, data: { keys } })).status()).toBe(200);
      for (const team_id of teams) expect((await page.request.post(GATEWAY + "/team/delete", { headers: adminHeaders, data: { team_id } })).status()).toBe(200);
      for (const user_id of users) expect((await page.request.post(GATEWAY + "/user/delete", { headers: adminHeaders, data: { user_id } })).status()).toBe(200);
      for (const organization_id of orgs) expect((await page.request.delete(GATEWAY + "/organization/delete", { headers: adminHeaders, data: { organization_id } })).status()).toBe(200);
      for (const id of models) expect((await page.request.post(GATEWAY + "/model/delete", { headers: adminHeaders, data: { id } })).status()).toBe(200);
      for (const name of credentials) expect((await page.request.delete(GATEWAY + "/credentials/" + name, { headers: adminHeaders })).status()).toBe(200);
    }
  });
}
