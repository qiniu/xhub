import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

type SignedResponse = { id: string; output: Record<string, unknown>[]; usage: { input_tokens: number; output_tokens: number } };

/** 读取JSON或SSE的完整Responses终态；参数为实际响应及模式，返回含原样output的对象。
 * SSE必须有completed且签名引用与item.done一致；不合格响应直接断言失败，无外部副作用。 */
function signedResponse(raw: string, stream: boolean): SignedResponse {
  if (!stream) return JSON.parse(raw) as SignedResponse;
  const events = raw.split("\n").filter(line => line.startsWith("data: ") && line !== "data: [DONE]").map(line => JSON.parse(line.slice(6)));
  const completed = events.filter(event => event.type === "response.completed");
  expect(completed).toHaveLength(1);
  const response = completed[0].response as SignedResponse;
  for (const item of response.output.filter(item => item.type === "reasoning" && item.encrypted_content)) {
    expect(events.filter(event => event.type === "response.output_item.done").map(event => event.item)).toContainEqual(item);
  }
  return response;
}

/** 注册中英文签名推理端到端场景；参数为语言，无返回值，供独立场景目录登记调用。 */
export function registerMessagesSignedHistoryScenario(locale: "zh-CN" | "en") {
  /** 前置真实浏览器、网关/PG及本地Messages夹具；页面编辑启用adaptive、创建有限额Key、两次工具续轮并核账。
   * 覆盖语言切换、JSON/SSE签名隐藏和原样回放、伪造及跨Key零外发；finally删除自建Key/模型，运行器回收schema。 */
  test("Messages signed reasoning and dependent tools " + locale, async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const tr = (key: string, values?: Record<string, string | number>) => translate(locale, key, values);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const model = "claude-signed-" + locale + "-" + Date.now();
    let modelID = "";
    const keyIDs: string[] = [];
    try {
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: model,
        litellm_params: { model: "e2e-claude-signed", custom_llm_provider: "anthropic", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_anthropic_messages", endpoint_types: ["responses", "messages"], pricing_source: "manual", max_input_tokens: 1024, max_output_tokens: 8192 },
      } });
      expect(created.status(), await created.text()).toBe(200);
      modelID = (await created.json()).model_info.id;
      await stableGoto(page, "/models-and-endpoints?model=" + encodeURIComponent(modelID));
      await page.getByRole("button", { name: tr("providerGuideUI.editModel"), exact: true }).click();
      const mode = page.getByLabel(tr("providerGuideUI.messagesReasoningMode"));
      await expect(mode).toHaveValue("disabled");
      await mode.selectOption("adaptive");
      await expect(page.getByText(tr("providerGuideUI.messagesReasoningHistory"), { exact: true })).toBeVisible();
      await expect(page.getByText(tr("providerGuideUI.messagesReasoningHelp"), { exact: true })).toBeVisible();
      // 在同一张表单切换语言，选项和值必须保留，帮助文案使用新语言及实际插值。
      const other = locale === "en" ? "zh-CN" : "en";
      await page.getByRole("button", { name: translate(locale, other === "en" ? "language.en" : "language.zhCN"), exact: true }).click();
      await expect(page.getByLabel(translate(other, "providerGuideUI.messagesReasoningMode"))).toHaveValue("adaptive");
      await expect(page.getByText(translate(other, "providerGuideUI.messagesReasoningHistory"), { exact: true })).toBeVisible();
      await page.getByRole("button", { name: translate(other, locale === "en" ? "language.en" : "language.zhCN"), exact: true }).click();
      await expect(page.getByLabel(tr("providerGuideUI.publicName"))).toHaveValue(model);
      await expect(page.getByRole("combobox", { name: tr("providerGuideUI.upstreamLabel"), exact: true })).toHaveValue("e2e-claude-signed");
      const saved = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/model/" + modelID + "/update") && response.request().method() === "PATCH");
      await page.getByRole("button", { name: tr("providerGuideUI.saveChanges"), exact: true }).click();
      const updated = await saved;
      expect(updated.status(), await updated.text()).toBe(200);
      expect(updated.request().postDataJSON().model_info.messages_reasoning_mode).toBe("adaptive");
      await page.getByRole("button", { name: tr("providerGuideUI.editModel"), exact: true }).click();
      await expect(page.getByLabel(tr("providerGuideUI.messagesReasoningMode"))).toHaveValue("adaptive");
      await stableGoto(page, "/api-keys");
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name")).fill(model);
      await page.getByLabel(tr("Max Budget (USD)"), { exact: true }).fill("1");
      const minted = page.waitForResponse(response => response.url().includes("/key/generate") && response.request().method() === "POST");
      await page.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
      const mintedResponse = await minted;
      expect(mintedResponse.status()).toBe(200);
      const key = await mintedResponse.json(); keyIDs.push(key.token_id);
      await page.keyboard.press("Escape");
      const otherResponse = await page.request.post(GATEWAY + "/key/generate", { headers, data: { key_alias: model + "-other", models: [model], max_budget: 1 } });
      expect(otherResponse.status(), await otherResponse.text()).toBe(200);
      const otherKey = await otherResponse.json(); keyIDs.push(otherKey.token_id);
      const keyHeaders = { Authorization: "Bearer " + key.key };
      const startCount = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
      let replay: Record<string, unknown>[] = [];
      for (const stream of [false, true]) {
        const history: Record<string, unknown>[] = [{ role: "user", content: "Read both synthetic values using lookup." }];
        for (let round = 1; round <= 3; round++) {
          // 前一轮完成后重建会话状态，浏览器续轮只能从真实数据库恢复签名历史。
          const control = JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8")).control;
          const restarted = await page.request.post(control + "/conversation/restart");
          expect(restarted.status(), await restarted.text()).toBe(200);
          const response = await page.request.post(GATEWAY + "/v1/responses", { headers: keyHeaders, data: {
            model, stream, max_output_tokens: 64, input: history, reasoning: { effort: "low", summary: "auto" }, parallel_tool_calls: true,
            tools: [{ type: "function", name: "lookup", strict: false, parameters: { type: "object", properties: { step: { type: "integer" }, previous: { type: "string" } } } }],
          } });
          const raw = await response.text();
          expect(response.status(), raw).toBe(200);
          expect(raw).not.toContain("PRIVATE_SIGNATURE_");
          expect(raw).not.toContain("PRIVATE_REDACTED_");
          const result = signedResponse(raw, stream);
          expect(result.usage).toMatchObject({ input_tokens: 8, output_tokens: 4 });
          if (round === 3) { expect(raw).toContain("VALUE_1|VALUE_2"); continue; }
          const reasoning = result.output.find(item => item.type === "reasoning");
          expect(reasoning?.encrypted_content).toEqual(expect.stringMatching(/^xhub_messages_v1:/));
          // Codex 会显式回传未设置的 content:null；保持签名引用与摘要原样。
          if (reasoning) reasoning.content = null;
          const calls = result.output.filter(item => item.type === "function_call");
          expect(calls).toHaveLength(1);
          expect(calls[0]).toMatchObject({ name: "lookup", call_id: "call_" + round });
          expect(JSON.parse(String(calls[0].arguments))).toEqual(round === 1 ? { step: 1 } : { step: 2, previous: "VALUE_1" });
          history.push(...result.output, { type: "function_call_output", call_id: "call_" + round, output: "VALUE_" + round });
          replay = [...history];
        }
      }
      const beforeDenied = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
      expect(beforeDenied - startCount).toBe(6);
      const base = { model, max_output_tokens: 64, reasoning: { effort: "low" } };
      const forged = await page.request.post(GATEWAY + "/v1/responses", { headers: keyHeaders, data: { ...base, input: [
        { type: "reasoning", encrypted_content: "xhub_messages_v1:" + "a".repeat(64), summary: [] }, { role: "user", content: "Continue." },
      ] } });
      expect(forged.status(), await forged.text()).toBe(400);
      const crossKey = await page.request.post(GATEWAY + "/v1/responses", { headers: { Authorization: "Bearer " + otherKey.key }, data: { ...base, input: replay } });
      expect(crossKey.status(), await crossKey.text()).toBe(400);
      expect((await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests).toBe(beforeDenied);
      for (const [id, spend] of [[key.token_id, 0.000096], [otherKey.token_id, 0]] as const) {
        await expect.poll(async () => {
          const info = (await (await page.request.get(GATEWAY + "/key/info?key=" + id, { headers })).json()).info;
          return { spend: Number(Number(info.spend).toFixed(9)), pending: Number(info.budget_pending || 0) };
        }).toEqual({ spend, pending: 0 });
      }
    } finally {
      for (const id of keyIDs) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [id] } })).ok()).toBeTruthy();
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).ok()).toBeTruthy();
    }
  });
}
