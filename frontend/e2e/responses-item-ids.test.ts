import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";

/** 从真实 JSON/SSE 提取完成对象，参数为正文和流模式，返回 Responses 对象；缺少终态即失败，不写外部数据。 */
function completed(raw: string, stream: boolean) {
  if (!stream) return JSON.parse(raw);
  const line = raw.split("\n").find((line) => line.startsWith("data:") && line.includes('"type":"response.completed"'));
  if (!line) throw new Error("Responses stream did not complete");
  return JSON.parse(line.slice(5)).response;
}

/** 经浏览器同源代理原样回放历史；参数为页面、会话令牌和正文，返回 HTTP 状态及结果，临时数据由场景与隔离 schema 清理。 */
async function replay(page: Page, bearer: string, body: Record<string, unknown>) {
  return page.evaluate(
    async ({ bearer, body }) => {
      const response = await fetch("/gw/v1/responses", {
        method: "POST",
        headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      return { status: response.status, raw: await response.text(), callID: response.headers.get("x-litellm-call-id") };
    },
    { bearer, body },
  );
}

for (const stream of [false, true]) {
  /** 前置真实浏览器/Go 后台/隔离数据库和严格 ID 上游；验证保存模型、页面切换、无来源推理的 JSON/SSE 完整历史与旧历史回放、错误和日志计费。
   * 只用本地声明凭据；finally 删除四个部署和连接，运行器删除身份、账单与 schema。 */
  test("Responses item IDs survive model switch " + (stream ? "SSE" : "JSON"), async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    const bearer = await sessionBearer(page),
      headers = { Authorization: "Bearer " + bearer };
    const source = "item-id-source-" + stream,
      grok = "item-id-grok-" + stream,
      tracked = "item-id-tracked-" + stream,
      target = "item-id-target-" + stream,
      credential = "item-id-connection-" + stream;
    const ids: string[] = [];
    expect(
      (
        await page.request.post(GATEWAY + "/credentials", {
          headers,
          data: {
            credential_name: credential,
            credential_info: { custom_llm_provider: "openai" },
            credential_values: { api_base: UPSTREAM + "/v1", api_key: "sk-fake" },
          },
        })
      ).status(),
    ).toBe(200);
    try {
      for (const [name, upstream, transport] of [
        [source, "e2e-item-id-chat", "bypass_openai_chat"],
        [grok, "e2e-item-id-grok", "bypass_openai_responses"],
        [tracked, "e2e-item-id-tracked", "bypass_openai_responses"],
        [target, "e2e-item-id-native", "bypass_openai_responses"],
      ]) {
        const response = await page.request.post(GATEWAY + "/model/new", {
          headers,
          data: {
            model_name: name,
            litellm_params: {
              model: upstream,
              custom_llm_provider: "openai",
              litellm_credential_name: credential,
              input_cost_per_token: 0.000001,
              output_cost_per_token: 0.000002,
            },
            model_info: { transport, endpoint_types: ["responses"], pricing_source: "manual" },
          },
        });
        expect(response.status(), await response.text()).toBe(200);
        ids.push((await response.json()).model_info.id);
      }
      await page.evaluate((stream) => sessionStorage.setItem("streamingEnabled", JSON.stringify(stream)), stream);
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: source, exact: true }).click();
      await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
      await page
        .getByRole("option")
        .filter({ hasText: /(?:^|\s)\/v1\/responses$/ })
        .click();
      const sessionSwitch = page.getByRole("switch", { name: t("Use API session management") });
      if (await sessionSwitch.isChecked()) await sessionSwitch.click();
      await page
        .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
        .filter({ visible: true })
        .fill("Reply with a greeting.");
      const firstPending = page.waitForResponse(
        (r) => /\/responses$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: t("Send message") }).click();
      const first = await firstPending;
      expect(first.status()).toBe(200);
      const result = completed(await first.text(), stream);
      // 统一入口的推理不暴露来源引用；消息仍必须使用合法且稳定的 msg_ 标识。
      for (const item of result.output) {
        if (item.type === "reasoning") {
          for (const field of ["id", "status", "encrypted_content"]) expect(item).not.toHaveProperty(field);
        } else {
          expect(item.id).toMatch(/^msg_xhub_[a-f0-9]{32}$/);
        }
      }
      await expect(page.getByText("e2e-source", { exact: true }).last()).toBeVisible();
      await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: target, exact: true }).click();
      await page
        .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
        .filter({ visible: true })
        .fill("Continue the conversation.");
      const nextPending = page.waitForResponse(
        (r) => /\/responses$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: t("Send message") }).click();
      const next = await nextPending;
      expect(next.status(), await next.text()).toBe(200);
      await expect(page.getByText("e2e-switched", { exact: true }).last()).toBeVisible();
      const input = [{ role: "user", content: "Hello" }, ...result.output, { role: "user", content: "Continue" }];
      const calls = [first.headers()["x-litellm-call-id"], next.headers()["x-litellm-call-id"]];
      for (const mode of [
        "current",
        "legacy-chat",
        "legacy-grok",
        "native-grok",
        "tracked-gpt",
        "function-new",
        "function-legacy",
      ]) {
        let history = structuredClone(input);
        if (mode === "native-grok") {
          const sourceReply = await replay(page, bearer, {
            model: grok,
            input: [{ role: "user", content: "Hello" }],
            stream,
          });
          expect(sourceReply.status, sourceReply.raw).toBe(200);
          const grokOutput = completed(sourceReply.raw, stream).output;
          expect(grokOutput[0]).toMatchObject({ type: "reasoning" });
          expect(grokOutput[0].summary).not.toHaveLength(0);
          for (const field of ["id", "status", "encrypted_content"]) expect(grokOutput[0]).not.toHaveProperty(field);
          history = [{ role: "user", content: "Hello" }, ...grokOutput, { role: "user", content: "Continue" }];
        }
        if (mode === "tracked-gpt") {
          const sourceReply = await replay(page, bearer, {
            model: tracked,
            input: [{ role: "user", content: "Hello" }],
            stream,
          });
          expect(sourceReply.status, sourceReply.raw).toBe(200);
          const trackedOutput = completed(sourceReply.raw, stream).output;
          expect(trackedOutput[0].metadata).toEqual({ turn_id: "trace" });
          history = [{ role: "user", content: "Hello" }, ...trackedOutput, { role: "user", content: "Continue" }];
        }
        if (mode.startsWith("function-")) {
          const toolReply = await replay(page, bearer, {
            model: source,
            input: [{ role: "user", content: "Call lookup_value." }],
            tools: [{ type: "function", name: "lookup_value", parameters: { type: "object" } }],
            stream,
          });
          expect(toolReply.status, toolReply.raw).toBe(200);
          const toolOutput = completed(toolReply.raw, stream).output;
          const call = toolOutput.find((item: { type: string }) => item.type === "function_call");
          expect(call.id).toMatch(/^fc_xhub_[a-f0-9]{32}$/);
          expect(call.call_id).toBe("call-e2e");
          if (mode === "function-legacy") call.id = call.call_id;
          history = [
            { role: "user", content: "Call lookup_value." },
            ...toolOutput,
            { type: "function_call_output", call_id: call.call_id, output: "KNOWN_TEST_VALUE" },
          ];
        }
        if (mode.startsWith("legacy-"))
          for (const item of history)
            if (item.type === "message" || item.type === "reasoning")
              item.id =
                (mode === "legacy-grok" ? "92ae29c5-9e2c-9cbd-a94b-2fd2abc135be_" : "chatcmpl-old_") +
                (item.type === "message" ? "message" : "reasoning");
        const reply = await replay(page, bearer, { model: target, input: history, stream, store: false });
        expect(reply.status, reply.raw).toBe(200);
        expect(completed(reply.raw, stream).output[0].id).toBe("msg_e2e_native");
        calls.push(reply.callID!);
      }
      for (const callID of calls)
        await expect
          .poll(async () => {
            const response = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
            if (!response.ok()) return null;
            const bill = await response.json();
            return { status: bill.status, spend: Number(bill.spend) };
          })
          .toEqual({ status: "success", spend: 0.000012 });
      const invalid = await replay(page, bearer, {
        model: target,
        input: [
          {
            type: "message",
            id: "foreign-invalid",
            role: "assistant",
            content: [{ type: "output_text", text: "Hello" }],
          },
        ],
      });
      expect(invalid.status).toBe(400);
      await stableGoto(page, "/logs");
      await expect(page.getByText(target, { exact: true }).first()).toBeVisible();
    } finally {
      for (const id of ids)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
