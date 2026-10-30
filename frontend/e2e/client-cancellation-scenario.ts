import { expect, test } from "@playwright/test";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate, type Locale } from "../src/i18n/translate";

/** 注册一个语言的完整浏览器场景；参数locale选择界面语言，测试体管理临时资源，调用本身不请求后台。 */
export function registerClientScenario(locale: Locale) {
  /** 验证浏览器连续停止三次真实生成后，另一Key仍能调用同部署且取消日志零费用。
   * 前置真实浏览器/网关/私有库和本例本地HTTP上游；测试不拦截浏览器请求，finally关闭上游并删除模型/Key，运行器回收schema。
   * Redis共享冷却细节由真实后台回归覆盖；本例覆盖双语按钮到数据面的完整停止流程。
   */
  test(`client cancellation keeps a healthy model available ${locale}`, async ({ page }) => {
    test.setTimeout(90000);
    const tr = (key: string) => translate(locale, key);
    let started = 0,
      stopped = 0;
    const upstream = createServer((req, res) => {
      let raw = "";
      req.on("data", (part) => {
        raw += part;
      });
      req.on("end", () => {
        const body = JSON.parse(raw);
        if (body.stream) {
          started++;
          res.writeHead(200, { "Content-Type": "text/event-stream" });
          res.write(
            'data: {"id":"chatcmpl_cancel","choices":[{"index":0,"delta":{"content":"CANCEL_PARTIAL"}}],"usage":{"prompt_tokens":8,"completion_tokens":2}}\n\n',
          );
          const timer = setInterval(() => res.write(": waiting\n\n"), 100);
          res.on("close", () => {
            clearInterval(timer);
            stopped++;
          });
        } else {
          res.writeHead(200, { "Content-Type": "application/json" });
          res.end(
            JSON.stringify({
              id: "chatcmpl_recovered",
              choices: [{ index: 0, message: { role: "assistant", content: "HEALTHY_OK" }, finish_reason: "stop" }],
              usage: { prompt_tokens: 8, completion_tokens: 2 },
            }),
          );
        }
      });
    });
    await new Promise<void>((resolve) => upstream.listen(0, "127.0.0.1", resolve));
    let modelID = "",
      key = "";
    const name = `e2e-cancel-health-${locale}`;
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: name,
          litellm_params: {
            model: name,
            api_base: `http://127.0.0.1:${(upstream.address() as AddressInfo).port}/v1`,
            api_key: "local-fixture",
            custom_llm_provider: "openai",
            input_cost_per_token: 0.000002,
            output_cost_per_token: 0.000008,
          },
          model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
        },
      });
      expect(created.status(), await created.text()).toBe(200);
      modelID = (await created.json()).model_info.id;
      const issued = await page.request.post(GATEWAY + "/key/generate", {
        headers,
        data: { models: [name], key_alias: name },
      });
      expect(issued.status()).toBe(200);
      key = (await issued.json()).key;
      await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "true"));
      await stableGoto(page, "/playground");
      if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      const input = page
        .getByPlaceholder(tr("Type your message... (Shift+Enter for new line)"))
        .filter({ visible: true });
      const calls: string[] = [];
      for (let index = 0; index < 3; index++) {
        await input.fill(`Generate a long answer ${index}.`);
        const pending = page.waitForResponse(
          (res) => /\/chat\/completions$/.test(new URL(res.url()).pathname) && res.request().method() === "POST",
        );
        await page.getByRole("button", { name: tr("Send message") }).click();
        const response = await pending;
        expect(response.status()).toBe(200);
        expect(response.headers()["cache-control"]).toContain("no-transform");
        expect(response.headers()["content-encoding"]).toBeUndefined();
        calls.push(response.headers()["x-litellm-call-id"]);
        await expect(page.getByText("CANCEL_PARTIAL", { exact: true }).last()).toBeVisible();
        await page.getByRole("button", { name: tr("Stop request"), exact: true }).click();
        await expect.poll(() => stopped).toBe(index + 1);
        await page.getByRole("button", { name: tr("Clear Chat"), exact: true }).click();
      }
      expect(started).toBe(3);
      const healthy = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key },
        data: { model: name, messages: [{ role: "user", content: "Return a healthy response." }] },
      });
      expect(healthy.status(), await healthy.text()).toBe(200);
      expect((await healthy.json()).choices[0].message.content).toBe("HEALTHY_OK");
      for (const callID of calls) {
        expect(callID).toBeTruthy();
        await expect
          .poll(async () => {
            const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
            if (!detail.ok()) return null;
            const row = await detail.json();
            expect(JSON.parse(row.error)).toMatchObject({error:{type:"request_canceled"},upstream_error:null});
            expect(row.metadata.upstream_response.failure).toMatchObject({kind:"request_canceled",cancellation_source:"unknown"});
            return { status: row.status, spend: Number(row.spend) };
          })
          .toEqual({ status: "error", spend: 0 });
      }
      await stableGoto(page, "/logs");
      await expect(page.getByRole("main")).toContainText(name);
      await page.getByRole("tab", {name:tr("Error Logs"),exact:true}).click();
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(calls[0]);
      await page.getByRole("row").filter({hasText:calls[0]}).click();
      const diagnostic=page.getByRole("region",{name:tr("logDiagnostics.title")});
      await expect(diagnostic).toContainText("request_canceled");
      await expect(diagnostic).toContainText(tr("logDiagnostics.cancelUnknown"));
      await expect(diagnostic).toContainText(tr("logDiagnostics.noUpstreamError"));
    } finally {
      if (key)
        expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })).status()).toBe(
          200,
        );
      if (modelID)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(
          200,
        );
      upstream.closeAllConnections();
      await new Promise<void>((resolve, reject) => upstream.close((error) => (error ? reject(error) : resolve())));
    }
  });
}
