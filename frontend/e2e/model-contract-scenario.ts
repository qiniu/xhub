import { randomUUID } from "node:crypto";
import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 为指定语言注册模型契约完整流程；前置隔离PG和受控上游，真实页面回读/探测/启停/推理/用量，finally删除部署与凭据。 */
export function registerModelContract(locale: "en" | "zh-CN") {
  test("model contract lifecycle " + locale, async ({ page }) => {
    test.setTimeout(120000);
    const tr = (key: string, params?: Record<string, string>) => translate(locale, key, params);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const name = "契约-model-" + randomUUID().slice(0, 8);
    const credential = "contract-" + randomUUID();
    let id = "";
    /** 创建隔离夹具并检查真实回复；失败不继续业务断言，记录的ID由finally清理。 */
    const create = async (path: string, data: unknown) => {
      const response = await page.request.post(GATEWAY + path, { headers, data });
      expect(response.status(), await response.text()).toBe(200);
      return response.json();
    };
    await create("/credentials", { credential_name: credential, credential_info: { custom_llm_provider: "openai" }, credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" } });
    try {
      const created = await create("/model/new", {
        model_name: name,
        litellm_params: { model: "gpt-4o-mini", custom_llm_provider: "openai", litellm_credential_name: credential, input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat", "responses"], pricing_source: "manual" },
      });
      id = created.model_info.id;
      await stableGoto(page, "/models-and-endpoints?model=" + id);
      await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
      let pending = page.waitForResponse(r => new URL(r.url()).pathname === "/health/test_connection");
      await page.getByRole("button", { name: tr("Test Connection"), exact: true }).click();
      let response = await pending;
      expect(response.status()).toBe(200);
      expect(await response.json()).toMatchObject({ status: "success", healthy: true });
      await expect(page.getByRole("status").filter({ hasText: tr("Connection is healthy and the model responded.") })).toBeVisible();
      const toggle = page.getByRole("switch", { name: tr("providerGuideUI.toggleLabel", { name }) });
      pending = page.waitForResponse(r => new URL(r.url()).pathname === "/model/" + id + "/update" && r.request().method() === "PATCH");
      await toggle.click();
      response = await pending;
      expect(response.status()).toBe(200);
      expect((await response.json()).model_info.disabled).toBe(true);
      await page.reload();
      await expect(toggle).not.toBeChecked();
      await expect(page.getByRole("button", { name: tr("Test Connection"), exact: true })).toBeDisabled();
      pending = page.waitForResponse(r => new URL(r.url()).pathname === "/model/" + id + "/update" && r.request().method() === "PATCH");
      await toggle.click();
      expect((await pending).status()).toBe(200);
      await expect(toggle).toBeChecked();
      const read = await page.request.get(GATEWAY + "/v2/model/info?modelId=" + id, { headers });
      expect((await read.json()).data[0]).toMatchObject({ model_name: name, litellm_params: { litellm_credential_name: credential }, model_info: { endpoint_types: ["chat", "responses"], disabled: false } });
      await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      await page.getByTestId("chat-composer-input").fill("Reply with a short greeting.");
      pending = page.waitForResponse(r => /\/(chat\/completions|responses)$/.test(new URL(r.url()).pathname) && r.request().method() === "POST");
      await page.getByTestId("chat-send-button").click();
      response = await pending;
      expect(response.status()).toBe(200);
      await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId).toBeTruthy();
      await expect.poll(async () => {
        const bill = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
        if (!bill.ok()) return null;
        const body = await bill.json();
        return { model: body.model, spend: Number(body.spend), input: body.prompt_tokens, output: body.completion_tokens };
      }).toEqual({ model: name, spend: 0.000012, input: 8, output: 2 });
      // 本地上游按此模型名前缀返回真实500，页面须呈现业务失败而非200假成功。
      const changed = await page.request.patch(GATEWAY + "/model/" + id + "/update", { headers, data: { litellm_params: { model: "e2e-fallback-500-contract" } } });
      expect(changed.status()).toBe(200);
      await stableGoto(page, "/models-and-endpoints?model=" + id);
      pending = page.waitForResponse(r => new URL(r.url()).pathname === "/health/test_connection");
      await page.getByRole("button", { name: tr("Test Connection"), exact: true }).click();
      response = await pending;
      expect(response.status()).toBe(200);
      expect(await response.json()).toMatchObject({ status: "error", healthy: false, error: "upstream_http_error" });
      await expect(page.getByRole("status").filter({ hasText: tr("Connection test failed: {value0}", { value0: "The upstream service returned HTTP 500." }) })).toBeVisible();
    } finally {
      if (id) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status(), "清理部署").toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status(), "清理供应商").toBe(200);
    }
  });
}
