import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "../../helpers";

/** 前置无 Redis 的真实网关、私有数据库和本地供应商；从 Playground 发起唯一推理，暂时拒绝账单写入后恢复。
 * 验证页面成功、待结算快照、日志和模型监控唯一入账；finally 移除故障并删除模型，运行器回收账单/schema。 */
test("pending billing survives database transaction failure without Redis", async ({ page }) => {
  test.setTimeout(120000);
  await loginAdmin(page);
  const bearer = await sessionBearer(page);
  const headers = { Authorization: "Bearer " + bearer };
  const control = JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8")).control;
  const model = "pending-browser-recovery-" + Date.now();
  let modelID = "";
  let injected = false;
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
          output_cost_per_token: 0.02,
        },
        model_info: { transport: "bypass_openai_chat", pricing_source: "manual", endpoint_types: ["chat"] },
      },
    });
    expect(created.status(), await created.text()).toBe(200);
    modelID = (await created.json()).model_info.id;
    const failed = await page.request.post(control + "/billing-pending/fail", { data: { model } });
    expect(failed.status(), "本场景必须使用 E2E_REDIS=0 的私有数据库夹具: " + (await failed.text())).toBe(200);
    injected = true;
    await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: model, exact: true }).click();
    await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
    await page
      .getByRole("option")
      .filter({ hasText: /(?:^|\s)\/v1\/chat\/completions$/ })
      .click();
    await page
      .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
      .filter({ visible: true })
      .fill("Verify durable billing recovery.");
    const pending = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.endsWith("/chat/completions") && response.request().method() === "POST",
    );
    await page.getByRole("button", { name: t("Send message") }).click();
    const response = await pending;
    expect(response.status(), "账单事务失败不能改写已完成的模型回答").toBe(200);
    await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
    const callID = response.headers()["x-litellm-call-id"];
    expect(callID).toBeTruthy();
    expect((await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).status()).toBe(404);
    const state = await page.request.post(control + "/billing-pending/state", { data: { model } });
    expect(await state.json()).toEqual({ pending: 1 });
    for (let i = 0; i < 2; i++) {
      const recovered = await page.request.post(control + "/billing-pending/recover", { data: { model } });
      expect(recovered.status(), await recovered.text()).toBe(200);
      expect(await recovered.json()).toEqual({ pending: 0 });
    }
    injected = false;
    const billResponse = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
    expect(billResponse.status()).toBe(200);
    const bill = await billResponse.json();
    expect(bill.status).toBe("success");
    expect(Number(bill.spend)).toBeCloseTo(0.12, 8);
    const monitor = await page.request.get(GATEWAY + "/models/monitor?model=" + model, { headers });
    expect(monitor.status()).toBe(200);
    const metrics = await monitor.json();
    expect(metrics.summary.requests).toBe(1);
    expect(metrics.summary.cost).toBeCloseTo(0.12, 8);
    await stableGoto(page, "/logs");
    await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).fill(callID);
    const row = page.getByRole("row").filter({ hasText: callID });
    await expect(row).toHaveCount(1);
    await row.click();
    await expect(page.getByRole("dialog")).toContainText(model);
    await stableGoto(page, "/model-monitor");
    await page.getByLabel(t("monitor.model"), { exact: true }).selectOption(model);
    await expect(page.getByRole("rowheader", { name: model, exact: true })).toHaveCount(1);
    await expect(page.getByRole("region", { name: t("monitor.requests"), exact: true })).toContainText("1");
  } finally {
    if (injected)
      expect((await page.request.post(control + "/billing-pending/recover", { data: { model } })).status()).toBe(200);
    if (modelID)
      expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(
        200,
      );
  }
});
