import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册双语 Claude 客户端兼容场景；参数为语言，无返回值，供独立场景文件调用。 */
export function registerClaudeClientCompatScenario(locale: "zh-CN" | "en") {
  /** 浏览器创建有限额Key并调用原生/桥接API，核对文本、费用和错误；本地上游检查真实出站字段。
   * 不依赖付费凭据，finally删除自建Key和模型，运行器回收账单与隔离schema。 */
  test("Claude keep-all, public history and Chat terminal " + locale, async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const tr = (key: string) => translate(locale, key);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const model = "claude-client-" + locale + "-" + Date.now();
    let modelID = "", keyID = "";
    try {
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: model,
        litellm_params: { model: "e2e-claude-client", custom_llm_provider: "anthropic", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002, cache_creation_input_token_cost: 0.000003, cache_creation_input_token_cost_above_1hr: 0.000004 },
        model_info: { transport: "bypass_anthropic_messages", endpoint_types: ["messages", "chat", "responses"], pricing_source: "manual", max_input_tokens: 1024, max_output_tokens: 8192 },
      } });
      expect(created.status(), await created.text()).toBe(200);
      modelID = (await created.json()).model_info.id;
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name")).fill(model);
      await page.getByLabel(tr("Max Budget (USD)"), { exact: true }).fill("1");
      const minted = page.waitForResponse(r => r.url().includes("/key/generate") && r.request().method() === "POST");
      await page.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
      const key = await (await minted).json(); keyID = key.token_id;
      await page.keyboard.press("Escape");
      const keyHeaders = { Authorization: "Bearer " + key.key };
      for (const stream of [false, true]) {
        for (const protocol of ["messages", "chat", "responses"]) {
          const body: Record<string, unknown> = { model, stream };
          let endpoint = "/v1/messages";
          if (protocol === "messages") {
            Object.assign(body, { max_tokens: 64, messages: [{ role: "user", content: "Reply exactly CLAUDE_COMPAT_OK." }], context_management: { edits: [{ type: "clear_thinking_20251015", keep: "all" }] } });
          } else if (protocol === "chat") {
            endpoint = "/v1/chat/completions";
            Object.assign(body, { max_tokens: 64, messages: [
              { role: "user", content: "Start a public reasoning history." },
              { role: "assistant", content: "Ready.", reasoning_content: "Public plan.", reasoning_items: [{ type: "reasoning", id: "rs_public_browser", summary: [{ type: "summary_text", text: "Public plan." }] }] },
              { role: "user", content: "Reply exactly CLAUDE_COMPAT_OK." },
            ] });
          } else {
            endpoint = "/v1/responses";
            Object.assign(body, { max_output_tokens: 64, input: "Reply exactly CLAUDE_COMPAT_OK.", reasoning: { effort: "none" }, parallel_tool_calls: false, prompt_cache_key: "synthetic-hint", text: { format: { type: "text" } }, tools: [{ type: "function", name: "lookup", strict: false, parameters: { type: "object", properties: {} } }] });
          }
          const response = await page.request.post(GATEWAY + endpoint, { headers: keyHeaders, data: body });
          const text = await response.text();
          expect(response.status(), text).toBe(200);
          expect(text).toContain("CLAUDE_COMPAT_OK");
          if (protocol === "chat") {
            expect(text).toContain('"finish_reason":"stop"');
            expect(text).not.toContain('"end_turn"');
          }
        }
      }
      await expect.poll(async () => (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info.spend).toBeCloseTo(6 * 0.000012, 8);
      for (const stream of [false, true]) {
        const cached = await page.request.post(GATEWAY + "/v1/messages", { headers: keyHeaders, data: { model, stream, max_tokens: 64, messages: [{ role: "user", content: "Verify automatic cache." }] } });
        expect(cached.status(), await cached.text()).toBe(200);
        expect(await cached.text()).toContain("CLAUDE_COMPAT_OK");
      }
      // 普通8输入+2输出，再加2个5m和3个1h缓存写入，单次应扣0.000030且释放所有预占。
      await expect.poll(async () => {
        const info = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info;
        return { spend: Number(Number(info.spend).toFixed(9)), pending: Number(info.budget_pending || 0) };
      }).toEqual({ spend: 0.000132, pending: 0 });
      const rejected = await page.request.post(GATEWAY + "/v1/messages", { headers: keyHeaders, data: { model, max_tokens: 64, messages: [{ role: "user", content: "Hello" }], context_management: { edits: [{ type: "compact_20260112" }] } } });
      expect(rejected.status()).toBe(400);
      expect(await rejected.text()).toContain("keep-all");
      await stableGoto(page, "/api-keys?key=" + keyID);
      await expect(page.getByLabel(tr("Max Budget (USD)"), { exact: true })).toHaveValue("1");
    } finally {
      if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).ok()).toBeTruthy();
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).ok()).toBeTruthy();
    }
  });
}
