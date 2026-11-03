import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 浏览器通过真实同源代理回放完整历史，参数为页面、临时令牌和请求，返回状态、终态和账单 ID；不修改输入，不替换网络。 */
async function request(page: Page, bearer: string, body: Record<string, unknown>) {
  return page.evaluate(
    async ({ bearer, body }) => {
      const response = await fetch("/gw/v1/responses", {
        method: "POST",
        headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const raw = await response.text();
      const result = response.ok
        ? body.stream
          ? JSON.parse(
              raw
                .split("\n")
                .find((line) => line.startsWith("data:") && line.includes('"response.completed"'))!
                .slice(5),
            ).response
          : JSON.parse(raw)
        : null;
      return {
        status: response.status,
        raw,
        result,
        callID: response.headers.get("x-litellm-call-id"),
        mode: response.headers.get("x-xhub-history-mode"),
      };
    },
    { bearer, body },
  );
}

for (const locale of ["zh-CN", "en"] as const) for (const stream of [false, true]) {
  /** 前置真实浏览器、Go 后台、隔离数据库和严格加密边界上游；验证页面切换三类模型、外部历史往返、工具关联、错误与账单。
   * 中英文使用运行器语言项目；finally 删除模型凭据，schema 回收身份和账单，无付费供应商依赖。 */
  test("Portable reasoning survives model and provider round trip " + locale + " " + stream, async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    const t = (key: string) => translate(locale, key);
    if (locale === "en") await page.getByRole("button", { name: translate("zh-CN", "language.en"), exact: true }).click();
    const bearer = await sessionBearer(page),
      headers = { Authorization: "Bearer " + bearer };
    const credential = "portable-credential-" + stream,
      names = ["portable-gpt-", "portable-grok-", "portable-deepseek-"].map((name) => name + stream);
    const ids: string[] = [],
      callIDs: string[] = [];
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
      for (let index = 0; index < names.length; index++) {
        const saved = await page.request.post(GATEWAY + "/model/new", {
          headers,
          data: {
            model_name: names[index],
            litellm_params: {
              model: ["e2e-portable-gpt", "e2e-portable-grok", "e2e-portable-deepseek"][index],
              custom_llm_provider: "openai",
              litellm_credential_name: credential,
              input_cost_per_token: 0.000001,
              output_cost_per_token: 0.000002,
            },
            model_info: {
              transport: index === 2 ? "bypass_openai_chat" : "bypass_openai_responses",
              endpoint_types: ["responses"],
              pricing_source: "manual",
            },
          },
        });
        expect(saved.status(), await saved.text()).toBe(200);
        ids.push((await saved.json()).model_info.id);
      }
      await page.evaluate((stream) => sessionStorage.setItem("streamingEnabled", JSON.stringify(stream)), stream);
      await stableGoto(page, "/playground");
      for (const name of [...names, names[0]]) {
        await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
        await page.getByRole("option", { name, exact: true }).click();
        await page.getByPlaceholder(t("Select an endpoint"), { exact: true }).click();
        await page
          .getByRole("option")
          .filter({ hasText: /(?:^|\s)\/v1\/responses$/ })
          .click();
        const session = page.getByRole("switch", { name: t("Use API session management") });
        if (await session.isChecked()) await session.click();
        await page
          .getByPlaceholder(t("Type your message... (Shift+Enter for new line)"))
          .filter({ visible: true })
          .fill("Continue this conversation.");
        const pending = page.waitForResponse(
          (r) => /\/responses$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
        );
        await page.getByRole("button", { name: t("Send message") }).click();
        const reply = await pending;
        expect(reply.status(), await reply.text()).toBe(200);
        expect(await reply.text()).not.toContain("encrypted_content");
        await expect(page.getByText("portable-ok", { exact: true }).last()).toBeVisible();
        callIDs.push(reply.headers()["x-litellm-call-id"]);
      }
      const foreign = {
        type: "reasoning",
        id: "rs_outside",
        encrypted_content: "outside-cipher",
        summary: [{ type: "summary_text", text: "Public plan" }],
      };
      const history: any[] = [
        { role: "user", content: "用户原文" },
        foreign,
        { role: "assistant", content: "Earlier answer" },
        { role: "user", content: "Read a file." },
      ];
      const original = JSON.stringify(history);
      const first = await request(page, bearer, {
        model: names[0],
        input: history,
        stream,
        tools: [{ type: "function", name: "read", parameters: { type: "object" } }],
      });
      expect(first.status, first.raw).toBe(200);
      expect(first.mode).toBe("portable");
      expect(first.raw).not.toContain("encrypted_content");
      expect(first.result.output.find((item: any) => item.type === "function_call").call_id).toBe("call_portable");
      expect(JSON.stringify(history)).toBe(original);
      history.push(...first.result.output, {
        type: "function_call_output",
        call_id: "call_portable",
        output: "file-value",
      });
      for (const name of [names[1], names[0]]) {
        const next = await request(page, bearer, { model: name, input: history, stream });
        expect(next.status, next.raw).toBe(200);
        expect(next.raw).not.toContain("encrypted_content");
        history.push(...next.result.output);
        callIDs.push(next.callID!);
      }
      // 原供应商仍接受原密文，严格夹具拒绝任何 XHub 新账户密文，证明往返不污染客户端历史。
      const outside = await page.request.post(UPSTREAM + "/v1/responses", {
        data: { model: "e2e-portable-outside", input: history },
      });
      expect(outside.status(), await outside.text()).toBe(200);
      expect(history).toContain(foreign);
      callIDs.push(first.callID!);
      const invalid = await request(page, bearer, { model: names[0], input: 42 });
      expect(invalid.status).toBe(400);
      for (const callID of callIDs)
        await expect
          .poll(async () => {
            const reply = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
            if (!reply.ok()) return null;
            const bill = await reply.json();
            return { status: bill.status, spend: Number(bill.spend) };
          })
          .toEqual({ status: "success", spend: 0.000012 });
      await stableGoto(page, "/logs");
      await expect(page.getByText(names[0], { exact: true }).first()).toBeVisible();
    } finally {
      for (const id of ids)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
      expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
    }
  });
}
