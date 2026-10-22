import { expect, test, type Page } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 构造新会话或工具发现续轮；参数为公开模型/历史/流式开关，返回独立正文，模拟客户端输入，不改变用户原文。 */
function referenceBody(model: string, history: boolean, stream: boolean): Record<string, unknown> {
  const messages: Record<string, unknown>[] = [{ role: "user", content: "Find and run lookup." }];
  if (history) messages.push(
    { role: "assistant", content: [{ type: "tool_use", id: "search-1", name: "ToolSearch", input: { query: "lookup" } }] },
    { role: "user", content: [{ type: "tool_result", tool_use_id: "search-1", content: [
      { type: "tool_reference", tool_name: "lookup" }, { type: "text", text: "保留用户原文" },
    ] }] },
  );
  return { model, max_tokens: 64, stream, messages, output_config: { effort: "high", future_option: true },
    tools: [{ name: "ToolSearch", input_schema: { type: "object" } }, { name: "lookup", input_schema: { type: "object" }, defer_loading: true }] };
}

/** 从真实浏览器经同源代理请求后台；参数为页面/Key/正文/路径，返回状态、完整正文与日志ID；不拦截或模拟网络。 */
async function postReference(page: Page, key: string, body: Record<string, unknown>, path = "/gw/v1/messages") {
  return page.evaluate(async ({ key, body, path }) => {
    const reply = await fetch(path, { method: "POST", headers: { Authorization: "Bearer " + key, "Content-Type": "application/json" }, body: JSON.stringify(body) });
    return { status: reply.status, raw: await reply.text(), id: reply.headers.get("x-litellm-call-id"), contentType: reply.headers.get("content-type") };
  }, { key, body, path });
}

/** 注册指定语言的原生扩展端到端场景；返回无，隔离Key/模型/账单由场景finally和运行器清理。 */
export function registerMessagesReferenceScenario(locale: "zh-CN" | "en") {
  /** 前置真实浏览器、后台、PG和严格本地上游；页面创建有限额Key，验证新旧会话JSON/SSE、字段/原文、日志与账单。
   * 同时复现未声明Bypass的model not found并显式启用后验证成功；跨协议失败不外发，finally删除数据。 */
  test("Messages tool references and native options " + locale, async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const tr = (key: string) => translate(locale, key);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const model = "claude-reference-" + locale + "-" + Date.now();
    const ids: string[] = [];
    const calls: string[] = [];
    let keyID = "";
    try {
      for (const chat of [false, true]) {
        const saved = await page.request.post(GATEWAY + "/model/new", { headers, data: {
          model_name: model + (chat ? "-chat" : ""),
          litellm_params: { model: "e2e-claude-reference", custom_llm_provider: chat ? "openai" : "anthropic", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
          model_info: { transport: chat ? "bypass_openai_chat" : "bypass_anthropic_messages", endpoint_types: ["messages"], pricing_source: "manual", max_input_tokens: 1024, max_output_tokens: 8192 },
        } });
        expect(saved.status(), await saved.text()).toBe(200);
        ids.push((await saved.json()).model_info.id);
      }
      await stableGoto(page, "/api-keys");
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name")).fill(model);
      await page.getByLabel(tr("Max Budget (USD)"), { exact: true }).fill("1");
      const minted = page.waitForResponse(reply => reply.url().includes("/key/generate") && reply.request().method() === "POST");
      await page.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
      const created = await minted;
      expect(created.status()).toBe(200);
      const key = await created.json(); keyID = key.token_id;
      await page.keyboard.press("Escape");
      for (const history of [false, true]) for (const stream of [false, true]) {
        const reply = await postReference(page, key.key, referenceBody(model, history, stream));
        expect(reply.status, reply.raw).toBe(200);
        expect(reply.raw).toContain("REFERENCE_OK");
        expect(reply.contentType).toContain(stream ? "text/event-stream" : "application/json");
        if (stream) expect(reply.raw).toContain("message_stop");
        expect(reply.id).toBeTruthy(); calls.push(reply.id!);
      }
      const before = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
      const bridgeBody = referenceBody(model + "-chat", true, false);
      delete bridgeBody.output_config; delete bridgeBody.tools;
      const bridge = await postReference(page, key.key, bridgeBody);
      expect(bridge.status, bridge.raw).toBe(400);
      expect(bridge.raw).toContain("model_unavailable");
      const bypassPath = "/gw/bypass/anthropic/v1/messages";
      const disabled = await postReference(page, key.key, referenceBody(model, true, false), bypassPath);
      expect(disabled.status, disabled.raw).toBe(400);
      expect(disabled.raw).toContain("model not found");
      expect((await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests).toBe(before);
      const enabled = await page.request.patch(GATEWAY + "/model/" + ids[0] + "/update", { headers, data: { model_info: { endpoint_types: ["messages", "bypass:anthropic-messages"] } } });
      expect(enabled.status(), await enabled.text()).toBe(200);
      const native = await postReference(page, key.key, referenceBody(model, true, true), bypassPath);
      expect(native.status, native.raw).toBe(200);
      expect(native.raw).toContain("REFERENCE_OK");
      calls.push(native.id!);
      for (const id of calls) await expect.poll(async () => {
        const bill = await (await page.request.get(GATEWAY + "/spend/logs/ui/" + id, { headers })).json();
        return { status: bill.status, spend: Number(bill.spend), input: bill.prompt_tokens, output: bill.completion_tokens };
      }).toEqual({ status: "success", spend: 0.000012, input: 8, output: 2 });
      await expect.poll(async () => {
        const info = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info;
        return { spend: Number(Number(info.spend).toFixed(6)), pending: Number(info.budget_pending || 0) };
      }).toEqual({ spend: 0.000060, pending: 0 });
      await stableGoto(page, "/logs");
      await expect(page.getByText(model, { exact: true }).first()).toBeVisible();
      await stableGoto(page, "/api-keys?key=" + keyID);
      await expect(page.getByText(translate(locale, "hardBudget.pending", { amount: 0 }), { exact: true })).toBeVisible();
    } finally {
      if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status()).toBe(200);
      for (const id of ids) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    }
  });
}
