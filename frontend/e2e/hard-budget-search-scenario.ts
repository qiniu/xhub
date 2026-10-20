import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册独立语言的搜索预算闭环；由场景文件调用，资源在测试finally清理。 */
export function registerHardBudgetSearchScenario(locale: "zh-CN" | "en") {
  /** 在双语控制台创建有限额Key，经真实后台与本地上游完成同步/流搜索并检查费用；余额不足阻断。
   * 前置隔离数据库和明确搜索计价部署；finally删除自建Key和模型，不使用生产凭据。 */
  test("budgeted hosted search preserves tools and settles " + locale, async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const tr = (key: string) => translate(locale, key);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const name = "budget-search-browser-" + locale + "-" + Date.now();
    let modelID = "", keyID = "";
    try {
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: name,
        litellm_params: { model: name, custom_llm_provider: "openai", api_base: UPSTREAM + "/v1", api_key: "sk-fake", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002, search_context_cost_per_query: 0.01 },
        model_info: { transport: "bypass_openai_responses", endpoint_types: ["responses"], pricing_source: "manual", max_input_tokens: 1024, max_output_tokens: 8192, web_search_support: "supported", web_search_budget_profile: "openai_responses_search_actions", web_search_budget_source: "Local fixture guarantees cumulative input and response output bounds and per-search pricing.", web_search_budget_input_tokens: 2048, web_search_budget_max_calls: 1 },
      }});
      expect(created.status(), await created.text()).toBe(200);
      modelID = (await created.json()).model_info.id;
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name")).fill(name);
      await page.getByLabel(tr("Max Budget (USD)"), { exact: true }).fill("0.1");
      const minted = page.waitForResponse(r => r.url().includes("/key/generate") && r.request().method() === "POST");
      await page.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
      const key = await (await minted).json(); keyID = key.token_id;
      await page.keyboard.press("Escape");
      for (const stream of [false, true]) {
        const response = await page.request.post(GATEWAY + "/v1/responses", { headers: { Authorization: "Bearer " + key.key }, data: { model: name, input: "Search for the official Go website.", tools: [{ type: "web_search" }], tool_choice: "required", max_output_tokens: 32, stream } });
        expect(response.status(), await response.text()).toBe(200);
        expect(await response.text()).toContain("web_search_call");
        expect(response.headers()["x-xhub-max-tool-calls"]).toBe("1");
        expect(response.headers()["x-xhub-dropped-capabilities"]).toBeUndefined();
      }
      await expect.poll(async () => (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info.spend).toBeCloseTo(0.020024, 8);
      await stableGoto(page, "/api-keys?key=" + keyID);
      await expect(page.getByLabel(tr("Max Budget (USD)"), { exact: true })).toHaveValue("0.1");
      await page.getByLabel(tr("Max Budget (USD)"), { exact: true }).fill("0.025");
      const saving = page.waitForResponse(r => r.url().includes("/key/update") && r.request().method() === "POST");
      await page.getByRole("button", { name: tr("Save Changes"), exact: true }).click();
      expect((await saving).status()).toBe(200);
      const denied = await page.request.post(GATEWAY + "/v1/responses", { headers: { Authorization: "Bearer " + key.key }, data: { model: name, input: "Search again.", tools: [{ type: "web_search" }], max_output_tokens: 32 } });
      expect(denied.status()).toBe(429);
      expect((await denied.json()).error.type).toBe("budget_reservation_exceeded");
    } finally {
      if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).ok()).toBeTruthy();
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).ok()).toBeTruthy();
    }
  });
}
