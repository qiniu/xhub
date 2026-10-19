import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册旧库升级的真实浏览器闭环；locale 为界面语言，无返回值。
 * 前置专属数据库、测试控制端及本地上游；验证历史账单保留、缺列错误、迁移后推理和日志/用量/监控恢复。
 * 重复迁移必须不重复计费；finally 恢复列并删除自建模型和密钥，运行器最终删除私有 schema。 */
export function registerUsageUpgradeScenario(locale: "en" | "zh-CN") {
  test("legacy usage upgrade restores observability " + locale, async ({ page }, info) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true }).click();
    const tr = (key: string) => translate(locale, key);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const control = JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8")).control;
    const model = "usage-upgrade-" + locale + "-" + Date.now();
    let modelID = "", key = "", legacy = false;
    const calls: string[] = [];
    /** 发送一次真实推理并等待账单落库；参数 source 为显式会话名，返回日志详情。
     * 只使用本地上游及本测试密钥，响应正文和计费均经真实网关；数据最终随私有 schema 回收。 */
    const call = async (source: string) => {
      const response = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key, "X-Session-Id": source },
        data: { model, reasoning_effort: "high", messages: [{ role: "user", content: "Verify usage migration recovery." }] },
      });
      expect(response.status(), await response.text()).toBe(200);
      expect((await response.json()).choices[0].message.content).toBe("e2e-ok");
      const id = response.headers()["x-litellm-call-id"];
      expect(id).toBeTruthy(); calls.push(id);
      await expect.poll(async () => (await page.request.get(GATEWAY + "/spend/logs/ui/" + id, { headers })).status()).toBe(200);
      return (await page.request.get(GATEWAY + "/spend/logs/ui/" + id, { headers })).json();
    };
    try {
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: model,
        litellm_params: { model, custom_llm_provider: "openai", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.01, output_cost_per_token: 0.02 },
        model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200); modelID = (await created.json()).model_info.id;
      const minted = await page.request.post(GATEWAY + "/key/generate", { headers, data: { key_alias: model } });
      expect(minted.status(), await minted.text()).toBe(200); key = (await minted.json()).key;
      const historical = await call(model + "-historical");
      expect(historical.session_source).toBe("explicit");
      expect(historical.reasoning_effort).toBe("high");
      expect((await page.request.get(control + "/usage/legacy")).status()).toBe(405);
      expect((await page.request.post(control + "/usage/legacy")).status()).toBe(200); legacy = true;
      expect((await page.request.get(GATEWAY + "/spend/logs/ui/" + calls[0], { headers })).status()).toBe(500);
      expect((await page.request.post(control + "/usage/migrate")).status()).toBe(200); legacy = false;
      const preserved = await page.request.get(GATEWAY + "/spend/logs/ui/" + calls[0], { headers });
      expect(preserved.status()).toBe(200);
      expect(historical.spend).toBeCloseTo(0.12, 8);
      expect(await preserved.json()).toMatchObject({ request_id: calls[0], session_source: "", reasoning_effort: "", spend: historical.spend });
      const current = await call(model + "-current");
      expect(current.session_source).toBe("explicit");
      expect(current.reasoning_effort).toBe("high");
      for (let index = 0; index < 2; index++) expect((await page.request.post(control + "/usage/migrate")).status()).toBe(200);
      const monitor = await page.request.get(GATEWAY + "/models/monitor?model=" + model, { headers });
      expect(monitor.status()).toBe(200);
      const metrics = await monitor.json();
      expect(metrics.summary).toMatchObject({ requests: 2, total_tokens: 20 });
      expect(metrics.summary.cost).toBeCloseTo(0.24, 8);
      expect((await page.request.get(GATEWAY + "/guardrails/usage/overview", { headers })).status()).toBe(200);
      await stableGoto(page, "/logs");
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).fill(calls[1]);
      const row = page.getByRole("row").filter({ hasText: calls[1] });
      await expect(row).toBeVisible(); await row.click();
      await expect(page.getByRole("dialog")).toContainText(model);
      const loaded = page.waitForResponse(r => new URL(r.url()).pathname === "/user/daily/activity/aggregated");
      await stableGoto(page, "/usage");
      const activity = await loaded; expect(activity.status()).toBe(200);
      const metadata = (await activity.json()).metadata;
      expect(metadata.total_api_requests).toBeGreaterThanOrEqual(2);
      for (const [title, field] of [["Total Requests", "total_api_requests"], ["Total Tokens", "total_tokens"]]) {
        const heading = page.getByRole("heading", { name: tr(title), exact: true }).first();
        await expect(heading.locator("xpath=ancestor::*[p][1]").locator(":scope > p")).toHaveText(metadata[field].toLocaleString());
      }
      await stableGoto(page, "/model-monitor");
      await page.getByLabel(tr("monitor.model"), { exact: true }).selectOption(model);
      await expect(page.getByRole("rowheader", { name: model, exact: true })).toHaveCount(1);
      await expect(page.getByRole("region", { name: tr("monitor.requests"), exact: true })).toContainText("2");
      await info.attach("usage-upgrade-contract", { body: JSON.stringify({ calls, metrics, metadata }, null, 2), contentType: "application/json" });
    } finally {
      if (legacy) expect((await page.request.post(control + "/usage/migrate")).status()).toBe(200);
      if (key) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })).status()).toBe(200);
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(200);
    }
  });
}
