import { expect, test } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t, watchGateway } from "./helpers";

const { verifyModelDeployment } = require("../../e2e/model_deployment_browser.cjs");
/** 用途：注册独立场景「真实验收按部署行核对单入口和 chat · responses 并完成浏览器调用计费」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC064() {


test("真实验收按部署行核对单入口和 chat · responses 并完成浏览器调用计费", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
  const headers = { Authorization: "Bearer " + await sessionBearer(page) };
  const name = "e2e-grouped-endpoint-acceptance";
  const credential = name + "-connection";
  const deployments: { id: string; public_name: string; provider: string; model: string; transport: string; endpoint_types: string[] }[] = [];
  const savedCredential = await page.request.post(GATEWAY + "/credentials", { headers, data: {
    credential_name: credential, credential_info: { custom_llm_provider: "custom" },
    credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
  } });
  expect(savedCredential.status(), await savedCredential.text()).toBe(200);
  try {
    for (const endpoint_types of [["chat", "responses"], ["chat"]]) {
      const model = "e2e-upstream-" + endpoint_types.length;
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: name,
        litellm_params: { model, custom_llm_provider: "custom", litellm_credential_name: credential,
          input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_openai_chat", endpoint_types, pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200);
      const id = (await created.json()).model_info.id;
      deployments.push({ id, public_name: name, provider: credential, model, transport: "bypass_openai_chat", endpoint_types });
      const stored = await page.request.get(GATEWAY + "/v2/model/info?modelId=" + encodeURIComponent(id), { headers });
      expect(stored.status(), "部署详情应可回读").toBe(200);
      expect((await stored.json()).data[0].model_info.endpoint_types, "入口声明应完整持久化").toEqual(endpoint_types);
    }
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: t("pages.models.all") }).click();
    await page.getByLabel("搜索模型").fill(name);
    const region = page.getByRole("region", { name: "公开模型 " + name, exact: true });
    await expect(region.getByRole("row")).toHaveCount(3);
    for (const deployment of deployments) await verifyModelDeployment(page, deployment);

    await stableGoto(page, "/playground");
    await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name, exact: true }).click();
    for (const endpoint of ["chat/completions", "responses"]) {
      await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
      await page.getByRole("option").filter({ hasText: new RegExp("(?:^|\\s)/v1/" + endpoint + "$") }).click();
      await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true }).fill("grouped endpoint acceptance");
      const pending = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/" + endpoint) && response.request().method() === "POST");
      await page.getByRole("button", { name: t("Send message") }).click();
      const response = await pending;
      expect(response.status(), "浏览器 " + endpoint + " 请求应成功").toBe(200);
      await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId, "浏览器请求必须关联账单").toBeTruthy();
      await expect.poll(async () => {
        const bill = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
        if (!bill.ok()) return null;
        const body = await bill.json();
        return { model: body.model, prompt_tokens: body.prompt_tokens, completion_tokens: body.completion_tokens, spend: Number(body.spend) };
      }).toEqual({ model: name, prompt_tokens: 8, completion_tokens: 2, spend: 0.000012 });
    }
    await guard.assertOk();
  } finally {
    for (const deployment of deployments) {
      expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: deployment.id } })).status(), "清理测试部署").toBe(200);
    }
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status(), "清理测试连接").toBe(200);
  }
});
}
