import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册日志精度和请求推理强度的双语闭环；locale 为界面语言，无返回值。
 * 前置真实网关、隔离数据库/Redis 和分帧本地上游；验证三协议调用、原始账单、未定价与免费状态、列表及详情/费用面板。
 * finally 删除自建模型与 Key，运行器删除私有 schema；不用生产数据或外部供应商凭据。 */
export function registerLogCostReasoningScenario(locale: "en" | "zh-CN") {
  test("log costs and reasoning effort " + locale, async ({ page }, info) => {
    test.setTimeout(150_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    await page.getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true }).click();
    const tr = (key: string) => translate(locale, key);
    const model = "monitor-tps-log-details-" + locale + "-" + Date.now();
    let modelID = "", keyID = "", key = "";
    const extraModelIDs: string[] = [];
    const observations: Record<string, unknown>[] = [];
    try {
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: model,
        litellm_params: { model, custom_llm_provider: "openai", api_base: UPSTREAM, api_key: "sk-fake",
          input_cost_per_token: 0.0000003, output_cost_per_token: 0.000008641 },
        model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200); modelID = (await created.json()).model_info.id;
      const minted = await page.request.post(GATEWAY + "/key/generate", { headers, data: { key_alias: model } });
      expect(minted.status(), await minted.text()).toBe(200);
      const credential = await minted.json(); keyID = credential.token_id; key = credential.key;
      for (const protocol of ["chat", "responses", "messages", "unspecified"] as const) {
        const stream = protocol === "chat" || protocol === "messages";
        let path = "/v1/chat/completions";
        const body: Record<string, unknown> = { model, stream,
          messages: [{ role: "user", content: "Verify exact log costs and request reasoning effort." }] };
        let effort = "high";
        if (protocol === "responses") {
          path = "/v1/responses"; delete body.messages;
          effort = "low"; body.input = "Verify exact log costs and request reasoning effort.";
          body.reasoning = { effort }; body.store = false;
        } else if (protocol === "messages") {
          path = "/v1/messages"; effort = "max"; body.max_tokens = 32; body.output_config = { effort };
        } else if (protocol === "chat") {
          body.reasoning_effort = effort;
        } else {
          effort = "";
        }
        const response = await page.request.post(GATEWAY + path, {
          headers: { Authorization: "Bearer " + key, "X-Session-Id": model + "-" + protocol }, data: body,
        });
        expect(response.status(), await response.text()).toBe(200);
        expect(await response.text()).toContain("e2e-ok");
        const callID = response.headers()["x-litellm-call-id"];
        await expect.poll(async () => (await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).status()).toBe(200);
        const detail = await (await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).json();
        expect(detail).toMatchObject({ reasoning_effort: effort, prompt_tokens: 10000, completion_tokens: 10 });
        expect(detail.spend).toBeCloseTo(0.00308641, 12);
        expect(detail.metadata.cost_breakdown.total_cost).toBeCloseTo(detail.spend, 12);
        await stableGoto(page, "/logs");
        await page.getByPlaceholder(tr("Search logs by ID…"), { exact: true }).fill(callID);
        const row = page.getByRole("row").filter({ hasText: callID });
        await expect(row).toContainText("$0.00308641");
        await row.click();
        const dialog = page.getByRole("dialog");
        await expect(dialog.getByText(tr("logs.reasoningEffort"), { exact: true })).toBeVisible();
        await expect(dialog.getByText(effort ? `${effort} (reasoning_effort=${effort})` : tr("logs.reasoningEffortUnknown"), { exact: true })).toBeVisible();
        await expect(dialog.getByText("$0.00308641", { exact: true }).first()).toBeVisible();
        await dialog.getByRole("button", { name: new RegExp(tr("Cost Breakdown")) }).click();
        await expect(dialog.getByRole("table")).toContainText("$0.00008641");
        const outputQuantity = translate(locale, "logs.completionTokenCount", { count: "10" });
        await expect(dialog.getByText(`(${outputQuantity} × $8.641 /1M)`, { exact: true })).toBeVisible();
        await expect(dialog.getByRole("table")).toContainText("$0.003");
        await expect(dialog.getByRole("table").getByText("$0.00", { exact: true })).toHaveCount(0);
        observations.push({ protocol, callID, effort, spend: detail.spend });
        await page.keyboard.press("Escape");
        await expect(dialog).not.toBeVisible();
      }
      // 未定价和显式免费都记 spend=0，必须走真实数据面才能证明页面没有把两者混同。
      for (const pricing of ["unpriced", "free"] as const) {
        const alias = model + "-" + pricing;
        const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
          model_name: alias,
          litellm_params: { model: "volcengine/" + alias, custom_llm_provider: "volcengine", api_base: UPSTREAM, api_key: "sk-fake",
            // 视频只报告输出 token；保留非零输入价满足手工定价校验，实际输出明确为零价。
            ...(pricing === "free" ? { input_cost_per_token: 0.000001, output_cost_per_token: 0 } : {}) },
          model_info: { transport: "ark_contents_generation", endpoint_types: ["bypass:ark-video"], ...(pricing === "free" ? { pricing_source: "manual" } : {}) },
        } });
        expect(created.status(), await created.text()).toBe(200);
        extraModelIDs.push((await created.json()).model_info.id);
        const dataHeaders = { Authorization: "Bearer " + key, "X-Session-Id": alias };
        const response = await page.request.post(GATEWAY + "/api/v3/contents/generations/tasks", {
          headers: dataHeaders,
          data: { model: alias, content: [{ type: "text", text: "Verify unknown versus free request pricing." }] },
        });
        expect(response.status(), await response.text()).toBe(200);
        const taskID = (await response.json()).id;
        const result = await page.request.get(GATEWAY + "/api/v3/contents/generations/tasks/" + taskID, { headers: dataHeaders });
        expect(result.status(), await result.text()).toBe(200);
        expect((await result.json()).status).toBe("succeeded");
        const callID = response.headers()["x-litellm-call-id"];
        await expect.poll(async () => (await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).status()).toBe(200);
        const detail = await (await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).json();
        expect(detail.spend).toBe(0);
        expect(detail.metadata.cost_breakdown.pricing_status).toBe(pricing === "free" ? "priced" : "unpriced");
        await stableGoto(page, "/logs");
        await page.getByPlaceholder(tr("Search logs by ID…"), { exact: true }).fill(callID);
        const row = page.getByRole("row").filter({ hasText: callID });
        await expect(row).toBeVisible();
        const costIndex = (await page.getByRole("columnheader").allTextContents()).findIndex((label) => label.trim() === tr("Cost"));
        expect(costIndex, "费用列应可通过翻译后的表头定位").toBeGreaterThanOrEqual(0);
        await expect(row.getByRole("cell").nth(costIndex)).toHaveText(pricing === "free" ? "$0.00" : "-");
        await row.click();
        const dialog = page.getByRole("dialog");
        await expect(dialog.getByText(tr("Cost"), { exact: true }).locator("..")).toContainText(pricing === "free" ? "$0.00" : "-");
        if (pricing === "unpriced") {
          await expect(dialog.getByText(tr("Pricing unavailable"), { exact: true })).toBeVisible();
          await expect(dialog.getByText("$0.00", { exact: true })).toHaveCount(0);
        } else {
          await expect(dialog.getByText("$0.00", { exact: true }).first()).toBeVisible();
        }
        observations.push({ pricing, callID, spend: detail.spend, breakdown: detail.metadata.cost_breakdown });
        await page.keyboard.press("Escape");
        await expect(dialog).not.toBeVisible();
      }
      await info.attach("log-cost-reasoning-contract", { body: JSON.stringify(observations, null, 2), contentType: "application/json" });
    } finally {
      if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status()).toBe(200);
      for (const id of extraModelIDs) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(200);
    }
  });
}
