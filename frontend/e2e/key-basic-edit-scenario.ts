import { randomUUID } from "node:crypto";
import { expect, test } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册双语Key真实编辑流程；locale选择语言，隔离数据库/真实网关/本地上游为前置。
 * 验证仅修改名称不请求Skill接口、不提交隐藏字段，读回权限预算不变，允许推理和拒绝越权均真实生效。
 * finally按唯一别名回收Key，失败不能视为通过；不访问生产或外部插件服务。
 */
export function registerKeyBasicEdit(locale: Locale) {
  test("basic key editor preserves permissions and budget " + locale, async ({ page }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const alias = "Original 用户-" + randomUUID();
    const updatedAlias = "Edited 用户-" + randomUUID();
    let id = "";
    try {
      const made = await page.request.post(GATEWAY + "/key/generate", { headers, data: {
        key_alias: alias, models: ["gpt-4o-mini"], allowed_routes: ["llm_api_routes"],
        max_budget: 10, rpm_limit: 600, tpm_limit: 100000, duration: "1h",
      } });
      expect(made.status(), await made.text()).toBe(200);
      const key = await made.json(); id = key.token_id;
      const beforeResponse = await page.request.get(GATEWAY + "/key/info?key=" + id, { headers });
      expect(beforeResponse.status()).toBe(200);
      const before = (await beforeResponse.json()).info;
      const skillRequests: string[] = [];
      page.on("request", req => {
        if (/\/(claude-code|v1\/skills|public\/skill_hub)(\/|$)/.test(new URL(req.url()).pathname)) skillRequests.push(req.url());
      });
      await stableGoto(page, "/api-keys?key=" + id);
      await expect(page.getByRole("heading", { name: alias, exact: true })).toBeVisible();
      await expect(page.getByText(tr("Skills"), { exact: true })).toHaveCount(0);
      await page.getByLabel(tr("Key Alias"), { exact: true }).fill(updatedAlias);
      const saving = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/key/update") && r.request().method() === "POST");
      await page.getByRole("button", { name: tr("Save Changes"), exact: true }).click();
      const saved = await saving;
      expect(saved.status(), await saved.text()).toBe(200);
      const payload = saved.request().postDataJSON();
      expect(payload.key_alias).toBe(updatedAlias);
      for (const field of ["skills", "allowed_skills", "object_permission", "metadata", "allowed_routes", "duration"]) expect(payload).not.toHaveProperty(field);
      expect(skillRequests).toEqual([]);
      await page.reload();
      await expect(page.getByRole("heading", { name: updatedAlias, exact: true })).toBeVisible();
      const read = await page.request.get(GATEWAY + "/key/info?key=" + id, { headers });
      expect(read.status()).toBe(200);
      const after = (await read.json()).info;
      for (const field of ["models", "allowed_routes", "max_budget", "rpm_limit", "tpm_limit", "expires", "user_id", "team_id", "blocked"]) expect(after[field], "基本编辑不能改变 " + field).toEqual(before[field]);
      const callsBefore = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(callsBefore.status()).toBe(200);
      const count = (await callsBefore.json()).requests;
      const keyHeaders = { Authorization: "Bearer " + key.key };
      const denied = await page.request.post(GATEWAY + "/key/generate", { headers: keyHeaders, data: {} });
      expect(denied.status()).toBe(403);
      const disallowed = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: keyHeaders, data: { model: "not-allowed", messages: [{ role: "user", content: "Verify model restrictions remain enforced." }] } });
      expect(disallowed.status()).toBe(401);
      expect((await disallowed.json()).error.message).toBe("model not in allowed model list");
      const chat = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: keyHeaders, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Verify basic key editing leaves inference available." }] } });
      expect(chat.status(), await chat.text()).toBe(200);
      expect((await chat.json()).choices[0].message.content).toContain("e2e-ok");
      const callsAfter = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(callsAfter.status()).toBe(200);
      expect((await callsAfter.json()).requests).toBe(count + 1);
    } finally {
      // 响应解析失败也按本测试独有别名回收，不能因为未取得ID而留下孤儿Key。
      for (const name of [alias, updatedAlias]) {
        const listed = await page.request.get(GATEWAY + "/key/list?scope=personal&key_alias=" + encodeURIComponent(name), { headers });
        expect(listed.status()).toBe(200);
        for (const row of (await listed.json()).keys) {
          expect(row.key_alias).toBe(name);
          expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { key: row.token } })).status()).toBe(200);
        }
      }
      if (id) expect((await page.request.get(GATEWAY + "/key/info?key=" + id, { headers })).status()).toBe(404);
    }
  });
}
