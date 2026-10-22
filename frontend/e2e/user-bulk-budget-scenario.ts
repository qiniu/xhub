import { expect, test, type Page } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 从真实浏览器用自建Key请求本地模型；参数为页面、Key和模型名，返回HTTP状态和正文，仅产生本例账单。 */
async function callBulkBudgetModel(page: Page, key: string, model: string) {
  return page.evaluate(
    async ({ key, model }) => {
      // 走控制台代理到真实网关，不能用路由mock代替预算执行链。
      // eslint-disable-next-line no-restricted-syntax
      const response = await fetch("/gw/v1/chat/completions", {
        method: "POST",
        headers: { Authorization: "Bearer " + key, "Content-Type": "application/json" },
        body: JSON.stringify({
          model,
          messages: [{ role: "user", content: "Verify the user budget." }],
          max_tokens: 8,
        }),
      });
      return { status: response.status, text: await response.text() };
    },
    { key, model },
  );
}

/** 注册双语用户批量预算完整流程；locale为语言，无返回值。
 * 前置私有schema、真实网关和可控上游；浏览器选择/保存/失败/取消上限，检查持久化、Key拦截与计费。
 * finally恢复非本例夹具的原预算并删除自建用户/模型，运行器最终清理整个schema和合成账单。 */
export function registerUserBulkBudgetScenario(locale: "en" | "zh-CN") {
  test("atomic bulk user budgets control real API keys " + locale, async ({ page }) => {
    test.setTimeout(150_000);
    await loginAdmin(page);
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    const tr = (key: string, params?: Record<string, string | number>) => translate(locale, key, params);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const prefix = "e2e-bulk-" + locale + "-" + Date.now();
    const users: { user_id: string; user_email: string }[] = [];
    let modelID = "",
      keyID = "",
      key = "",
      spent = 0;
    const model = prefix + "-model";
    let restore: { user_id: string; max_budget: number | null }[] = [];
    /** 读取真实个人预算和消费；参数为ID，返回后台账户快照，HTTP异常中止测试，不修改数据。 */
    const read = async (id: string) => {
      const response = await page.request.get(GATEWAY + "/user/info?user_id=" + id, { headers });
      expect(response.status(), await response.text()).toBe(200);
      return (await response.json()).user_info;
    };
    /** 刷新列表并按邮箱选择本例用户；无参数，返回批量对话框，确保页面保存后的新数据而非旧组件状态。 */
    const openBulk = async () => {
      await stableGoto(page, "/users");
      await page.getByRole("button", { name: tr("pages.users.selectUsers"), exact: true }).click();
      for (const user of users)
        await page.getByRole("row").filter({ hasText: user.user_email }).getByRole("checkbox").check();
      await page.getByRole("button", { name: tr("pages.users.bulkEditCount", { count: 2 }), exact: true }).click();
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByText(tr("pages.users.bulkBudgetHelp"))).toBeVisible();
      await expect(dialog.getByLabel(tr("pages.users.bulkBudgetAction"))).toHaveValue("");
      await expect(dialog.getByRole("button", { name: tr("pages.users.saveChanges"), exact: true })).toBeDisabled();
      return dialog;
    };
    /** 通过可访问表单提交一次预算命令；参数为set/clear、额度和预期状态，返回真实JSON响应，失败不关闭表单。 */
    const save = async (mode: "set" | "clear", value: string, status = 200) => {
      const dialog = page.getByRole("dialog");
      await dialog.getByLabel(tr("pages.users.bulkBudgetAction")).selectOption(mode);
      if (mode === "set") await dialog.getByLabel(tr("pages.users.maxBudget"), { exact: true }).fill(value);
      const saved = page.waitForResponse(
        (r) => r.url().endsWith("/user/bulk_update") && r.request().method() === "POST",
      );
      await dialog.getByRole("button", { name: tr("pages.users.saveChanges"), exact: true }).click();
      const response = await saved;
      expect(response.status(), await response.text()).toBe(status);
      if (status === 200) await expect(dialog).toHaveCount(0);
      return response.json();
    };
    try {
      const configured = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: model,
          litellm_params: {
            model: "gpt-4o-mini",
            api_base: UPSTREAM + "/v1",
            api_key: "sk-fake",
            custom_llm_provider: "openai",
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
          model_info: {
            transport: "bypass_openai_chat",
            endpoint_types: ["chat"],
            pricing_source: "manual",
            max_input_tokens: 1024,
            max_output_tokens: 8192,
            supported_parameters: ["max_tokens"],
          },
        },
      });
      expect(configured.status(), await configured.text()).toBe(200);
      modelID = (await configured.json()).model_info.id;
      for (const suffix of ["first", "second"]) {
        const created = await page.request.post(GATEWAY + "/user/new", {
          headers,
          data: {
            user_email: prefix + "-" + suffix + "@example.invalid",
            user_alias: "原始用户-" + suffix,
            password: "bulk-budget-password",
            user_role: "user",
          },
        });
        expect(created.status(), await created.text()).toBe(200);
        users.push(await created.json());
      }
      // 实际单用户编辑入口也必须保存并回读月度日期，不能只验证仓库中未挂载的旧组件。
      await stableGoto(page, "/users");
      await page.getByTestId(`user-actions-${users[0].user_id}`).click();
      await page.getByRole("menuitem", { name: tr("pages.users.editUser"), exact: true }).click();
      await page.getByLabel(tr("userBudgetReset.label")).selectOption("15");
      const singleSaved = page.waitForResponse(r => r.url().endsWith("/user/update") && r.request().method() === "POST");
      await page.getByRole("button", { name: tr("pages.users.saveChanges"), exact: true }).click();
      expect((await singleSaved).status()).toBe(200);
      expect((await read(users[0].user_id)).budget_reset_day).toBe(15);
      await expect(page.getByText(tr("userBudgetReset.day", { day: 15 }), { exact: true })).toBeVisible();
      await page.reload();
      await page.getByRole("button", { name: tr("pages.users.editUser"), exact: true }).click();
      await expect(page.getByLabel(tr("userBudgetReset.label"))).toHaveValue("15");
      await page.getByRole("button", { name: tr("common.cancel"), exact: true }).click();
      const minted = await page.request.post(GATEWAY + "/key/generate", {
        headers,
        data: { user_id: users[0].user_id, key_alias: prefix },
      });
      expect(minted.status(), await minted.text()).toBe(200);
      const mintedKey = await minted.json();
      key = mintedKey.key;
      keyID = mintedKey.token_id;
      // 双语页面只改刷新日期，额度必须保持null；再取消日期，刷新时间清空且原始用户名称保留。
      for (const day of ["31", "0"]) {
        const periodDialog = await openBulk();
        await periodDialog.getByLabel(tr("userBudgetReset.label")).selectOption(day);
        const changed = page.waitForResponse(r => r.url().endsWith("/user/bulk_update") && r.request().method() === "POST");
        await periodDialog.getByRole("button", { name: tr("pages.users.saveChanges"), exact: true }).click();
        expect((await changed).status()).toBe(200);
        await expect(periodDialog).toHaveCount(0);
        for (const user of users) {
          const value = await read(user.user_id);
          expect(value.max_budget).toBeNull();
          expect(value.budget_reset_day).toBe(Number(day));
          if (day === "0") expect(value.budget_reset_at).toBeNull();
          else expect(value.budget_reset_at).toBeTruthy();
          expect(value.user_alias).toContain("原始用户");
        }
      }
      const initialDialog = await openBulk();
      await initialDialog.getByLabel(tr("pages.users.bulkBudgetAction")).selectOption("set");
      await initialDialog.getByLabel(tr("pages.users.maxBudget"), { exact: true }).fill("-1");
      await initialDialog.getByRole("button", { name: tr("pages.users.saveChanges"), exact: true }).click();
      await expect(initialDialog.getByRole("alert")).toHaveText(tr("pages.users.bulkBudgetInvalid"));
      for (const user of users) expect((await read(user.user_id)).max_budget).toBeNull();
      const zero = await save("set", "0");
      expect(zero).toMatchObject({ atomic: true, successful_updates: 2, failed_updates: 0, total_requested: 2 });
      for (const user of users) expect((await read(user.user_id)).max_budget).toBe(0);
      const denied = await callBulkBudgetModel(page, key, model);
      expect(denied.status, denied.text).toBe(429);
      await openBulk();
      await save("set", "10");
      for (const user of users) expect((await read(user.user_id)).max_budget).toBe(10);
      // 直接检查真实HTTP坏数值契约，不能让JSON解码溢出变成清除额度。
      const overflow = await page.request.post(GATEWAY + "/user/bulk_update", {
        headers: { ...headers, "Content-Type": "application/json" },
        data: `{"users":[{"user_id":${JSON.stringify(users[0].user_id)},"max_budget":1e309}]}`,
      });
      expect(overflow.status()).toBe(400);
      expect((await overflow.json()).error.type).toBe("bulk_budget_invalid");
      for (const user of users) expect((await read(user.user_id)).max_budget).toBe(10);
      // 单用户/Key入口也必须拒绝部分JSON，不能让授权管理员的坏请求取消上限或覆盖别名。
      expect(
        (await page.request.post(GATEWAY + "/key/update", { headers, data: { key: keyID, max_budget: 10 } })).status(),
      ).toBe(200);
      for (const target of [
        { path: "/user/update", idField: "user_id", id: users[0].user_id, aliasField: "user_alias" },
        { path: "/key/update", idField: "key", id: keyID, aliasField: "key_alias" },
      ]) {
        const head = `{${JSON.stringify(target.idField)}:${JSON.stringify(target.id)},${JSON.stringify(target.aliasField)}:"must-not-save","max_budget":`;
        for (const suffix of ["1e309}", "-1e309}", "null} {}", "{} }"]) {
          const rejected = await page.request.post(GATEWAY + target.path, {
            headers: { ...headers, "Content-Type": "application/json" },
            data: head + suffix,
          });
          expect(rejected.status(), await rejected.text()).toBe(400);
          expect((await rejected.json()).error.type).toBe("invalid_request");
        }
      }
      const unchangedUser = await read(users[0].user_id);
      expect(unchangedUser.max_budget).toBe(10);
      expect(unchangedUser.user_alias).toBe("原始用户-first");
      const keyRead = await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers });
      expect(keyRead.status()).toBe(200);
      const unchangedKey = (await keyRead.json()).info;
      expect(unchangedKey.max_budget).toBe(10);
      expect(unchangedKey.key_alias).toBe(prefix);
      // 恢复Key共享上限，后续批量降低失败应由真实消费触发，避免固定Key分配掩盖消费断言。
      expect(
        (
          await page.request.post(GATEWAY + "/key/update", { headers, data: { key: keyID, max_budget: null } })
        ).status(),
      ).toBe(200);
      await page.reload();
      await expect(page.getByRole("row").filter({ hasText: users[0].user_email })).toContainText("10.00");
      const allowed = await callBulkBudgetModel(page, key, model);
      expect(allowed.status, allowed.text).toBe(200);
      expect(JSON.parse(allowed.text).choices[0].message.content).toContain("ok");
      await expect
        .poll(async () => Number((await read(users[0].user_id)).spend), { timeout: 15_000 })
        .toBeGreaterThan(0);
      spent = Number((await read(users[0].user_id)).spend);
      await openBulk();
      const failure = await save("set", "0", 400);
      expect(failure.error.type).toBe("bulk_budget_allocation");
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByRole("alert")).toHaveText(tr("pages.users.bulkBudgetAllocation"));
      await expect(dialog.getByLabel(tr("pages.users.maxBudget"), { exact: true })).toHaveValue("0");
      for (const user of users) expect((await read(user.user_id)).max_budget).toBe(10);
      await save("clear", "");
      for (const user of users) expect((await read(user.user_id)).max_budget).toBeNull();
      expect(Number((await read(users[0].user_id)).spend)).toBe(spent);
      expect((await read(users[0].user_id)).user_alias).toBe("原始用户-first");
      // 全部范围由界面显式选择；仅在独立schema执行，并保存夹具预算供finally恢复。
      const listed = await page.request.get(GATEWAY + "/user/list?page=1&page_size=500", { headers });
      expect(listed.status()).toBe(200);
      const existing = (await listed.json()).users;
      restore = existing
        .filter((u: any) => !users.some((own) => own.user_id === u.user_id))
        .map((u: any) => ({ user_id: u.user_id, max_budget: u.max_budget ?? null }));
      const allDialog = await openBulk();
      await allDialog.getByRole("checkbox", { name: tr("pages.users.updateAllUsers"), exact: true }).check();
      await expect(allDialog.getByText(tr("pages.users.bulkBudgetAllWarning", { count: 1000 }))).toBeVisible();
      const all = await save("clear", "");
      expect(all.total_requested).toBe(existing.length);
      expect(all.successful_updates).toBe(existing.length);
      expect(all.results.map((u: any) => u.user_id).sort()).toEqual(existing.map((u: any) => u.user_id).sort());
      await page.reload();
      for (const user of users) {
        await expect(page.getByRole("row").filter({ hasText: user.user_email })).toContainText(
          tr("pages.users.unlimited"),
        );
        expect((await read(user.user_id)).max_budget).toBeNull();
      }
      expect(Number((await read(users[0].user_id)).spend)).toBe(spent);
      const auditResponse = await page.request.get(GATEWAY + "/audit/logs?action=user.bulk_budget&page_size=100", {
        headers,
      });
      expect(auditResponse.status()).toBe(200);
      const audits = (await auditResponse.json()).audit_logs;
      expect(audits.filter((audit: any) => audit.detail.batch_id === zero.batch_id)).toHaveLength(2);
    } finally {
      if (restore.length)
        expect(
          (await page.request.post(GATEWAY + "/user/bulk_update", { headers, data: { users: restore } })).status(),
        ).toBe(200);
      for (const user of users)
        expect(
          (await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: user.user_id } })).status(),
        ).toBe(200);
      if (key)
        expect(
          (await page.request.get(GATEWAY + "/v1/models", { headers: { Authorization: "Bearer " + key } })).status(),
        ).toBe(401);
      if (modelID)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(
          200,
        );
    }
  });
}
