import { expect, test } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";

import { translate } from "../src/i18n/translate";
/** 用途：注册独立场景「supplier model monitor displays real billing and switches chart and language」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC094() {


test("supplier model monitor displays real billing and switches chart and language", async ({ page }, info) => {
  // Redis 每分钟批量落库；允许真实刷新周期完成，避免以直接写库替代异步链路。
  test.setTimeout(120_000);
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  const suffix = Date.now(),
    actual = "monitor-actual-" + suffix;
  const suppliers = ["monitor-supplier-a-" + suffix, "monitor-supplier-b-" + suffix];
  const ids: string[] = [],
    created: string[] = [];
  let key = "";
  try {
    const minted = await page.request.post(GATEWAY + "/key/generate", {
      headers,
      data: { key_alias: "monitor-probe-" + suffix },
    });
    expect(minted.status(), await minted.text()).toBe(200);
    key = (await minted.json()).key;
    for (let index = 0; index < suppliers.length; index++) {
      const credential = await page.request.post(GATEWAY + "/credentials", {
        headers,
        data: {
          credential_name: suppliers[index],
          credential_info: { custom_llm_provider: "openai" },
          credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
        },
      });
      expect(credential.status(), await credential.text()).toBe(200);
      created.push(suppliers[index]);
      const model = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: "monitor-alias-" + index + "-" + suffix,
          litellm_params: {
            model: actual,
            litellm_credential_name: suppliers[index],
            custom_llm_provider: "openai",
            input_cost_per_token: 0.01,
            output_cost_per_token: 0.02,
          },
          model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
        },
      });
      expect(model.status(), await model.text()).toBe(200);
      ids.push((await model.json()).model_info.id);
    }
    // 配置已存在但尚未推理时，目录和下拉列表必须可用。
    await stableGoto(page, "/model-monitor");
    await expect(
      page.getByLabel(t("monitor.supplier"), { exact: true }).getByRole("option", { name: suppliers[0], exact: true }),
    ).toHaveCount(1);
    await page.getByLabel(t("monitor.supplier"), { exact: true }).selectOption(suppliers[0]);
    await page.getByLabel(t("monitor.model"), { exact: true }).selectOption(actual);
    await expect(page.getByRole("region", { name: t("monitor.requests") })).toContainText("0");
    await expect(page.getByRole("rowheader", { name: actual, exact: true })).toHaveCount(0);
    const requestIDs: string[] = [];
    for (let index = 0; index < suppliers.length; index++) {
      const completion = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key },
        data: {
          model: "monitor-alias-" + index + "-" + suffix,
          stream: index === 1,
          messages: [{ role: "user", content: "Monitor test prompt" }],
        },
      });
      expect(completion.status(), await completion.text()).toBe(200);
      requestIDs.push(completion.headers()["x-litellm-call-id"]);
    }
    await expect
      .poll(async () => {
        const res = await page.request.get(GATEWAY + "/models/monitor?model=" + actual, { headers });
        return res.ok() ? (await res.json()).summary.requests : -1;
      }, { timeout: 80_000 })
      .toBe(2);
    for (const id of requestIDs) {
      const logResponse = await page.request.get(GATEWAY + "/spend/logs/ui/" + id, { headers });
      expect(logResponse.ok()).toBeTruthy();
      expect(await logResponse.json()).toMatchObject({ inbound_endpoint: "/v1/chat/completions", outbound_endpoint: "/v1/chat/completions" });
    }
    await stableGoto(page, "/logs");
    await expect(page.getByRole("columnheader", { name: t("requestMetrics.endpoints"), exact: true })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: t("requestMetrics.tps"), exact: true })).toBeVisible();
    await expect(page.getByText("/v1/chat/completions", { exact: true }).first()).toBeVisible();
    const response = await page.request.get(GATEWAY + "/models/monitor?model=" + actual, { headers });
    const body = await response.json();
    expect(body.summary).toMatchObject({ requests: 2, total_tokens: 20 });
    expect(body.summary.cost).toBeCloseTo(0.24);
    expect(body.models).toHaveLength(2);
    expect(body.models.find((row: any) => row.supplier === suppliers[0]).ttft_samples).toBe(1);
    expect(body.models.find((row: any) => row.supplier === suppliers[1]).ttft_samples).toBe(1);
    await stableGoto(page, "/model-monitor");
    await expect(page.getByRole("heading", { name: t("monitor.title"), exact: true })).toBeVisible();
    await page.getByLabel(t("monitor.model"), { exact: true }).selectOption(actual);
    await expect(page.getByRole("rowheader", { name: actual, exact: true })).toHaveCount(2);
    await page.getByLabel(t("monitor.metric"), { exact: true }).selectOption("cost");
    await expect(page.getByRole("region", { name: t("monitor.trend") })).toBeVisible();
    await expect(page.getByRole("region", { name: t("monitor.trend") }).getByRole("application")).toBeVisible();
    await page.getByLabel(t("monitor.supplier"), { exact: true }).selectOption(suppliers[0]);
    const row = page.getByRole("row").filter({ has: page.getByRole("rowheader", { name: actual, exact: true }) });
    await expect(row).toHaveCount(1);
    await expect(row.getByRole("cell").nth(1)).toHaveText("1");
    await expect(row.getByRole("cell").nth(9)).toHaveText("$0.12");
    await page.getByRole("button", { name: "English", exact: true }).click();
    const en = (key: string) => translate("en", key);
    await expect(page.getByRole("heading", { name: en("monitor.title"), exact: true })).toBeVisible();
    await expect(page.getByLabel(en("monitor.supplier"), { exact: true })).toHaveValue(suppliers[0]);
    // 语言切换会刷新服务端组件；先完成真实重载并等待数据加载，避免未完成的刷新覆盖日期输入。
    await page.reload();
    await expect(page.getByRole("button", { name: en("monitor.refresh"), exact: true })).toBeEnabled();
    await page.getByLabel(en("monitor.start"), { exact: true }).fill("2026-02-01");
    await page.getByLabel(en("monitor.end"), { exact: true }).fill("2026-01-01");
    await page.getByRole("button", { name: en("monitor.refresh"), exact: true }).click();
    await expect(page.getByRole("main").getByRole("alert")).toHaveText(en("monitor.invalidRange"));
    await page.getByRole("button", { name: en("language.zhCN"), exact: true }).click();
    await expect(page.getByRole("heading", { name: t("monitor.title"), exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: t("monitor.refresh"), exact: true })).toBeEnabled();
    await page.getByLabel(t("monitor.start"), { exact: true }).fill("2026-02-01");
    await page.getByLabel(t("monitor.end"), { exact: true }).fill("2026-01-01");
    await page.getByRole("button", { name: t("monitor.refresh"), exact: true }).click();
    await expect(page.getByRole("main").getByRole("alert")).toHaveText(t("monitor.invalidRange"));
    expect((await page.request.get(GATEWAY + "/models/monitor?timezone=1", { headers })).status()).toBe(400);
    expect((await page.request.get(GATEWAY + "/models/monitor")).status()).toBe(401);
    await stableGoto(page, "/model-monitor");
    await expect(page.getByRole("heading", { name: t("monitor.title"), exact: true })).toBeVisible();
    await info.attach("monitor-contract", { body: JSON.stringify(body, null, 2), contentType: "application/json" });
    await info.attach("monitor-page", { body: await page.screenshot({ fullPage: true }), contentType: "image/png" });
  } finally {
    if (key)
      expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })).status()).toBe(200);
    for (const id of ids)
      expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    for (const name of created)
      expect((await page.request.delete(GATEWAY + "/credentials/" + name, { headers })).status()).toBe(200);
  }
});
}
