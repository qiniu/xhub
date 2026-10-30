import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";

/** 将真实 Messages JSON/SSE 回复还原为客户端会回传的内容块；参数为正文和流开关，返回 thinking/text/tool_use 数组。仅用于下述 E2E 工具续轮，损坏 JSON 直接失败，不写存储或伪造响应。 */
function messagesContent(raw: string, stream: boolean): Record<string, unknown>[] {
  if (!stream) return JSON.parse(raw).content;
  const blocks: Record<string, unknown>[] = [];
  for (const line of raw.split("\n")) {
    if (!line.startsWith("data: ")) continue;
    const event = JSON.parse(line.slice(6));
    if (event.type === "content_block_start") blocks[event.index] = { ...event.content_block };
    if (event.type === "content_block_delta") {
      const block = blocks[event.index];
      const delta = event.delta;
      if (delta.type === "thinking_delta") block.thinking = String(block.thinking || "") + delta.thinking;
      if (delta.type === "signature_delta") block.signature = String(block.signature || "") + delta.signature;
      if (delta.type === "text_delta") block.text = String(block.text || "") + delta.text;
      if (delta.type === "input_json_delta") block.input = JSON.parse(delta.partial_json);
    }
  }
  return blocks;
}

/** 通过真实浏览器同源 /gw 发送 Messages；参数为页面、认证、正文及可选 Claude 原生会话 ID，返回状态、正文、调用和会话 ID。沿用真实网络，不注入网关 X-Session-Id；仅产生本例隔离日志。 */
async function requestMessages(page: Page, bearer: string, body: Record<string, unknown>, claudeSession?: string) {
  return page.evaluate(async ({ bearer, body, claudeSession }) => {
    const headers: Record<string, string> = { Authorization: "Bearer " + bearer, "Content-Type": "application/json" };
    if (claudeSession) headers["X-Claude-Code-Session-Id"] = claudeSession;
    const response = await fetch("/gw/v1/messages", { method: "POST", headers, body: JSON.stringify(body) });
    return { status: response.status, text: await response.text(), callID: response.headers.get("x-litellm-call-id"), sessionID: response.headers.get("x-session-id") };
  }, { bearer, body, claudeSession });
}

/** 验证控制台选用 Messages、Chat/Responses 公开及密文推理的 JSON/SSE 工具续轮和日志计费。参数来自真实 Playwright fixture，返回无；本地上游与专属部署，finally 删除模型/凭据，运行器清理数据库，不使用真实供应商 Key。 */
test("messages bridge preserves public thinking and file tool continuation", async ({ page }) => {
  test.setTimeout(180000);
  await loginAdmin(page);
  const bearer = await sessionBearer(page);
  const headers = { Authorization: "Bearer " + bearer };
  const credential = "e2e-public-thinking-connection";
  const ids: string[] = [];
  expect((await page.request.post(GATEWAY + "/credentials", { headers, data: {
    credential_name: credential, credential_info: { custom_llm_provider: "openai" },
    credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" },
  } })).status()).toBe(200);
  try {
    for (const protocol of ["responses", "responses-encrypted", "responses-redacted", "chat"]) {
      const name = "e2e-public-thinking-" + protocol;
      const encrypted = protocol === "responses-encrypted" || protocol === "responses-redacted";
      const reasoning = protocol === "responses-redacted"
        ? { type: "redacted_thinking", data: "xhub_responses_reasoning:fixture-encrypted" }
        : { type: "thinking", thinking: "Plan", signature: encrypted ? "xhub_responses_reasoning:fixture-encrypted" : "" };
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: name,
        litellm_params: { model: encrypted ? "e2e-agent-messages-" + (protocol === "responses-redacted" ? "redacted" : "encrypted") : "e2e-agent-messages", custom_llm_provider: "openai", litellm_credential_name: credential,
          input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_openai_" + (protocol.startsWith("responses") ? "responses" : protocol), endpoint_types: ["messages"], pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200);
      ids.push((await created.json()).model_info.id);
      await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
      await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/messages$/ }).click();
      await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true }).fill("Reply with a short greeting.");
      const pending = page.waitForResponse(r => /\/messages$/.test(new URL(r.url()).pathname) && r.request().method() === "POST");
      await page.getByRole("button", { name: t("Send message") }).click();
      expect((await pending).status()).toBe(200);
      await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
      for (const stream of [false, true]) {
        const sessionID = `claude-e2e-${protocol}-${stream}`;
        const nativeHeader = protocol === "responses-encrypted" ? sessionID : undefined;
        const metadata = { user_id: JSON.stringify({ device_id: "device", session_id: nativeHeader ? "lower-priority-" + sessionID : sessionID }) };
        const first = await requestMessages(page, bearer, {
          model: name, stream, max_tokens: 256, metadata, messages: [{ role: "user", content: "Read the file." }],
          tools: [{ name: "read", input_schema: { type: "object", properties: {} } }],
        }, nativeHeader);
        expect(first.status, first.text).toBe(200);
        expect(first.sessionID).toBe(sessionID);
        const content = messagesContent(first.text, stream);
        expect(content[0]).toMatchObject(reasoning);
        expect(content[1]).toMatchObject({ type: "tool_use", id: "call_1", name: "read", input: {} });
        const second = await requestMessages(page, bearer, {
          model: name, stream, max_tokens: 256, metadata, messages: [{ role: "user", content: [{ type: "text", text: "Read the file.", cache_control: { type: "ephemeral" } }] },
            { role: "assistant", content }, { role: "user", content: [{ type: "tool_result", tool_use_id: "call_1", content: "file-value" }] }],
        }, nativeHeader);
        expect(second.status, second.text).toBe(200);
        expect(second.sessionID).toBe(first.sessionID);
        expect(messagesContent(second.text, stream)).toEqual([
          reasoning, { type: "text", text: "e2e-ok" },
        ]);
        if (encrypted) {
          const rejected = await requestMessages(page, bearer, {
            model: name, stream, max_tokens: 256, metadata,
            messages: [{ role: "user", content: "Read the file." }, { role: "assistant", content }],
          }, "unowned-" + sessionID);
          expect(rejected.status, rejected.text).toBe(400);
          expect(rejected.text).toContain("owned session");
        }
        for (const response of [first, second]) {
          await expect.poll(async () => {
            const bill = await page.request.get(GATEWAY + "/spend/logs/ui/" + response.callID, { headers });
            if (!bill.ok()) return null;
            const row = await bill.json();
            return { model: row.model, status: row.status, spend: Number(row.spend) };
          }).toEqual({ model: name, status: "success", spend: 0.000012 });
        }
      }
      await stableGoto(page, "/logs");
      await expect(page.getByText(name, { exact: true }).first()).toBeVisible();
    }
  } finally {
    for (const id of ids) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
});
