import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

// 双语真实浏览器验证默认关闭、账号灰度、显式全量、开启后的调试台SSE、未知扩展、账单和关闭后旧会话续接。
// 隔离数据库及严格本地上游；请求仅补合成扩展而不mock响应，finally恢复配置并删除部署/凭据。
for (const locale of ["en", "zh-CN"] as const) test("native Responses rollout " + locale, async ({ page }) => {
  test.setTimeout(120000);
  await loginAdmin(page);
  const tr = (key: string) => translate(locale, key);
  if (locale === "en") await page.getByRole("button", { name: translate("zh-CN", "language.en"), exact: true }).click();
  const bearer = await sessionBearer(page), headers = { Authorization: "Bearer " + bearer };
  const me = await page.request.get(GATEWAY + "/auth/me", { headers });
  const userID = (await me.json()).user_id;
  expect(userID).toBeTruthy();
  const name = "native-rollout-" + locale, credential = name + "-connection";
  let id = "";
  // globalSwitch 使用管理接口控制测试账号灰度；仅修改隔离环境，断言持久化成功。
  const globalSwitch = async (enabled: boolean) => {
    const r = await page.request.post(GATEWAY + "/config/update", { headers, data: { general_settings: { native_responses: { enabled, user_ids: [userID] } } } });
    expect(r.status(), await r.text()).toBe(200);
  };
  expect((await page.request.post(GATEWAY + "/credentials", { headers, data: { credential_name: credential, credential_info: { custom_llm_provider: "openai" }, credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" } } })).status()).toBe(200);
  try {
    const created = await page.request.post(GATEWAY + "/model/new", { headers, data: { model_name: name, litellm_params: { model: "e2e-native-decoupled", custom_llm_provider: "openai", litellm_credential_name: credential, input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 }, model_info: { transport: "bypass_openai_responses", endpoint_types: ["responses"], pricing_source: "manual" } } });
    expect(created.status(), await created.text()).toBe(200);
    id = (await created.json()).model_info.id;
    const extension = { type: "future_item", payload: { value: "keep" } };
    const body = { model: name, input: [extension, { role: "user", content: "Hello" }] };
    await globalSwitch(false);
    expect((await page.request.post(GATEWAY + "/v1/responses", { headers, data: body })).status()).toBe(400);
    // 非名单身份不启用；显式全量不依赖名单，随后恢复当前账号灰度验证浏览器路径。
    expect((await page.request.post(GATEWAY + "/config/update", { headers, data: { general_settings: { native_responses: { enabled: true, user_ids: ["not-current-user"] } } } })).status()).toBe(200);
    expect((await page.request.post(GATEWAY + "/v1/responses", { headers, data: body })).status()).toBe(400);
    expect((await page.request.post(GATEWAY + "/config/update", { headers, data: { general_settings: { native_responses: { enabled: true, all_users: true } } } })).status()).toBe(200);
    const allUsers = await page.request.post(GATEWAY + "/v1/responses", { headers, data: body });
    expect(allUsers.status(), await allUsers.text()).toBe(200);
    expect(allUsers.headers()["x-xhub-responses-path"]).toBe("native");
    await globalSwitch(true);
    const json = await page.request.post(GATEWAY + "/v1/responses", { headers, data: body });
    expect(json.status(), await json.text()).toBe(200);
    expect(await json.text()).toContain("future_output");
    expect(json.headers()["x-xhub-responses-path"]).toBe("native");
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name, exact: true }).click();
    await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option", { name: "OpenAI Responses · /v1/responses", exact: true }).click();
    await page.route("**/responses", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      const request = route.request().postDataJSON();
      const input = typeof request.input === "string" ? [{ role: "user", content: request.input }] : request.input;
      await route.continue({ postData: JSON.stringify({ ...request, input: [extension, ...input] }) });
    });
    const prompt = "Verify native Responses compatibility.";
    const completed = page.waitForResponse(r => r.url().endsWith("/responses") && r.request().method() === "POST");
    await page.getByTestId("chat-composer-input").fill(prompt);
    await page.getByTestId("chat-send-button").click();
    const response = await completed;
    expect(response.status()).toBe(200);
    expect(response.headers()["x-xhub-responses-path"]).toBe("native");
    expect(await response.text()).toContain("future_output");
    await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
    await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
    await page.unroute("**/responses");
    const first = await page.request.post(GATEWAY + "/v1/responses", { headers, data: { model: name, input: "Hello" } });
    expect(first.status()).toBe(200);
    const parent = (await first.json()).id;
    await globalSwitch(false);
    const next = await page.request.post(GATEWAY + "/v1/responses", { headers, data: { model: name, input: "Continue.", previous_response_id: parent } });
    expect(next.status(), await next.text()).toBe(200);
    expect(next.headers()["x-xhub-responses-path"]).toBe("legacy");
    await expect.poll(async () => {
      const log = await page.request.get(GATEWAY + "/spend/logs/ui/" + response.headers()["x-litellm-call-id"], { headers });
      return log.ok() ? (await log.json()).status : null;
    }).toBe("success");
    await stableGoto(page, "/logs");
    await expect(page.getByText(name, { exact: true }).first()).toBeVisible();
  } finally {
    await page.unroute("**/responses");
    expect((await page.request.post(GATEWAY + "/config/field/delete", {headers, data: {config_type: "general_settings", field_name: "native_responses"}})).status()).toBe(200);
    if (id) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
});
