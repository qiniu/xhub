import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";

/** 验证标准 Responses 的浏览器调用、原生参数/流/工具续轮、连接修改后的历史拒绝、非法请求恢复和账单。前置真实浏览器、Go 后台、隔离数据库及本地协议夹具；参数来自 Playwright fixture，无返回值。finally 删除模型和凭据，运行器回收 schema；不使用外部密钥或修改现有数据。 */
test("native agent protocols preserve reasoning and tool continuation through the console", async ({ page }) => {
  test.setTimeout(120000);
  await loginAdmin(page);
  const bearer = await sessionBearer(page);
  const headers = { Authorization: "Bearer " + bearer };
  const name = "e2e-native-agent-public";
  const credential = "e2e-native-agent-connection";
  let modelID = "";
  expect((await page.request.post(GATEWAY + "/credentials", { headers, data: {
    credential_name: credential, credential_info: { custom_llm_provider: "openai" },
    credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" },
  } })).status()).toBe(200);
  try {
    const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
      model_name: name,
      litellm_params: { model: "e2e-agent-native", custom_llm_provider: "openai", litellm_credential_name: credential,
        input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
      model_info: { transport: "bypass_openai_responses", endpoint_types: ["responses", "chat", "messages"], pricing_source: "manual" },
    } });
    expect(created.status(), await created.text()).toBe(200);
    modelID = (await created.json()).model_info.id;
    await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name, exact: true }).click();
    await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/responses$/ }).click();
    await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true }).fill("Native agent protocol acceptance");
    const pending = page.waitForResponse(r => /\/responses$/.test(new URL(r.url()).pathname) && r.request().method() === "POST");
    await page.getByRole("button", { name: t("Send message") }).click();
    const browserReply = await pending;
    expect(browserReply.status()).toBe(200);
    await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
    const callIDs = [browserReply.headers()["x-litellm-call-id"]];

    // 浏览器 fetch 真实走 /gw，不拦截请求或响应；原生字段通过真实后台到独立上游校验。
    const options = { model: name, reasoning: { effort: "medium", summary: "auto" },
      include: ["reasoning.encrypted_content"], text: { format: { type: "text" } }, store: false };
    for (const stream of [false, true]) {
      const result = await page.evaluate(async ({ bearer, body }) => {
        const response = await fetch("/gw/v1/responses", { method: "POST", headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json" }, body: JSON.stringify(body) });
        return { status: response.status, text: await response.text(), callID: response.headers.get("x-litellm-call-id")!, sessionID: response.headers.get("x-session-id")! };
      }, { bearer, body: { ...options, input: "Read the file", stream, tools: [{ type: "function", name: "read", parameters: { type: "object", properties: {} } }] } });
      expect(result.status, result.text).toBe(200);
      expect(result.text).not.toContain("encrypted_content");
      expect(result.text).toContain("Plan");
      expect(result.text).toContain("function_call");
      callIDs.push(result.callID);
      const completed = stream ? JSON.parse(result.text.split("\n").find(line => line.startsWith("data:") && line.includes('"response.completed"'))!.slice(5)).response : JSON.parse(result.text);
      const continued = await page.evaluate(async ({ bearer, body }) => {
        const response = await fetch("/gw/v1/responses", { method: "POST", headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json" }, body: JSON.stringify(body) });
        return { status: response.status, text: await response.text(), callID: response.headers.get("x-litellm-call-id")! };
      }, { bearer, body: { ...options, previous_response_id: completed.id, input: [{ type: "function_call_output", call_id: "call_1", output: "File contents" }] } });
      expect(continued.status, continued.text).toBe(200);
      expect(continued.text).toContain("e2e-ok");
      callIDs.push(continued.callID);
      const fullHistory = await page.evaluate(async ({ bearer, body, sessionID }) => {
        const response = await fetch("/gw/v1/responses", { method: "POST", headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json", "X-Session-Id": sessionID }, body: JSON.stringify(body) });
        return { status: response.status, text: await response.text(), callID: response.headers.get("x-litellm-call-id")! };
      }, { bearer, sessionID: result.sessionID, body: { ...options, input: [{ role: "user", content: "Read the file" }, ...completed.output, { type: "function_call_output", call_id: "call_1", output: "File contents" }] } });
      expect(fullHistory.status, fullHistory.text).toBe(200);
      callIDs.push(fullHistory.callID);
    }
    const chat = await page.request.post(GATEWAY + "/v1/chat/completions", { headers, data: {
      model: name, reasoning_effort: "medium", response_format: { type: "text" },
      messages: [{ role: "user", content: "Hello" }],
    } });
    expect(chat.status(), await chat.text()).toBe(200);
    expect((await chat.json()).choices[0].message.content).toBe("e2e-ok");
    callIDs.push(chat.headers()["x-litellm-call-id"]);

    // 更新凭据后旧游标仍拒绝；客户端自行提交的公开完整历史允许使用新连接。
    const ownedHeaders = { ...headers, "X-Session-Id": "e2e-connection-snapshot" };
    const snapshot = await page.request.post(GATEWAY + "/v1/responses", { headers: ownedHeaders, data: { ...options, input: "Start an owned conversation." } });
    expect(snapshot.status(), await snapshot.text()).toBe(200);
    const saved = await snapshot.json();
    expect(JSON.stringify(saved.output)).not.toContain("encrypted_content");
    callIDs.push(snapshot.headers()["x-litellm-call-id"]);
    expect((await page.request.patch(GATEWAY + "/credentials/" + credential, { headers, data: { credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake-updated" } } })).status()).toBe(200);
    const history = [{ role: "user", content: "Start an owned conversation." }, ...saved.output, { role: "user", content: "Continue." }];
    for (const body of [{ ...options, previous_response_id: saved.id, input: "Continue." }]) {
      const blocked = await page.request.post(GATEWAY + "/v1/responses", { headers: ownedHeaders, data: body });
      expect(blocked.status(), await blocked.text()).toBe(409);
    }
    for (let index = 0; index < 3; index++) {
      const invalid = await page.request.post(GATEWAY + "/v1/responses", { headers, data: { model: name, input: "Hello", session_id: " " } });
      expect(invalid.status()).toBe(400);
    }
    const fresh = await page.request.post(GATEWAY + "/v1/responses", { headers: ownedHeaders, data: { model: name, input: "A fresh plain request." } });
    expect(fresh.status(), await fresh.text()).toBe(200);
    callIDs.push(fresh.headers()["x-litellm-call-id"]);
    const stillBlocked = await page.request.post(GATEWAY + "/v1/responses", { headers: ownedHeaders, data: { ...options, input: history } });
    expect(stillBlocked.status(), await stillBlocked.text()).toBe(200);
    callIDs.push(stillBlocked.headers()["x-litellm-call-id"]);

    for (const callID of callIDs) {
      expect(callID).toBeTruthy();
      await expect.poll(async () => {
        const response = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
        if (!response.ok()) return null;
        const bill = await response.json();
        return { model: bill.model, status: bill.status, prompt: bill.prompt_tokens, completion: bill.completion_tokens, spend: Number(bill.spend) };
      }).toEqual({ model: name, status: "success", prompt: 8, completion: 2, spend: 0.000012 });
    }
    const rejected = await page.request.post(GATEWAY + "/v1/chat/completions", { headers, data: {
      model: name, messages: [{ role: "user", content: "Hello" }], unsupported_bridge_option: true,
    } });
    expect(rejected.status()).toBe(400);
    const unauthorized = await page.request.post(GATEWAY + "/v1/responses", { headers: { Authorization: "Bearer sk-invalid" }, data: { model: name, input: "Hello" } });
    expect(unauthorized.status()).toBe(401);
    await stableGoto(page, "/logs");
    await expect(page.getByText(name, { exact: true }).first()).toBeVisible();
  } finally {
    if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
});
