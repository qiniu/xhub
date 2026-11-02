import { createHash } from "node:crypto";
import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, login, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册日志候选的真实浏览器回归；locale 为界面语言。隔离数据库和本地上游创建两用户、模型及密钥；
 * 验证候选、权限隔离、筛选结果和手输路径，finally 删除资源，运行器删除测试 schema。 */
export function registerLogFilterOptionsScenario(locale: "en" | "zh-CN") {
  test("log filter suggestions and isolation " + locale, async ({ page }, info) => {
    test.setTimeout(180_000);
    await loginAdmin(page);
    const adminHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
    const prefix = "log-option-" + locale.toLowerCase() + "-" + Date.now();
    const password = "log-option-test-password";
    const models: string[] = [], credentials: string[] = [], orgs: string[] = [], teams: string[] = [], users: string[] = [], keys: string[] = [], secrets: string[] = [], calls: string[] = [];
    const aliases = [prefix + "-model-a", prefix + "-model-b"];
    const suppliers = [prefix + "-provider-a", prefix + "-provider-b"];
    const keyAliases = [prefix + "-key-a", prefix + "-key-b"];
    const sessions = [prefix + "-session-a", prefix + "-session-b"];
    /** 调用管理员真实接口创建隔离夹具；失败时保留路由及响应信息，返回 JSON；finally 清理。 */
    const create = async (path: string, data: unknown) => {
      const response = await page.request.post(GATEWAY + path, { headers: adminHeaders, data });
      expect(response.status(), path + ": " + await response.text()).toBe(200);
      return response.json();
    };
    try {
      for (let i = 0; i < 2; i++) {
        await create("/credentials", { credential_name: suppliers[i], credential_info: { custom_llm_provider: "openai" }, credential_values: { api_base: UPSTREAM, api_key: "sk-fake" } });
        credentials.push(suppliers[i]);
        const model = await create("/model/new", { model_name: aliases[i], litellm_params: { model: prefix + "-actual", litellm_credential_name: suppliers[i], custom_llm_provider: "openai", input_cost_per_token: 0.01, output_cost_per_token: 0.02 }, model_info: { transport: "bypass_openai_chat", pricing_source: "manual" } });
        models.push(model.model_info.id);
        const org = await create("/organization/new", { organization_alias: prefix + "-org-" + i, models: [aliases[i]] });
        orgs.push(org.organization_id);
        const team = await create("/team/new", { organization_id: orgs[i], team_alias: prefix + "-team-" + i });
        teams.push(team.team_id);
        const user = await create("/user/new", { user_email: prefix + "-" + i + "@example.test", password, team_id: teams[i], team_role: "user" });
        users.push(user.user_id);
        const key = await create("/key/generate", { user_id: users[i], key_alias: keyAliases[i] });
        keys.push(key.token_id); secrets.push(key.key);
        const response = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + secrets[i], "X-Session-Id": sessions[i] }, data: { model: aliases[i], messages: [{ role: "user", content: "Verify log filtering." }] } });
        expect(response.status(), await response.text()).toBe(200);
        calls.push(response.headers()["x-litellm-call-id"]);
        expect(calls[i]).toBeTruthy();
      }
      await login(page, prefix + "-0@example.test", password);
      await expect(page.getByText(translate("zh-CN", "nav.apiKeys"), { exact: true }).first()).toBeVisible();
      await page.getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true }).click();
      const tr = (key: string) => translate(locale, key);
      const userHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
      await expect.poll(async () => (await page.request.get(GATEWAY + "/spend/logs/ui/" + calls[0], { headers: userHeaders })).status(), { timeout: 80_000 }).toBe(200);
      const fingerprint = createHash("sha256").update(secrets[0]).digest("hex");
      const foreignFingerprint = createHash("sha256").update(secrets[1]).digest("hex");
      await stableGoto(page, "/logs");
      const panel = page.getByRole("tabpanel", { name: /^(日志|Logs)$/ });
      const open = panel.getByRole("button", { name: locale === "en" ? /^Filters(?:\s*\d+)?$/ : /^筛选(?:\s*\d+)?$/ });
      const dialog = page.getByRole("dialog");
      const labels = ["Model", "Key Alias", "logsUX.keyFingerprint", "logsUX.supplier", "requestMetrics.inbound"];
      const fields = ["model", "key_alias", "key_hash", "supplier", "inbound_endpoint"];
      const values = [aliases[0], keyAliases[0], fingerprint, suppliers[0], "/v1/chat/completions"];
      const foreign = [aliases[1], keyAliases[1], foreignFingerprint, suppliers[1], ""];
      for (let i = 0; i < fields.length; i++) {
        await open.click();
        if (i > 0) await dialog.getByText(tr("logsUX.advanced"), { exact: true }).click();
        const input = dialog.getByLabel(tr(labels[i]), { exact: true });
        const lookup = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/spend/logs/ui/filter-options") && new URL(r.url()).searchParams.get("field") === fields[i]);
        await input.click();
        const response = await lookup;
        expect(response.status(), await response.text()).toBe(200);
        expect(response.headers()["cache-control"]).toBe("no-store");
        const body = await response.json();
        expect(body.data).toContain(values[i]);
        if (foreign[i]) expect(body.data).not.toContain(foreign[i]);
        const shown = fields[i] === "key_hash" ? fingerprint.slice(0, 12) + "…" + fingerprint.slice(-8) : values[i];
        await page.getByRole("option", { name: shown, exact: true }).click();
        expect(await input.inputValue()).toBe(values[i]);
        const filtered = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/spend/logs/ui") && new URL(r.url()).searchParams.get(fields[i]) === values[i]);
        await dialog.getByRole("button", { name: tr("logsUX.apply"), exact: true }).click();
        const result = await filtered;
        expect(result.status(), await result.text()).toBe(200);
        const resultBody = await result.json();
        expect(resultBody.total).toBe(1);
        expect(resultBody.data[0].request_id).toBe(calls[0]);
        await expect(panel.getByRole("row").filter({ hasText: calls[0] })).toBeVisible();
        await panel.getByRole("button", { name: tr("Reset Filters"), exact: true }).click();
      }
      await open.click();
      await dialog.getByText(tr("logsUX.advanced"), { exact: true }).click();
      await dialog.getByLabel(tr("Session ID"), { exact: true }).fill(sessions[0]);
      const sessionResult = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/spend/logs/ui") && new URL(r.url()).searchParams.get("session_id") === sessions[0]);
      await dialog.getByRole("button", { name: tr("logsUX.apply"), exact: true }).click();
      expect((await (await sessionResult).json()).data[0].request_id).toBe(calls[0]);
      await panel.getByRole("button", { name: tr("Reset Filters"), exact: true }).click();
      await open.click();
      const modelInput = dialog.getByLabel(tr("Model"), { exact: true });
      await modelInput.fill(prefix + "-missing");
      await expect(page.getByText(tr("logsUX.suggestionsEmpty"), { exact: true })).toBeVisible();
      // 组合框浮层会拦截首次外部点击，先以键盘关闭，再应用手工输入。
      await page.keyboard.press("Escape");
      const missing = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/spend/logs/ui") && new URL(r.url()).searchParams.get("model") === prefix + "-missing");
      await dialog.getByRole("button", { name: tr("logsUX.apply"), exact: true }).click();
      expect((await (await missing).json()).total).toBe(0);
      await expect(panel.getByText(tr("No matching requests"), { exact: true })).toBeVisible();
      await info.attach("log-filter-options-contract", { body: JSON.stringify({ locale, fields, scoped: true }, null, 2), contentType: "application/json" });
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
