import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 从真实 JSON/SSE 还原完整助手历史；参数为正文、端点及流标记，返回可原样提交的内容，错误 JSON 令测试失败，不改服务状态。 */
function historyOutput(raw: string, endpoint: string, stream: boolean): any {
  if (!stream) {
    const value = JSON.parse(raw);
    if (endpoint === "messages") return value.content;
    if (endpoint === "responses") return value;
    return value.choices[0].message;
  }
  const events = raw
    .split("\n")
    .filter((line) => line.startsWith("data: ") && line !== "data: [DONE]")
    .map((line) => JSON.parse(line.slice(6)));
  if (endpoint === "responses") return events.find((event) => event.type === "response.completed").response;
  if (endpoint === "chat/completions")
    return events.find((event) => event.choices?.[0]?.delta?.tool_calls).choices[0].delta;
  const blocks: any[] = [],
    args: Record<number, string> = {};
  for (const event of events) {
    if (event.type === "content_block_start") blocks[event.index] = { ...event.content_block };
    if (event.type === "content_block_delta") {
      const block = blocks[event.index],
        delta = event.delta;
      if (delta.type === "input_json_delta") args[event.index] = (args[event.index] || "") + delta.partial_json;
      else {
        const key = delta.type.replace("_delta", "");
        block[key] = (block[key] || "") + delta[key];
      }
    }
    if (event.type === "content_block_stop" && args[event.index])
      blocks[event.index].input = JSON.parse(args[event.index]);
  }
  return blocks;
}

/** 通过浏览器实际 Key 发起数据面请求；参数为页面、Key、端点、会话和正文，返回状态/正文/日志ID；无路由 mock，仅写本例账单。 */
async function sendHistory(
  page: Page,
  key: string,
  context: { endpoint: string; session: string },
  body: Record<string, unknown>,
) {
  const { endpoint, session } = context;
  return page.evaluate(
    async ({ key, endpoint, session, body }) => {
      // 此处必须从浏览器读取原始协议响应，防止产品客户端封装掩盖转换错误。
      // eslint-disable-next-line no-restricted-syntax
      const response = await fetch("/gw/v1/" + endpoint, {
        method: "POST",
        headers: {
          Authorization: "Bearer " + key,
          "Content-Type": "application/json",
          "X-Claude-Code-Session-Id": session,
          "X-Session-Id": session,
        },
        body: JSON.stringify(body),
      });
      return {
        status: response.status,
        text: await response.text(),
        callID: response.headers.get("x-litellm-call-id"),
      };
    },
    { key, endpoint, session, body },
  );
}

/** 注册中英文真实控制台到数据面的原生工具历史验收；参数为语言，无返回值；finally删除专用Key/模型/凭据，运行器回收隔离数据库。 */
export function registerAgentHistoryScenario(locale: "en" | "zh-CN") {
  test(`native agent history and parallel tools ${locale}`, async ({ page }) => {
    test.setTimeout(180000);
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const tr = (key: string) => translate(locale, key);
    const bearer = await sessionBearer(page),
      headers = { Authorization: "Bearer " + bearer };
    const credential = "e2e-history-" + locale,
      ids: string[] = [];
    let token = "",
      key = "",
      lastCall = "";
    expect(
      (
        await page.request.post(GATEWAY + "/credentials", {
          headers,
          data: {
            credential_name: credential,
            credential_info: { custom_llm_provider: "openai" },
            credential_values: { api_base: UPSTREAM + "/v1", api_key: "local-history-key" },
          },
        })
      ).status(),
    ).toBe(200);
    try {
      for (const protocol of ["chat", "messages"]) {
        const created = await page.request.post(GATEWAY + "/model/new", {
          headers,
          data: {
            model_name: `history-${protocol}-${locale}`,
            litellm_params: {
              model: "e2e-history-" + protocol,
              custom_llm_provider: "openai",
              litellm_credential_name: credential,
              input_cost_per_token: 0.000001,
              output_cost_per_token: 0.000002,
            },
            model_info: {
              transport: protocol === "chat" ? "bypass_openai_chat" : "bypass_anthropic_messages",
              endpoint_types: ["chat", "responses", "messages"],
              pricing_source: "manual",
            },
          },
        });
        expect(created.status(), await created.text()).toBe(200);
        ids.push((await created.json()).model_info.id);
      }
      await stableGoto(page, "/api-keys");
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name"), { exact: true }).fill("agent-history-" + locale);
      const generated = page.waitForResponse(
        (r) => r.url().endsWith("/key/generate") && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
      const response = await generated;
      expect(response.status()).toBe(200);
      const data = await response.json();
      token = data.token_id;
      key = data.key;
      expect(token).toBeTruthy();
      expect(key).toBeTruthy();
      await expect(page.getByText(tr("pages.apiKeys.saveKey"))).toBeVisible();
      await page.keyboard.press("Escape");
      await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: `history-messages-${locale}`, exact: true }).click();
      await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
      await page
        .getByRole("option")
        .filter({ hasText: /\/v1\/messages$/ })
        .click();
      await page
        .getByPlaceholder(tr("Type your message... (Shift+Enter for new line)"))
        .filter({ visible: true })
        .fill("Reply with a short greeting.");
      const played = page.waitForResponse(
        (r) => /\/messages$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: tr("Send message") }).click();
      expect((await played).status()).toBe(200);
      await expect(page.getByText("history-ok", { exact: true }).last()).toBeVisible();
      for (const stream of [false, true]) {
        const session = `native-${locale}-${stream}`;
        const body = {
          model: `history-messages-${locale}`,
          max_tokens: 128,
          stream,
          messages: [{ role: "user", content: "Read the fixture." }],
          // Claude Code 会显式携带非延迟标记；真实数据面必须接受并完成工具续轮。
          tools: [{ name: "read", defer_loading: false, input_schema: { type: "object", properties: { path: { type: "string" } } } }],
        };
        const first = await sendHistory(page, key, { endpoint: "messages", session }, body);
        expect(first.status, first.text).toBe(200);
        const content = historyOutput(first.text, "messages", stream);
        expect(content).toEqual([
          { type: "thinking", thinking: "Inspect file", signature: "native-signature" },
          { type: "redacted_thinking", data: "native-cipher" },
          { type: "tool_use", id: "call_read", name: "read", input: { path: "fixture" }, caller: { type: "direct" } },
        ]);
        const continuation = {
          ...body,
          messages: [
            ...body.messages,
            { role: "assistant", content },
            { role: "user", content: [{ type: "tool_result", tool_use_id: "call_read", content: "file-value" }] },
          ],
        };
        const second = await sendHistory(page, key, { endpoint: "messages", session }, continuation);
        expect(second.status, second.text).toBe(200);
        expect(historyOutput(second.text, "messages", stream)).toEqual([{ type: "text", text: "history-ok" }]);
        const resumed = await sendHistory(
          page,
          key,
          { endpoint: "messages", session: session + "-unowned" },
          continuation,
        );
        expect(resumed.status, resumed.text).toBe(200);
        // 原生完整历史无需网关归属；改名后仍用同一Key和会话续轮，并核对模型目录同步显示。
        const renamed = body.model + "-renamed";
        expect((await page.request.post(GATEWAY + "/model/update", {headers, data:{id:ids[1],model_name:renamed}})).status()).toBe(200);
        const afterRename = await sendHistory(page,key,{endpoint:"messages",session},{...continuation,model:renamed,max_tokens:129});
        expect(afterRename.status,afterRename.text).toBe(200);
        expect(historyOutput(afterRename.text,"messages",stream)).toEqual([{type:"text",text:"history-ok"}]);
        await stableGoto(page,"/mine-models");
        await page.getByRole("textbox",{name:tr("Search"),exact:true}).fill(renamed);
        await expect(page.getByRole("heading",{name:renamed,exact:true})).toBeVisible();
        expect((await page.request.post(GATEWAY + "/model/update", {headers,data:{id:ids[1],model_name:body.model}})).status()).toBe(200);
        const invalid = await sendHistory(
          page,
          key,
          { endpoint: "messages", session },
          {
            ...body,
            messages: [
              {
                role: "assistant",
                content: [{ ...content[2], caller: { type: "code_execution_20250825", tool_id: "code" } }],
              },
            ],
          },
        );
        expect(invalid.status, invalid.text).toBe(400);
        lastCall = second.callID!;
      }
      for (const endpoint of ["chat/completions", "responses"])
        for (const stream of [false, true]) {
          const session = `parallel-${locale}-${endpoint.replace("/", "-")}-${stream}`,
            model = `history-chat-${locale}`;
          const tool = {
            type: "function",
            name: "lookup",
            parameters: { type: "object", properties: { key: { type: "string" } } },
          };
          const user = { role: "user", content: "Look up alpha and beta in parallel." };
          const body =
            endpoint === "responses"
              ? { model, stream, input: [user], tools: [tool] }
              : {
                  model,
                  stream,
                  messages: [user],
                  tools: [{ type: "function", function: { name: tool.name, parameters: tool.parameters } }],
                };
          const first = await sendHistory(page, key, { endpoint, session }, body);
          expect(first.status, first.text).toBe(200);
          const output = historyOutput(first.text, endpoint, stream);
          if (endpoint === "chat/completions") expect(output.tool_calls.map((call: any) => call.index)).toEqual([0, 1]);
          const results =
            endpoint === "responses"
              ? [
                  { type: "function_call_output", call_id: "call_b", output: "beta-value" },
                  { type: "function_call_output", call_id: "call_a", output: "alpha-value" },
                ]
              : [
                  { role: "tool", tool_call_id: "call_b", content: "beta-value" },
                  { role: "tool", tool_call_id: "call_a", content: "alpha-value" },
                ];
          const follow =
            endpoint === "responses"
              ? { model, stream, previous_response_id: output.id, input: results, tools: [tool] }
              : { ...body, messages: [user, output, ...results] };
          const second = await sendHistory(page, key, { endpoint, session }, follow);
          expect(second.status, second.text).toBe(200);
          expect(second.text).toContain("history-ok");
          if (endpoint === "responses") {
            const full = await sendHistory(
              page,
              key,
              { endpoint, session },
              { model, stream, input: [user, ...output.output, ...results], tools: [tool] },
            );
            expect(full.status, full.text).toBe(200);
            expect(full.text).toContain("history-ok");
          }
          lastCall = second.callID!;
        }
      await expect
        .poll(async () => {
          const bill = await page.request.get(GATEWAY + "/spend/logs/ui/" + lastCall, { headers });
          if (!bill.ok()) return null;
          const row = await bill.json();
          return { status: row.status, spend: Number(row.spend) };
        })
        .toEqual({ status: "success", spend: 0.000012 });
      await stableGoto(page, "/logs");
      await page.getByPlaceholder(tr("Search logs by ID…"), { exact: true }).fill(lastCall);
      await expect(page.getByRole("row").filter({ hasText: lastCall })).toBeVisible();
    } finally {
      if (token)
        expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [token] } })).status()).toBe(
          200,
        );
      for (const id of ids)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
