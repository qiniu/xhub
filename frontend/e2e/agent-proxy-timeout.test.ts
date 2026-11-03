import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";

/** 验证用户在调试台等待超过60秒的非流式推理仍能收到答案和账单。前置真实浏览器、Next生产服务、Go网关、隔离库和实际等待65秒的本地上游；finally删除部署与凭据，运行器回收schema，不依赖外部服务。 */
test("slow agent response survives the console gateway proxy", async ({ page }) => {
  test.setTimeout(120000);
  await loginAdmin(page);
  const bearer = await sessionBearer(page);
  const headers = { Authorization: "Bearer " + bearer };
  const credential = "e2e-agent-slow-credential";
  const name = "e2e-agent-slow-response";
  let modelID = "";
  expect((await page.request.post(GATEWAY + "/credentials", { headers, data: {
    credential_name: credential, credential_info: { custom_llm_provider: "openai" }, credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" },
  } })).status()).toBe(200);
  try {
    const model = await page.request.post(GATEWAY + "/model/new", { headers, data: {
      model_name: name, litellm_params: { model: "e2e-agent-proxy-slow", custom_llm_provider: "openai", litellm_credential_name: credential, input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
      model_info: { transport: "bypass_openai_chat", endpoint_types: ["responses"], pricing_source: "manual" },
    } });
    expect(model.status(), await model.text()).toBe(200);
    modelID = (await model.json()).model_info.id;
    await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name, exact: true }).click();
    await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/responses$/ }).click();
    await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true }).fill("Reply with a short greeting.");
    const start = Date.now();
    const pending = page.waitForResponse(r => /\/responses$/.test(new URL(r.url()).pathname) && r.request().method() === "POST", { timeout: 90000 });
    await page.getByRole("button", { name: t("Send message") }).click();
    const response = await pending;
    expect(response.status()).toBe(200);
    expect(Date.now() - start).toBeGreaterThanOrEqual(60000);
    await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
    const callID = response.headers()["x-litellm-call-id"];
    expect(callID).toBeTruthy();
    await expect.poll(async () => {
      const response = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
      return response.ok() ? (await response.json()).status : null;
    }).toBe("success");
  } finally {
    if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
});
