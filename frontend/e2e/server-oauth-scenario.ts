import { expect, test } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册旧server授权边界的双语浏览器流程；locale指定页面语言，无返回值。
 * 前置真实网关、隔离数据库和本地上游；验证七个授权入口拒绝且不破坏普通配置，页面会话和真实推理仍可用。
 * 不伪造HTTP响应；finally删除临时server，运行器清理专属schema；不连接外部OAuth供应商。
 */
export function registerServerOAuthBoundary(locale: Locale) {
  test("legacy server OAuth rejects false success and preserves chat " + locale, async ({ page }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const id = "server-oauth-e2e-" + locale;
    let created = false;
    try {
      const creation = await page.request.post(GATEWAY + "/server", { headers, data: { server_id: id, server_name: id, note: "original" } });
      expect(creation.status()).toBe(200);
      created = true;
      const before = await page.request.get(GATEWAY + "/server/" + id, { headers });
      expect(before.status()).toBe(200);
      const saved = await before.json();
      for (const [method, path] of [
        ["POST", "/server/oauth/session"], ["GET", `/server/oauth/${id}/authorize`],
        ["POST", `/server/oauth/${id}/register`], ["POST", `/server/oauth/${id}/token`],
        ["POST", `/server/${id}/oauth-user-credential`], ["DELETE", `/server/${id}/oauth-user-credential`],
        ["GET", `/server/${id}/oauth-user-credential/status`],
      ]) {
        const result = await page.request.fetch(GATEWAY + path, { method, headers, data: { access_token: "private-e2e-fixture", code: "private-e2e-code" } });
        expect(result.status(), method + path + "未执行授权不能伪报成功").toBe(501);
        expect(await result.json()).toMatchObject({ error: { type: "unsupported_operation" } });
        expect(await result.text()).not.toContain("private-e2e");
        expect(result.headers()["cache-control"]).toBe("no-store");
        expect(result.headers()["location"]).toBeUndefined();
        expect(result.headers()["set-cookie"]).toBeUndefined();
      }
      const unchanged = await page.request.get(GATEWAY + "/server/" + id, { headers });
      expect(unchanged.status(), "删除授权凭据不能删除普通配置").toBe(200);
      expect(await unchanged.json()).toEqual(saved);
      expect((await page.request.put(GATEWAY + "/server", { headers, data: { server_id: id, note: "updated" } })).status()).toBe(200);
      const updated = await page.request.get(GATEWAY + "/server/" + id, { headers });
      expect(await updated.json()).toMatchObject({ note: "updated", server_name: id });
      expect((await page.request.get(GATEWAY + "/auth/me", { headers })).status()).toBe(200);
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
      await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
      await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/chat\/completions$/ }).click();
      const beforeCalls = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(beforeCalls.status()).toBe(200);
      const count = (await beforeCalls.json()).requests;
      const prompt = "Verify rejected authorization leaves this conversation available.";
      await page.getByTestId("chat-composer-input").fill(prompt);
      const inference = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/chat/completions") && response.request().method() === "POST");
      await page.getByTestId("chat-send-button").click();
      expect((await inference).status()).toBe(200);
      await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
      await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
      const afterCalls = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect((await afterCalls.json()).requests, "页面对话必须真正抵达本地上游").toBe(count + 1);
    } finally {
      if (created) {
        expect((await page.request.delete(GATEWAY + "/server/" + id, { headers })).status(), "临时server清理失败不能算通过").toBe(200);
        expect((await page.request.get(GATEWAY + "/server/" + id, { headers })).status()).toBe(404);
      }
    }
  });
}
