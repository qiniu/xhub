import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

// 双语验证旧 Codex 压缩历史经浏览器到真实网关及严格上游后可续聊，页面可选择同一模型并查看日志。
// 前置隔离数据库/本地上游；覆盖 JSON/SSE、工具结果、未知父响应，finally 删除部署与凭据，运行器清理账单。
for (const locale of ["en", "zh-CN"] as const) test("Old compaction continuation " + locale, async ({ page }) => {
  test.setTimeout(120000);
  await loginAdmin(page);
  const tr = (key: string) => translate(locale, key);
  if (locale === "en") await page.getByRole("button", { name: translate("zh-CN", "language.en"), exact: true }).click();
  const bearer = await sessionBearer(page), headers = { Authorization: "Bearer " + bearer };
  const name = "compaction-" + locale, credential = name + "-connection";
  let id = "";
  const credentials = await page.request.post(GATEWAY + "/credentials", { headers, data: { credential_name: credential, credential_info: { custom_llm_provider: "openai" }, credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" } } });
  expect(credentials.status()).toBe(200);
  try {
    const created = await page.request.post(GATEWAY + "/model/new", { headers, data: { model_name: name, litellm_params: { model: "e2e-agent-native", custom_llm_provider: "openai", litellm_credential_name: credential, input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 }, model_info: { transport: "bypass_openai_responses", endpoint_types: ["responses"], pricing_source: "manual" } } });
    expect(created.status(), await created.text()).toBe(200);
    id = (await created.json()).model_info.id;
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name, exact: true }).click();
    const input = [{ type: "compaction", id: "cmp_old", encrypted_content: "fixture-cipher" }, { type: "function_call", call_id: "call_1", name: "read", arguments: "{}" }, { type: "function_call_output", call_id: "call_1", output: "file-value" }, { role: "user", content: "Continue the existing conversation." }];
    for (const stream of [false, true]) {
      const result = await page.evaluate(async ({ bearer, body }) => {
        const r = await fetch("/gw/v1/responses", { method: "POST", headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json" }, body: JSON.stringify(body) });
        return { status: r.status, raw: await r.text(), id: r.headers.get("x-litellm-call-id") };
      }, { bearer, body: { model: name, input, stream } });
      expect(result.status, result.raw).toBe(200);
      expect(result.raw).toContain("e2e-ok");
      if (stream) expect(result.raw).toContain("response.completed");
      await expect.poll(async () => {
        const log = await page.request.get(GATEWAY + "/spend/logs/ui/" + result.id, { headers });
        return log.ok() ? (await log.json()).status : null;
      }).toBe("success");
    }
    const denied = await page.request.post(GATEWAY + "/v1/responses", { headers, data: { model: name, input, previous_response_id: "unknown-other-response" } });
    expect(denied.status()).toBe(400);
    await stableGoto(page, "/logs");
    await expect(page.getByText(name, { exact: true }).first()).toBeVisible();
  } finally {
    if (id) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
});
