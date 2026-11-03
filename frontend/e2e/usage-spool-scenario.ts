import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册中英文磁盘账单恢复闭环；前置无 Redis 的真实网关、私有数据库和本地供应商，从 Playground 发起唯一推理后关闭整个IAM连接池。
 * 验证错误日志唯一且不阻止后续对话、页面成功、本机快照、新连接及新队列对象重放、日志和模型监控唯一入账；finally 移除故障并删除模型，运行器回收账单/schema。 */
export function registerUsageSpoolScenario(locale: "en" | "zh-CN") {
  test("durable disk billing recovers after database disconnect " + locale, async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    const t = (key: string) => translate(locale, key);
    const bearer = await sessionBearer(page);
    const headers = { Authorization: "Bearer " + bearer };
    const control = JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8")).control;
    const model = "spool-browser-recovery-" + Date.now();
    let modelID = "";
    let guardID = "";
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
      const failed = await page.request.post(control + "/usage-spool/fail", { data: { model } });
      expect(failed.status(), "本场景必须使用 E2E_REDIS=0 的私有数据库夹具: " + (await failed.text())).toBe(200);
      injected = true;
      // 先触发执行链错误，再验证后续真实对话不被重复账单污染；护栏先于上游，故此时PG仍可用。
      const guard = await page.request.post(GATEWAY + "/guardrails", {
        headers,
        data: {
          guardrail: {
            guardrail_name: model + "-block",
            litellm_params: {
              guardrail: "blocked_words",
              blocked_words: ["spool-block-marker"],
              mode: "pre_call",
              default_on: false,
            },
          },
        },
      });
      expect(guard.status(), await guard.text()).toBe(200);
      guardID = (await guard.json()).guardrail_id;
      const blocked = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers,
        data: { model, guardrails: [model + "-block"], messages: [{ role: "user", content: "spool-block-marker" }] },
      });
      expect(blocked.status(), await blocked.text()).toBe(400);
      const failedID = blocked.headers()["x-litellm-call-id"];
      const failedLog = await page.request.get(GATEWAY + "/spend/logs/ui/" + failedID, { headers });
      expect(failedLog.status()).toBe(200);
      expect((await failedLog.json()).status).toBe("error");
      const empty = await page.request.post(control + "/usage-spool/state", { data: { model } });
      expect(await empty.json()).toEqual({ pending: 0, quarantined: 0 });
      expect((await page.request.delete(GATEWAY + "/guardrails/" + guardID, { headers })).status()).toBe(200);
      guardID = "";
      await stableGoto(page, "/logs");
      await page.getByRole("tab", { name: t("Error Logs"), exact: true }).click();
      await page
        .getByPlaceholder(/按 ID 搜索日志|Search logs by ID/)
        .filter({ visible: true })
        .fill(failedID);
      await expect(page.getByRole("row").filter({ hasText: failedID })).toHaveCount(1);
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
      expect(response.status(), "账单数据库断连不能改写已完成的模型回答").toBe(200);
      await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
      const callID = response.headers()["x-litellm-call-id"];
      expect(callID).toBeTruthy();
      const state = await page.request.post(control + "/usage-spool/state", { data: { model } });
      expect(await state.json()).toEqual({ pending: 1, quarantined: 0 });
      for (let i = 0; i < 2; i++) {
        const recovered = await page.request.post(control + "/usage-spool/recover", { data: { model } });
        expect(recovered.status(), await recovered.text()).toBe(200);
        expect(await recovered.json()).toEqual({ pending: 0, quarantined: 0 });
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
      // 入站护栏尚未选择供应商，不增加模型供应商监控请求；成功推理仍唯一。
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
        expect((await page.request.post(control + "/usage-spool/recover", { data: { model } })).status()).toBe(200);
      if (guardID)
        expect((await page.request.delete(GATEWAY + "/guardrails/" + guardID, { headers })).status()).toBe(200);
      if (modelID)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(
          200,
        );
    }
  });
}
