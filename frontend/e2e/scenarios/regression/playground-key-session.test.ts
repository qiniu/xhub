import { expect, test } from "@playwright/test";
import { GATEWAY, login, loginAdmin, sessionBearer, stableGoto } from "../../helpers";
import { translate } from "../../../src/i18n/translate";

// 双语真实登录与网关：错误自定义Key返回401后仍保留会话，刷新后切回会话可完成推理。
// 前置隔离数据库与本地上游；finally删除新建用户和其Key，运行器回收schema，不依赖生产SSO。
for (const locale of ["en", "zh-CN"] as const) {
  test("invalid playground key preserves login " + locale, async ({ page }) => {
    test.setTimeout(120000);
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    const admin = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const email = `session-${locale}@example.invalid`;
    const created = await page.request.post(GATEWAY + "/user/new", {
      headers: admin,
      data: { user_email: email, password: "session-test-password", user_role: "user", max_budget: 500 },
    });
    expect(created.status(), await created.text()).toBe(200);
    const userID = (await created.json()).user_id;
    try {
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      await login(page, email, "session-test-password", locale);
      await expect(page.getByTestId("create-key-button")).toBeVisible();
      const bearer = await sessionBearer(page);
      await stableGoto(page, "/playground");
      await page.getByText(tr("myModels.connectionSettings"), { exact: true }).click();
      await page.getByRole("combobox", { name: tr("Virtual Key Source") }).click();
      await page.getByRole("option", { name: tr("Virtual Key"), exact: true }).click();
      const rejected = page.waitForResponse((r) => r.url().includes("/model/available") && r.status() === 401);
      const verified = page.waitForResponse((r) => r.url().endsWith("/auth/me") && r.status() === 200);
      await page.getByPlaceholder(tr("Enter custom Virtual Key")).fill("sk-invalid-session-regression");
      await rejected;
      await verified;
      expect(await sessionBearer(page)).toBe(bearer);
      await expect(page).toHaveURL(/\/playground/);
      const reverified = page.waitForResponse((r) => r.url().endsWith("/auth/me") && r.status() === 200);
      await page.reload();
      await reverified;
      await expect(page).toHaveURL(/\/playground/);
      expect(await sessionBearer(page)).toBe(bearer);
      // 保留调试台设置，仅撤销登录并重新使用表单登录，覆盖本次生产复现路径。
      const logout = await page.request.post(GATEWAY + "/auth/logout", {
        headers: { Authorization: "Bearer " + bearer },
      });
      expect(logout.status()).toBe(200);
      await page.context().clearCookies();
      await page.evaluate(() => sessionStorage.removeItem("token"));
      await stableGoto(page, "/login");
      await page.getByPlaceholder(tr("login.usernamePlaceholder")).fill(email);
      await page.getByPlaceholder(tr("login.passwordPlaceholder")).fill("session-test-password");
      await page.getByPlaceholder(tr("login.passwordPlaceholder")).press("Enter");
      await expect(page.getByTestId("create-key-button")).toBeVisible();
      const reloginVerified = page.waitForResponse((r) => r.url().endsWith("/auth/me") && r.status() === 200);
      await stableGoto(page, "/playground");
      await reloginVerified;
      await expect(page).toHaveURL(/\/playground/);
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await expect(page.getByText(tr("Unable to load models for this key"), { exact: true })).toBeVisible();
      await page.keyboard.press("Escape");
      await page.getByText(tr("myModels.connectionSettings"), { exact: true }).click();
      await expect(page.getByPlaceholder(tr("Enter custom Virtual Key"))).toHaveValue("sk-invalid-session-regression");
      await page.getByRole("combobox", { name: tr("Virtual Key Source") }).click();
      const recovered = page.waitForResponse((r) => r.url().includes("/model/available") && r.status() === 200);
      await page.getByRole("option", { name: tr("Current UI Session"), exact: true }).click();
      await recovered;
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: /gpt-4o-mini/ }).click();
      await page.getByTestId("chat-composer-input").fill("Verify login survives an invalid custom key");
      await page.getByTestId("chat-send-button").click();
      await expect(page.getByTestId("message-surface").filter({ hasText: /e2e-ok/ })).toBeVisible();
      await expect(
        page.getByTestId("message-surface").filter({ hasText: "Verify login survives an invalid custom key" }),
      ).toBeVisible();
    } finally {
      const deleted = await page.request.post(GATEWAY + "/user/delete", { headers: admin, data: { user_id: userID } });
      expect(deleted.status(), await deleted.text()).toBe(200);
    }
  });
}
