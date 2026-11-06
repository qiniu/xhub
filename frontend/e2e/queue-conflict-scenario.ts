import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

// 验证冲突账单保留时，浏览器仍可看到后续真实流式请求和已结算费用；测试使用私有数据库与独占 Redis。
export function registerQueueConflictScenario(locale: "en" | "zh-CN") {
  test("queue conflict does not block later logs " + locale, async ({ page }) => {
    test.setTimeout(150_000);
    if (process.env.E2E_REDIS !== "1") throw new Error("E2E_REDIS=1 is required for the isolated queue test");
    const control = JSON.parse(readFileSync(join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8")).control;
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    await page.getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true }).click();
    const tr = (key: string) => translate(locale, key);
    const name = `queue-recovery-${locale}-${Date.now()}`;
    const conflictID = `e2e-queue-conflict-${locale}-${Date.now()}`;
    let modelID = "", keyID = "", started = false;
    try {
      const model = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: name,
        litellm_params: { model: name, custom_llm_provider: "openai", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
      } });
      expect(model.status(), await model.text()).toBe(200);
      modelID = (await model.json()).model_info.id;
      const keyResponse = await page.request.post(GATEWAY + "/key/generate", { headers, data: { key_alias: name } });
      expect(keyResponse.status(), await keyResponse.text()).toBe(200);
      const key = await keyResponse.json(); keyID = key.token_id;
      const setup = await page.request.post(control + "/usage/queue-conflict", { data: { action: "start", id: conflictID } });
      expect(setup.status(), await setup.text()).toBe(200); started = true;
      const reply = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + key.key }, data: {
        model: name, stream: true, messages: [{ role: "user", content: "Verify queue recovery." }],
      } });
      expect(reply.status(), await reply.text()).toBe(200);
      expect(await reply.text()).toContain("e2e-ok");
      const callID = reply.headers()["x-litellm-call-id"];
      await expect.poll(async () => {
        await page.request.post(control + "/usage/queue-conflict", { data: { action: "flush", id: conflictID } });
        return (await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).status();
      }).toBe(200);
      const detail = await (await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).json();
      expect(detail.status).toBe("success");
      expect(detail.spend).toBeGreaterThan(0);
      await stableGoto(page, "/logs?log_id=" + encodeURIComponent(callID));
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByText(name, { exact: true }).first()).toBeVisible();
      await expect(dialog.getByText(tr("Cost"), { exact: true }).first()).toBeVisible();
    } finally {
      if (started) {
        const result = await page.request.post(control + "/usage/queue-conflict", { data: { action: "resolve", id: conflictID } });
        expect(result.status(), await result.text()).toBe(200);
      }
      if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status()).toBe(200);
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(200);
    }
  });
}
