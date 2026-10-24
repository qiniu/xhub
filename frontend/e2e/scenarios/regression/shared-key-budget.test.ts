import { expect, test } from "@playwright/test";
import { GATEWAY, login, loginAdmin, sessionBearer, stableGoto } from "../../helpers";
import { translate } from "../../../src/i18n/translate";

// 双语完整流程：普通用户未建 Key 时看到个人预算，创建固定及两个空预算 Key，真实推理及控制台共享余额。
// 前置独立后台与离线上游；不触及生产账户，finally 删除用户及其 Key，运行器回收 schema。
for (const locale of ["en", "zh-CN"] as const)
  test("shared personal key budget " + locale, async ({ page }) => {
    test.setTimeout(120000);
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    const admin = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const email = `shared-${locale}@example.invalid`;
    const created = await page.request.post(GATEWAY + "/user/new", {
      headers: admin,
      data: { user_email: email, password: "shared-test-password", user_role: "user", max_budget: 500 },
    });
    expect(created.status(), await created.text()).toBe(200);
    const userID = (await created.json()).user_id;
    try {
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      await login(page, email, "shared-test-password", locale);
      await expect(page.getByTestId("create-key-button")).toBeVisible();
      const session = { Authorization: "Bearer " + (await sessionBearer(page)) };
      await stableGoto(page, "/usage");
      await expect(
        page.getByText(translate(locale, "{value0} limit", { value0: "$500.0000" }), { exact: true }),
      ).toBeVisible();
      const tokens: string[] = [];
      const ids: string[] = [];
      for (const [name, budget] of [
        ["fixed", 500],
        ["blank-one", null],
        ["blank-two", null],
      ] as const) {
        await stableGoto(page, "/api-keys");
        await page.getByTestId("create-key-button").click();
        const dialog = page.getByRole("dialog");
        await dialog.getByLabel(tr("Key Name")).fill(name);
        await expect(dialog.getByRole("region", { name: tr("quotaGuide.personalTitle") })).toContainText("$500.00");
        if (budget !== null) await dialog.getByLabel(tr("Max Budget (USD)")).fill(String(budget));
        const response = page.waitForResponse(
          (r) => r.url().endsWith("/key/generate") && r.request().method() === "POST",
        );
        await dialog.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
        const r = await response;
        expect(r.status(), await r.text()).toBe(200);
        const key = await r.json();
        tokens.push(key.key);
        ids.push(key.token_id);
        expect(key.max_budget).toBe(budget);
        await page.keyboard.press("Escape");
      }
      // 真实列表只暴露记录ID；两个空额度Key独立存在且共享个人额度，不能回显创建时明文。
      const listed = await page.request.get(GATEWAY + "/key/list?scope=personal", { headers: session });
      expect(listed.status()).toBe(200);
      const list = await listed.json();
      expect(list.total_count).toBe(3);
      for (const row of list.keys) {
        expect(ids).toContain(row.token_id);
        expect(row.token).toBe(row.token_id);
        expect(row.key).toBeUndefined();
        expect(tokens).not.toContain(row.token);
      }
      const before = await page.request.get(GATEWAY + "/v2/user/info", { headers: session });
      expect((await before.json()).user_info).toMatchObject({ max_budget: 500, spend: 0 });
      for (const token of tokens) {
        const response = await page.request.post(GATEWAY + "/v1/chat/completions", {
          headers: { Authorization: "Bearer " + token },
          data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Verify shared budget" }] },
        });
        expect(response.status(), await response.text()).toBe(200);
      }
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: /gpt-4o-mini/ }).click();
      await page.getByTestId("chat-composer-input").fill("Verify console shares personal budget");
      await page.getByTestId("chat-send-button").click();
      await expect(page.getByTestId("message-surface").filter({ hasText: /e2e-ok/ })).toBeVisible();
      await expect(
        page.getByTestId("message-surface").filter({ hasText: "Verify console shares personal budget" }),
      ).toBeVisible();
    } finally {
      expect(
        (await page.request.post(GATEWAY + "/user/delete", { headers: admin, data: { user_id: userID } })).status(),
      ).toBe(200);
    }
  });
