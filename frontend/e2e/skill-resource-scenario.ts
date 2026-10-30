import { expect, test } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册中英文Skill资源边界流程；locale为界面语言，无返回值。真实管理员会话、隔离数据库和本地上游为前置。
 * 验证保留词详情只读、归档明确拒绝、正文冲突不误删以及真实页面对话；不模拟网关响应，不声称外部Skill安装可用。
 * finally删除创建的资源并检查404，运行器负责schema清理；清理失败不得算通过。
 */
export function registerSkillResourceBoundary(locale: Locale) {
  test("skill resource boundaries preserve configuration and chat " + locale, async ({ page }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const id = locale === "en" ? "new" : "register";
    const created: string[] = [];
    const otherID = "skill-other-" + locale;
    try {
      const creation = await page.request.post(GATEWAY + "/v1/skills", { headers, data: { skill_id: id, name: "skill-fixture", enabled: false, note: "original" } });
      expect(creation.status()).toBe(200);
      created.push(id);
      const other = await page.request.post(GATEWAY + "/v1/skills", { headers, data: { skill_id: otherID, name: "must-remain" } });
      expect(other.status()).toBe(200);
      created.push(otherID);
      const before = await page.request.get(GATEWAY + "/v1/skills/" + id, { headers });
      expect(before.status()).toBe(200);
      const saved = await before.json();
      expect(saved).toMatchObject({ skill_id: id, name: "skill-fixture", enabled: false, note: "original" });
      const listBefore = await page.request.get(GATEWAY + "/v1/skills", { headers });
      expect(listBefore.status()).toBe(200);
      const savedList = await listBefore.json();
      const manifest = await page.request.get(GATEWAY + "/v1/skills/" + id + "/archive", { headers });
      expect(manifest.status()).toBe(501);
      expect(await manifest.json()).toMatchObject({ error: { type: "unsupported_operation" } });
      const unchanged = await page.request.get(GATEWAY + "/v1/skills/" + id, { headers });
      expect(unchanged.status(), "读取或下载请求不能修改配置").toBe(200);
      expect(await unchanged.json()).toEqual(saved);
      const listAfter = await page.request.get(GATEWAY + "/v1/skills", { headers });
      expect(listAfter.status()).toBe(200);
      expect(await listAfter.json(), "读取或下载请求不能增加记录").toEqual(savedList);
      const deletion = await page.request.delete(GATEWAY + "/v1/skills/" + id, { headers, data: { skill_id: otherID, id: otherID, skillss: [otherID, id, id] } });
      expect(deletion.status()).toBe(200);
      expect(await deletion.json(), "详情删除必须只处理路径ID").toMatchObject({ deleted: 1, skill_id: id });
      expect((await page.request.get(GATEWAY + "/v1/skills/" + id, { headers })).status()).toBe(404);
      const kept = await page.request.get(GATEWAY + "/v1/skills/" + otherID, { headers });
      expect(kept.status(), "正文指定的无关Skill必须保留").toBe(200);
      expect(await kept.json()).toMatchObject({ skill_id: otherID, name: "must-remain" });
      expect((await page.request.get(GATEWAY + "/auth/me", { headers })).status()).toBe(200);
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
      await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
      await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/chat\/completions$/ }).click();
      const beforeCalls = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(beforeCalls.status()).toBe(200);
      const count = (await beforeCalls.json()).requests;
      const prompt = "Verify skill metadata leaves this conversation available.";
      await page.getByTestId("chat-composer-input").fill(prompt);
      const inference = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/chat/completions") && response.request().method() === "POST");
      await page.getByTestId("chat-send-button").click();
      expect((await inference).status()).toBe(200);
      await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
      await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
      const afterCalls = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect((await afterCalls.json()).requests, "页面对话必须真正抵达本地上游").toBe(count + 1);
    } finally {
      for (const id of created) {
        expect((await page.request.delete(GATEWAY + "/v1/skills/" + id, { headers })).status(), "临时Skill清理失败不能算通过").toBe(200);
        expect((await page.request.get(GATEWAY + "/v1/skills/" + id, { headers })).status()).toBe(404);
      }
    }
  });
}
