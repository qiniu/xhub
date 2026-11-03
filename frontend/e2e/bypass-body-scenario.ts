import { test, expect } from "@playwright/test";
import { createServer } from "node:http";
import { once } from "node:events";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 验证双语控制台到 Bypass 的原文保留、完整流、上游失败和日志可见性。
 * 前置真实浏览器/网关/隔离数据库，本地 HTTP 供应商记录实际出站正文；finally 删除模型、关闭服务，运行器回收 schema 和账单。
 */
export function registerBypassBody(locale: "en" | "zh-CN") {
  test(`bypass request body and log lifecycle ${locale}`, async ({ page }) => {
    const received: string[] = [];
    let reject = false;
    // 此供应商仅产生确定的协议回复，网关、模型配置、浏览器代理和记账全部走真实代码。
    const upstream = createServer(async (req, res) => {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      received.push(Buffer.concat(chunks).toString());
      if (req.url !== "/v1/responses" || req.headers.authorization !== "Bearer fixture-key") {
        res.writeHead(400).end("Invalid upstream target or credential");
        return;
      }
      if (reject) {
        res.writeHead(422, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: { type: "fixture_rejected", message: "Payload fixture rejected" } }));
        return;
      }
      const response = {
        id: `resp_body_${locale}_${received.length}`, object: "response", status: "completed", model: "wire-model",
        output: [{ type: "message", role: "assistant", status: "completed", content: [{ type: "output_text", text: "Payload retained", annotations: [] }] }],
        usage: { input_tokens: 8, output_tokens: 2, total_tokens: 10 },
      };
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      res.end(`event: response.completed\r\ndata: ${JSON.stringify({ type: "response.completed", response })}\r\n\r\n`);
    });
    upstream.listen(0, "127.0.0.1");
    await once(upstream, "listening");
    const port = (upstream.address() as { port: number }).port;
    let modelID = "";
    let headers: Record<string, string> = {};
    try {
      await loginAdmin(page);
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      const bearer = await sessionBearer(page);
      headers = { Authorization: "Bearer " + bearer };
      const model = `body-contract-${locale}-${Date.now()}`;
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: model,
        litellm_params: { model: "wire-model", custom_llm_provider: "openai", api_base: `http://127.0.0.1:${port}/v1`, api_key: "fixture-key", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_openai_responses", endpoint_types: ["responses", "bypass:openai-responses"], pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200);
      modelID = (await created.json()).model_info.id;
      expect(modelID).toBeTruthy();
      await stableGoto(page, "/mine-models");
      await page.getByRole("textbox", { name: translate(locale, "Search"), exact: true }).fill(model);
      await expect(page.getByRole("heading", { name: model, exact: true })).toBeVisible();

      for (const fail of [false, true, false]) {
        reject = fail;
        const before = received.length;
        // 使用原始 JSON 传大整数，不能先经 JavaScript number 舍入；中英文用户输入也必须原样保留。
        const body = `{"model":${JSON.stringify(model)},"input":"Reply briefly. 保留用户原文","stream":true,"session_id":"body-browser-${locale}","disable_fallbacks":true,"opaque":9007199254740993,"vendor":{"nullable":null,"empty":"","zero":0}}`;
        const got = await page.evaluate(async ({ bearer, body }) => {
          // eslint-disable-next-line no-restricted-syntax
          const response = await fetch("/gw/bypass/openai/v1/responses", { method: "POST", headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json" }, body });
          return { status: response.status, text: await response.text(), callID: response.headers.get("x-litellm-call-id") };
        }, { bearer, body });
        expect(got.status, got.text).toBe(fail ? 422 : 200);
        expect(received.length, "成功和失败均不能重复调用供应商").toBe(before + 1);
        const wire = received[before];
        expect(wire).toContain('"opaque":9007199254740993');
        const parsed = JSON.parse(wire);
        expect(parsed.model).toBe("wire-model");
        expect(parsed.input).toBe("Reply briefly. 保留用户原文");
        expect(parsed.vendor).toEqual({ nullable: null, empty: "", zero: 0 });
        expect(parsed).not.toHaveProperty("session_id");
        expect(parsed).not.toHaveProperty("disable_fallbacks");
        expect(got.text).toContain(fail ? "Payload fixture rejected" : "Payload retained");
        if (!fail) {
          expect(got.text).toContain("response.completed");
          expect(got.text).toContain("\r\n\r\n");
        }
        expect(got.callID).toBeTruthy();
        await expect.poll(async () => (await page.request.get(GATEWAY + "/spend/logs/ui/" + got.callID, { headers })).status()).toBe(200);
        const log = await (await page.request.get(GATEWAY + "/spend/logs/ui/" + got.callID, { headers })).json();
        expect(log.status).toBe(fail ? "error" : "success");
        expect(log.spend).toBeCloseTo(fail ? 0 : 0.000012, 10);
        await stableGoto(page, "/logs?log_id=" + got.callID);
        const dialog = page.getByRole("dialog");
        await expect(dialog).toBeVisible();
        if (fail) await expect(dialog.getByRole("alert")).toContainText("422");
        else await expect(dialog).toContainText(model);
        await page.keyboard.press("Escape");
      }
    } finally {
      try {
        if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(200);
      } finally {
        upstream.closeAllConnections();
        await new Promise<void>((resolve, reject) => upstream.close(err => err ? reject(err) : resolve()));
      }
    }
  });
}
