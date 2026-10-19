import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册输出 TPS 浏览器闭环；locale 为界面语言，无返回值。
 * 前置隔离网关、数据库、Redis 与分帧本地上游；验证实际调用、日志公式、监控样本和旧指标升级保持账单。
 * finally 恢复迁移状态并删除本场景模型/Key，原始调用由运行器删除私有 schema；不使用外部供应商。 */
export function registerModelMonitorTPSScenario(locale: "en" | "zh-CN") {
  test("output TPS ignores input and synchronous calls " + locale, async ({ page }, info) => {
    test.setTimeout(150_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    await page.getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true }).click();
    const tr = (key: string) => translate(locale, key);
    const model = "monitor-tps-" + locale + "-" + Date.now();
    const control = JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8")).control;
    let modelID = "", keyID = "", key = "", legacy = false;
    const calls: string[] = [];
    try {
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: model,
        litellm_params: { model, custom_llm_provider: "openai", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.000001, output_cost_per_token: 0.00001 },
        model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200); modelID = (await created.json()).model_info.id;
      const minted = await page.request.post(GATEWAY + "/key/generate", { headers, data: { key_alias: model } });
      expect(minted.status(), await minted.text()).toBe(200);
      const credential = await minted.json(); keyID = credential.token_id; key = credential.key;
      for (const stream of [true, false]) {
        const response = await page.request.post(GATEWAY + "/v1/chat/completions", {
          headers: { Authorization: "Bearer " + key },
          data: { model, stream, messages: [{ role: "user", content: "Verify output token generation speed." }] },
        });
        expect(response.status(), await response.text()).toBe(200);
        expect(await response.text()).toContain("e2e-ok");
        calls.push(response.headers()["x-litellm-call-id"]);
      }
      await expect.poll(async () => (await page.request.get(GATEWAY + "/spend/logs/ui/" + calls[1], { headers })).status(), { timeout: 80_000 }).toBe(200);
      const streamed = await (await page.request.get(GATEWAY + "/spend/logs/ui/" + calls[0], { headers })).json();
      const sync = await (await page.request.get(GATEWAY + "/spend/logs/ui/" + calls[1], { headers })).json();
      expect(streamed).toMatchObject({ streamed: true, prompt_tokens: 10000, completion_tokens: 10 });
      expect(sync).toMatchObject({ streamed: false, prompt_tokens: 10000, completion_tokens: 10, tps: null });
      const want = 10 * 1000 / (streamed.request_duration_ms - streamed.ttft_ms);
      expect(want).toBeGreaterThan(0);
      expect(streamed.tps).toBeCloseTo(want, 9);
      const query = GATEWAY + "/models/monitor?model=" + model;
      const before = await (await page.request.get(query, { headers })).json();
      expect(before.summary).toMatchObject({ requests: 2, total_tokens: 20020, tps_samples: 1 });
      expect(before.summary.avg_tps).toBeCloseTo(want, 9);
      expect(before.summary.cost).toBeCloseTo(0.0202, 8);
      expect((await page.request.get(control + "/monitor/tps/legacy")).status()).toBe(405);
      expect((await page.request.post(control + "/monitor/tps/legacy")).status()).toBe(200); legacy = true;
      const broken = await (await page.request.get(query, { headers })).json();
      expect(broken.summary.avg_tps).not.toBeCloseTo(want, 2);
      for (let index = 0; index < 2; index++) expect((await page.request.post(control + "/usage/migrate")).status()).toBe(200);
      legacy = false;
      const fixed = await (await page.request.get(query, { headers })).json();
      expect(fixed).toEqual(before);
      // 真实页面从新口径聚合读取数据；语言刷新后等待数据可交互，再选择测试模型。
      await stableGoto(page, "/model-monitor");
      await expect(page.getByRole("button", { name: tr("monitor.refresh"), exact: true })).toBeEnabled();
      await page.getByLabel(tr("monitor.model"), { exact: true }).selectOption(model);
      await page.getByLabel(tr("monitor.metric"), { exact: true }).selectOption("avg_tps");
      await expect(page.getByText(tr("monitor.measurement"), { exact: true })).toBeVisible();
      const row = page.getByRole("row").filter({ has: page.getByRole("rowheader", { name: model, exact: true }) });
      await expect(row).toHaveCount(1);
      await expect(row.getByRole("cell").nth(3)).toHaveText(want.toLocaleString(locale, { maximumFractionDigits: 2 }));
      await expect(page.getByRole("region", { name: tr("monitor.requests"), exact: true })).toContainText("2");
      await stableGoto(page, "/logs");
      await page.getByPlaceholder(tr("Search logs by ID…"), { exact: true }).fill(calls[0]);
      const logRow = page.getByRole("row").filter({ hasText: calls[0] });
      await expect(page.getByRole("columnheader", { name: tr("requestMetrics.tps"), exact: true })).toBeVisible();
      const tpsColumn = (await page.getByRole("columnheader").allTextContents()).findIndex(text => text.trim() === tr("requestMetrics.tps"));
      expect(tpsColumn).toBeGreaterThanOrEqual(0);
      // TTFT 和 TPS 共用说明，必须按可访问列名定位速度，不能仅用 tooltip 误选两列。
      await expect(logRow.getByRole("cell").nth(tpsColumn)).toHaveText(want.toFixed(2));
      await page.getByPlaceholder(tr("Search logs by ID…"), { exact: true }).fill(calls[1]);
      await expect(page.getByRole("row").filter({ hasText: calls[1] }).getByRole("cell").nth(tpsColumn)).toHaveText("—");
      await info.attach("output-tps-contract", { body: JSON.stringify({ streamed, sync, before, fixed }, null, 2), contentType: "application/json" });
    } finally {
      if (legacy) expect((await page.request.post(control + "/usage/migrate")).status()).toBe(200);
      if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status()).toBe(200);
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(200);
    }
  });
}
