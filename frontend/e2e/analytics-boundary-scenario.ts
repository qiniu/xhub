import { expect, test } from "@playwright/test";
import { matchesGatewayResponse } from "./gateway-response.cjs";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate, type Locale } from "../src/i18n/translate";
// C5独立验收清单，不能从服务端匹配表导入导致同错同过。
const routes = [
  "GET /global/activity/exceptions",
  "GET /global/activity/exceptions/deployment",
  "GET /global/all_end_users",
  "GET /global/spend",
  "GET /global/spend/report",
  "GET /key/spend/report",
  "GET /model/metrics",
  "GET /model/metrics/exceptions",
  "GET /model/metrics/slow_responses",
  "GET /model/streaming_metrics",
  "GET /organization/spend/report",
  "GET /otel-spans",
  "GET /spend/logs",
  "GET /spend_logs/end_users",
  "GET /spend_logs/users",
  "GET /tag/dau",
  "GET /tag/distinct",
  "GET /tag/mau",
  "GET /tag/summary",
  "GET /tag/user-agent/per-user-analytics",
  "GET /tag/wau",
  "GET /team/spend/report",
  "GET /user/spend/report",
  "POST /global/spend/refresh",
  "POST /global/spend/reset",
  "POST /team/{team_id}/member/{user_id}/reset_spend",
];
/** 注册旧统计边界双语流程；locale指定界面语言，无返回值。
 * 前置真实PG、浏览器及本地计费用上游；页面对话产生费用/日志，逐一验证旧统计明确失败且真实记录不变。
 * finally删除自建模型，运行器清理全部隔离schema；不使用生产账号或外部供应商。
 */
export function registerAnalyticsBoundary(locale: Locale) {
  test("analytics aliases preserve billed chat and logs " + locale, async ({ page }) => {
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const tr = (key: string) => translate(locale, key);
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    const model = "analytics-boundary-" + locale + "-" + Date.now();
    let modelID = "";
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: model,
          litellm_params: {
            model,
            custom_llm_provider: "openai",
            api_base: UPSTREAM,
            api_key: "sk-fake",
            input_cost_per_token: 0.01,
            output_cost_per_token: 0.01,
          },
          model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
        },
      });
      expect(created.status(), await created.text()).toBe(200);
      modelID = (await created.json()).model_info.id;
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: model, exact: true }).click();
      await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
      await page
        .getByRole("option")
        .filter({ hasText: /(?:^|\s)\/v1\/chat\/completions$/ })
        .click();
      const prompt = "Verify billed chat remains visible after unsupported analytics operations.";
      await page.getByTestId("chat-composer-input").fill(prompt);
      // 控制台按端点实现可能发带 /v1 或无 /v1 的同协议别名，均须观察真实请求。
      const pending = page.waitForResponse(
        (r) =>
          ["/v1/chat/completions", "/chat/completions"].some((path) =>
            matchesGatewayResponse(r.url(), r.request().method(), path, "POST"),
          ),
        { timeout: 15000 },
      );
      await page.getByTestId("chat-send-button").click();
      const chat = await pending;
      expect(chat.status()).toBe(200);
      await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
      await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
      const callID = chat.headers()["x-litellm-call-id"];
      expect(callID).toBeTruthy();
      const logURL = GATEWAY + "/spend/logs/ui/" + callID;
      await expect.poll(async () => (await page.request.get(logURL, { headers })).status()).toBe(200);
      const original = await (await page.request.get(logURL, { headers })).json();
      expect(original.spend).toBeGreaterThan(0);
      const before = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(before.status()).toBe(200);
      const calls = await before.json();
      for (const route of routes) {
        const [method, template] = route.split(" ");
        const path = template.replace(/\{[^}]+\}/g, "fixture");
        const r = await page.request.fetch(GATEWAY + path, { method, headers, data: { private_input: "do-not-echo" } });
        expect(r.status(), route + ": " + (await r.text())).toBe(501);
        const iam = path.startsWith("/team/") || path.startsWith("/organization/");
        expect(await r.json()).toMatchObject({
          error: { type: iam ? "unsupported_iam_operation" : "unsupported_analytics_operation" },
        });
        if (!iam) expect(r.headers()["cache-control"]).toBe("no-store");
        expect(await r.text()).not.toContain("do-not-echo");
      }
      const after = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(after.status()).toBe(200);
      expect(await after.json()).toEqual(calls);
      const preserved = await page.request.get(logURL, { headers });
      expect(preserved.status()).toBe(200);
      expect(await preserved.json()).toEqual(original);
      await stableGoto(page, "/logs");
      await page.getByPlaceholder(tr("Search logs by ID…"), { exact: true }).fill(callID);
      const row = page.getByRole("row").filter({ hasText: callID });
      await expect(row).toBeVisible();
      await row.click();
      await expect(page.getByRole("dialog")).toContainText(model);
      // 详情以log_id保存在URL；重新进入集合页后再刷新，避免把详情对话框当作表格。
      await stableGoto(page, "/logs");
      await page.reload();
      await expect(page.getByPlaceholder(tr("Search logs by ID…"), { exact: true })).toBeVisible();
      await page.getByPlaceholder(tr("Search logs by ID…"), { exact: true }).fill(callID);
      await expect(page.getByRole("row").filter({ hasText: callID })).toBeVisible();
      // 旧页面不再读取假统计，双语说明链接进入真实用量页。
      await stableGoto(page, "/old-usage");
      await expect(page.getByRole("status")).toHaveText(tr("legacyUsage.unavailable"));
      await page.getByRole("link", { name: tr("legacyUsage.openUsage"), exact: true }).click();
      await expect(page.getByTestId("daily-spend-settled")).toBeVisible();
    } finally {
      if (modelID)
        expect(
          (await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status(),
          "删除测试模型",
        ).toBe(200);
    }
  });
}
