import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";

/** 经浏览器真实同源代理发送 Chat，返回助手消息、HTTP/会话/账单信息；参数为页面、认证、正文与可选会话 ID。解析 JSON/SSE 并保留推理扩展，不拦截网络，数据由隔离运行器清理。 */
async function chatRequest(page: Page, bearer: string, body: Record<string, unknown>, session?: string) {
  return page.evaluate(async ({ bearer, body, session }) => {
    const headers: Record<string, string> = { Authorization: "Bearer " + bearer, "Content-Type": "application/json" };
    if (session) headers["X-Session-Id"] = session;
    const reply = await fetch("/gw/v1/chat/completions", { method: "POST", headers, body: JSON.stringify(body) });
    const raw = await reply.text();
    const message: Record<string, any> = { role: "assistant", content: "", reasoning_content: "", tool_calls: [] };
    if (reply.ok && body.stream) {
      for (const line of raw.split("\n")) {
        if (!line.startsWith("data: {")) continue;
        for (const choice of JSON.parse(line.slice(6)).choices ?? []) {
          const delta = choice.delta;
          message.content += delta.content ?? "";
          message.reasoning_content += delta.reasoning_content ?? "";
          if (delta.reasoning_items) message.reasoning_items = delta.reasoning_items;
          for (const tool of delta.tool_calls ?? []) {
            const call = message.tool_calls[tool.index] ??= { id: "", type: "function", function: { name: "", arguments: "" } };
            call.id = tool.id ?? call.id;
            call.function.name += tool.function?.name ?? "";
            call.function.arguments += tool.function?.arguments ?? "";
          }
        }
      }
    } else if (reply.ok) Object.assign(message, JSON.parse(raw).choices[0].message);
    return { status: reply.status, raw, message, session: reply.headers.get("x-session-id"), callID: reply.headers.get("x-litellm-call-id") };
  }, { bearer, body, session });
}

/** 验证控制台保存/选择 Responses 部署并使用 Chat，同步/流式推理扩展及普通客户端的工具续轮、隔离和记账。前置真实浏览器/后台/本地上游，finally 删除模型凭据，运行器清理 schema，不调用付费服务。 */
test("Chat preserves encrypted Responses reasoning through tool continuation", async ({ page }) => {
  test.setTimeout(120000);
  await loginAdmin(page);
  const bearer = await sessionBearer(page);
  const headers = { Authorization: "Bearer " + bearer };
  const name = "e2e-chat-encrypted-reasoning";
  const credential = "e2e-chat-encrypted-credential";
  let id = "";
  expect((await page.request.post(GATEWAY + "/credentials", { headers, data: { credential_name: credential, credential_info: { custom_llm_provider: "openai" }, credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" } } })).status()).toBe(200);
  try {
    const created = await page.request.post(GATEWAY + "/model/new", { headers, data: { model_name: name, litellm_params: { model: "e2e-agent-chat-reasoning", custom_llm_provider: "openai", litellm_credential_name: credential, input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 }, model_info: { transport: "bypass_openai_responses", endpoint_types: ["chat"], pricing_source: "manual" } } });
    expect(created.status(), await created.text()).toBe(200);
    id = (await created.json()).model_info.id;
    await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name, exact: true }).click();
    await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/chat\/completions$/ }).click();
    await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true }).fill("Reply with a short greeting.");
    const pending = page.waitForResponse(r => /\/chat\/completions$/.test(new URL(r.url()).pathname) && r.request().method() === "POST");
    await page.getByRole("button", { name: t("Send message") }).click();
    expect((await pending).status()).toBe(200);
    await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
    for (const stream of [false, true]) for (const echo of [false, true]) {
      const options = { model: name, stream, tools: [{ type: "function", function: { name: "read", parameters: { type: "object", properties: {} } } }] };
      // 每例使用独立英文提示词，避免重复请求命中结果缓存而跳过上游及付费记账验证。
      const prompt = `Read the file. Stream=${stream}, replay=${echo}.`;
      const first = await chatRequest(page, bearer, { ...options, messages: [{ role: "user", content: prompt }] });
      expect(first.status, first.raw).toBe(200);
      expect(first.message.reasoning_items).toEqual([expect.objectContaining({ type: "reasoning", id: "rs_public", encrypted_content: stream ? "fixture-encrypted-terminal" : "fixture-encrypted" })]);
      expect(first.message.tool_calls).toEqual([expect.objectContaining({ id: "call_1", function: { name: "read", arguments: "{}" } })]);
      const message = { ...first.message };
      if (!echo) delete message.reasoning_items;
      const history = [{ role: "user", content: prompt }, message, { role: "tool", tool_call_id: "call_1", content: "file-value" }];
      const second = await chatRequest(page, bearer, { ...options, messages: history }, first.session!);
      expect(second.status, second.raw).toBe(200);
      expect(second.message.content).toBe("e2e-ok");
      if (stream) expect(second.raw).toContain("data: [DONE]");
      if (echo) expect((await chatRequest(page, bearer, { ...options, messages: history }, "unowned-chat-session")).status).toBe(400);
      for (const result of [first, second]) {
        await expect.poll(async () => {
          const reply = await page.request.get(GATEWAY + "/spend/logs/ui/" + result.callID, { headers });
          if (!reply.ok()) return null;
          const bill = await reply.json();
          return { model: bill.model, status: bill.status, spend: Number(bill.spend) };
        }).toEqual({ model: name, status: "success", spend: 0.000012 });
      }
    }
    await stableGoto(page, "/logs");
    await expect(page.getByText(name, { exact: true }).first()).toBeVisible();
  } finally {
    if (id) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
});
