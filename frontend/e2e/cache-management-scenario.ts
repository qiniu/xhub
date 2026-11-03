import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { waitForLog } from "./product-fixes-scenario";
import { translate, type Locale } from "../src/i18n/translate";

/** 注册真实缓存页面与数据面验收；locale为语言，返回无。前置隔离网关、PG及本地模型。
 * 验证不支持设置、命中零费用、页面清理后未命中及真实401失败恢复。仅改请求头注入失败，不mock响应。
 * finally移除路由并真实清理缓存；登录/日志由运行器严格删除私有schema。
 */
export function registerCacheManagement(locale: Locale) {
  test("cache management reflects runtime " + locale, async ({ page }) => {
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const tr = (key: string) => translate(locale, "cacheAdmin." + key);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const model = "cache-management-" + crypto.randomUUID();
    const body = { model, messages: [{ role: "user", content: "Reply with CACHE_OK." }] };
    let key = "";
    try {
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: model,
        litellm_params: { model: "cache-management-upstream", api_base: UPSTREAM, api_key: "sk-fake", deployment_id: model, custom_llm_provider: "custom", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { id: model, transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200);
      const teams = await (await page.request.get(GATEWAY + "/v2/team/list?page=1&page_size=500", { headers })).json();
      const team = teams.teams.find((row: { team_alias: string }) => row.team_alias === "e2e-fixture-team");
      const jwt = (await page.context().cookies()).find((cookie) => cookie.name === "token")!.value;
      const userID = JSON.parse(Buffer.from(jwt.split(".")[1], "base64url").toString()).user_id;
      const minted = await page.request.post(GATEWAY + "/key/generate", { headers, data: { key_alias: model, key_type: "llm_api", team_id: team.team_id, user_id: userID } });
      expect(minted.status(), await minted.text()).toBe(200);
      key = (await minted.json()).key;
      const inferenceHeaders = { Authorization: "Bearer " + key };
      await stableGoto(page, "/caching");
      await expect(page.getByText(tr("ready"), { exact: true })).toBeVisible();
      await expect(page.getByText(tr("scope"))).toBeVisible();
      await expect(page.getByText(tr("unsupported"))).toBeVisible();
      await expect(page.getByRole("textbox")).toHaveCount(0);
      for (const path of ["/cache/settings", "/cache/settings/test", "/delete"]) {
        const denied = await page.request.post(GATEWAY + path, { headers, data: { type: "redis", enabled: false, keys: ["unused"] } });
        expect(denied.status(), path + await denied.text()).toBe(501);
        expect(await denied.json()).toMatchObject({ error: { type: "unsupported_operation" } });
      }
      for (let index = 0; index < 2; index++) {
        const response = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: inferenceHeaders, data: body });
        expect(response.status(), await response.text()).toBe(200);
        expect((await response.json()).choices[0].message.content).toContain("e2e-ok");
        expect(response.headers()["x-litellm-cache-hit"] === "true").toBe(index === 1);
        const row = await waitForLog(page.request, headers, response.headers()["x-litellm-call-id"]);
        const cost = Number(row.spend);
        if (index === 1) expect(cost).toBe(0); else expect(cost).toBeGreaterThan(0);
      }
      // 修改请求鉴权以触发真实后台401，保留浏览器登录会话；不伪造成功或失败响应。
      await page.route("**/flushall", (route) => route.continue({ headers: { ...route.request().headers(), authorization: "Bearer invalid-cache-test" } }));
      await page.getByRole("button", { name: tr("flush"), exact: true }).click();
      await expect(page.getByRole("alert").filter({ hasText: tr("flushFailed") })).toBeVisible();
      const preserved = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: inferenceHeaders, data: body });
      expect(preserved.status(), await preserved.text()).toBe(200);
      expect(preserved.headers()["x-litellm-cache-hit"]).toBe("true");
      await page.unroute("**/flushall");
      await page.getByRole("button", { name: tr("flush"), exact: true }).click();
      await expect(page.getByTestId("cache-result")).toHaveText(tr("flushed"));
      const fresh = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: inferenceHeaders, data: body });
      expect(fresh.status(), await fresh.text()).toBe(200);
      expect(fresh.headers()["x-litellm-cache-hit"]).not.toBe("true");
      expect(Number((await waitForLog(page.request, headers, fresh.headers()["x-litellm-call-id"])).spend)).toBeGreaterThan(0);
      await page.getByRole("button", { name: locale === "en" ? "中文" : "English", exact: true }).click();
      const other = locale === "en" ? "zh-CN" : "en";
      await expect(page.getByTestId("cache-result")).toHaveText(translate(other, "cacheAdmin.flushed"));
    } finally {
      await page.unroute("**/flushall");
      const cleanup = await page.request.post(GATEWAY + "/flushall", { headers, data: {} });
      expect(cleanup.status(), "缓存清理失败: " + await cleanup.text()).toBe(200);
      if (key) {
        const removed = await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } });
        expect(removed.status(), await removed.text()).toBe(200);
      }
      const removed = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: model } });
      expect(removed.status(), await removed.text()).toBe(200);
    }
  });
}
