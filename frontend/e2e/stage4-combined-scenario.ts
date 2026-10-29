import { expect, test } from "@playwright/test";
import { matchesGatewayResponse } from "./gateway-response.cjs";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate, type Locale } from "../src/i18n/translate";
// 独立交错选择五批入口；各批完整路径由各自 E2E 和后台回归覆盖。
const routes = [
  ["PATCH", "/update/ui_settings", "unsupported_settings_operation"],
  ["POST", "/model/block", "unsupported_model_operation"],
  ["POST", "/global/spend/reset", "unsupported_analytics_operation"],
  ["DELETE", "/fixture-provider/v1/files/fixture", "unsupported_operation"],
  ["GET", "/routes", "unsupported_discovery_operation"],
];
/** 注册五批候选组合验收；locale决定界面语言，返回注册结果。
 * 隔离PG及真实浏览器先读取供应商目录、发起计费对话，再交错拒绝旧操作并读回模型和日志。
 * 验证侧栏设置失败不登出、英文提示词与用户原文保留、真实用量仍可查看；finally删除模型，运行器清理schema。
 */
export function registerStage4Combined(locale: Locale) {
  test("stage4 combined boundaries preserve chat and logs " + locale, async ({ page }) => {
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const tr = (key: string) => translate(locale, key);
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    await stableGoto(page, "/search-tools");
    const picker = page.getByRole("combobox", { name: tr("probes.provider"), exact: true });
    for (const provider of ["tavily", "perplexity", "serper"]) {
      await picker.selectOption(provider);
      await expect(picker).toHaveValue(provider);
    }
    const model = "stage4-combined-" + locale + "-" + Date.now();
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
        .filter({ hasText: /\/v1\/chat\/completions$/ })
        .click();
      const prompt = "Verify billed chat remains visible after combined legacy operations.";
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
      const snapshot = await page.request.get(GATEWAY + "/v2/model/info", { headers });
      expect(snapshot.status()).toBe(200);
      const models = await snapshot.json();
      const before = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(before.status()).toBe(200);
      const calls = await before.json();
      for (const [method, path, kind] of routes) {
        const r = await page.request.fetch(GATEWAY + path, { method, headers, data: { private_input: "do-not-echo" } });
        expect(r.status(), method + " " + path + ": " + (await r.text())).toBe(501);

        expect(await r.json()).toMatchObject({
          error: { type: kind },
        });
        expect(r.headers()["cache-control"]).toBe("no-store");
        expect(await r.text()).not.toContain("do-not-echo");
      }
      const after = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(after.status()).toBe(200);
      expect(await after.json()).toEqual(calls);
      const preserved = await page.request.get(logURL, { headers });
      expect(preserved.status()).toBe(200);
      expect(await preserved.json()).toEqual(original);
      const preservedModels = await page.request.get(GATEWAY + "/v2/model/info", { headers });
      expect(preservedModels.status()).toBe(200);
      expect(await preservedModels.json()).toEqual(models);
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", { name: tr("pages.models.all"), exact: true }).click();
      await expect(page.getByText(model, { exact: true }).first()).toBeVisible();
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
