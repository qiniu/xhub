import { expect, test } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册插件列表筛选的双语浏览器流程；locale指定页面语言，无返回值。
 * 前置真实网关、隔离数据库和本地上游；验证真实筛选、错误参数及配置不被读取修改，页面会话和真实推理仍可用。
 * 不伪造HTTP响应；finally删除临时插件，运行器清理专属schema；不连接外部插件服务。
 */
export function registerPluginFilterBoundary(locale: Locale) {
  test("plugin list filters configuration and preserves chat " + locale, async ({ page }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const id = "plugin-filter-e2e-" + locale;
    let created = false;
    try {
      const creation = await page.request.put(GATEWAY + "/claude-code/plugins/" + id, { headers, data: { id, name: id, enabled: false, note: "original" } });
      expect(creation.status()).toBe(200);
      created = true;
      const before = await page.request.get(GATEWAY + "/claude-code/plugins/" + id, { headers });
      expect(before.status()).toBe(200);
      const saved = await before.json();
      expect(saved.enabled).toBe(false);
      const listBefore = await page.request.get(GATEWAY + "/claude-code/plugins", { headers });
      expect(listBefore.status()).toBe(200);
      const savedList = await listBefore.json();
      const all = await page.request.get(GATEWAY + "/claude-code/plugins?enabled_only=false", { headers });
      expect(all.status()).toBe(200);
      expect((await all.json()).plugins.some((row: { id: string }) => row.id === id)).toBe(true);
      const filtered = await page.request.get(GATEWAY + "/claude-code/plugins?enabled_only=true", { headers });
      expect(filtered.status()).toBe(200);
      const enabled = await filtered.json();
      expect(enabled.count).toBe(enabled.plugins.length);
      expect(enabled.plugins.some((row: { id: string }) => row.id === id)).toBe(false);
      expect((await page.request.get(GATEWAY + "/claude-code/plugins?enabled_only=invalid", { headers })).status()).toBe(400);
      const unchanged = await page.request.get(GATEWAY + "/claude-code/plugins/" + id, { headers });
      expect(await unchanged.json()).toEqual(saved);
      const listAfter = await page.request.get(GATEWAY + "/claude-code/plugins", { headers });
      expect(await listAfter.json(), "筛选读取不能修改配置").toEqual(savedList);
      // 此处仅通过现有配置API改标记并回读，不宣称执行了插件启用。
      expect((await page.request.put(GATEWAY + "/claude-code/plugins/" + id, { headers, data: { enabled: true } })).status()).toBe(200);
      const marked = await page.request.get(GATEWAY + "/claude-code/plugins?enabled_only=true", { headers });
      expect((await marked.json()).plugins.some((row: { id: string }) => row.id === id)).toBe(true);
      expect((await page.request.put(GATEWAY + "/claude-code/plugins/" + id, { headers, data: { id, note: "updated" } })).status()).toBe(200);
      const updated = await page.request.get(GATEWAY + "/claude-code/plugins/" + id, { headers });
      expect(await updated.json()).toMatchObject({ note: "updated", name: id });
      expect((await page.request.get(GATEWAY + "/auth/me", { headers })).status()).toBe(200);
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
      await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
      await page.getByRole("option").filter({ hasText: /\/v1\/chat\/completions$/ }).click();
      const beforeCalls = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(beforeCalls.status()).toBe(200);
      const count = (await beforeCalls.json()).requests;
      const prompt = "Verify plugin list filtering leaves this conversation available.";
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
        expect((await page.request.delete(GATEWAY + "/claude-code/plugins/" + id, { headers })).status(), "临时插件清理失败不能算通过").toBe(200);
        expect((await page.request.get(GATEWAY + "/claude-code/plugins/" + id, { headers })).status()).toBe(404);
      }
    }
  });
}
