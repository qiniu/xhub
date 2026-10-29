import { test, expect } from "@playwright/test";
import { createServer } from "node:http";
import { once } from "node:events";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 验证双语页面启停与真实对话并发、恢复后的型号/凭据更新和账单。
 * 前置真实浏览器/网关/隔离PG及本地协议上游；通道确认到达上游后点击停用，不依赖时间等待。
 * finally 释放在途请求、删除模型和凭据并关闭服务，运行器回收 schema 与日志。
 */
export function registerModelView(locale: "en" | "zh-CN") {
  test(`model view hot update ${locale}`, async ({ page }) => {
    const tr = (key: string, params?: Record<string, string>) => translate(locale, key, params);
    const received: { model: string; authorization?: string }[] = [];
    let enter!: () => void;
    let release!: () => void;
    const entered = new Promise<void>(resolve => { enter = resolve; });
    const barrier = new Promise<void>(resolve => { release = resolve; });
    const upstream = createServer(async (req, res) => {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      const body = JSON.parse(Buffer.concat(chunks).toString());
      received.push({ model: body.model, authorization: req.headers.authorization });
      if (received.length === 1) { enter(); await barrier; }
      res.writeHead(200, { "Content-Type": "application/json" });
      const response = { id: `chat_view_${received.length}`, object: "chat.completion", choices: [{ index: 0, message: { role: "assistant", content: "View accepted" }, finish_reason: "stop" }], usage: { prompt_tokens: 8, completion_tokens: 2, total_tokens: 10 } };
      res.end(JSON.stringify(response));
    });
    upstream.listen(0, "127.0.0.1");
    await once(upstream, "listening");
    const port = (upstream.address() as { port: number }).port;
    const credential = `view-${locale}-${Date.now()}`;
    const model = credential;
    let id = "";
    let credentialCreated = false;
    let headers: Record<string, string> = {};
    try {
      await loginAdmin(page);
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      const bearer = await sessionBearer(page);
      headers = { Authorization: "Bearer " + bearer };
      const saved = await page.request.post(GATEWAY + "/credentials", { headers, data: { credential_name: credential, credential_info: { custom_llm_provider: "openai" }, credential_values: { api_base: `http://127.0.0.1:${port}/v1`, api_key: "view-before" } } });
      expect(saved.status(), await saved.text()).toBe(200);
      credentialCreated = true;
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: model,
        litellm_params: { model: "wire-before", custom_llm_provider: "openai", litellm_credential_name: credential, input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat", "bypass:openai-chat"], pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200);
      id = (await created.json()).model_info.id;
      await stableGoto(page, "/models-and-endpoints?model=" + id);
      await expect(page.getByRole("heading", { name: model, exact: true })).toBeVisible();
      const toggle = page.getByRole("switch", { name: tr("providerGuideUI.toggleLabel", { name: model }) });
      /** 浏览器通过控制台真实代理发请求；返回真实状态/正文/日志ID，不 mock 网关响应。 */
      const call = (path: string) => page.evaluate(async ({ bearer, model, path }) => {
        // eslint-disable-next-line no-restricted-syntax
        const response = await fetch("/gw" + path, { method: "POST", headers: { Authorization: "Bearer " + bearer, "Content-Type": "application/json" }, body: JSON.stringify({ model, messages: [{ role: "user", content: "Reply briefly. 保留原文" }] }) });
        return { status: response.status, text: await response.text(), callID: response.headers.get("x-litellm-call-id") };
      }, { bearer, model, path });
      const active = call("/v1/chat/completions");
      await entered;
      let pending = page.waitForResponse(r => new URL(r.url()).pathname === "/model/" + id + "/update" && r.request().method() === "PATCH");
      await toggle.click();
      expect((await pending).status()).toBe(200);
      await expect(toggle).not.toBeChecked();
      for (const path of ["/v1/chat/completions", "/bypass/openai/v1/chat/completions"]) {
        const denied = await call(path);
        expect(denied.status, denied.text).toBeGreaterThanOrEqual(400);
        expect(denied.text).toContain("error");
      }
      expect(received).toHaveLength(1);
      release();
      const completed = await active;
      expect(completed.status, completed.text).toBe(200);
      expect(completed.text).toContain("View accepted");
      expect(received[0]).toEqual({ model: "wire-before", authorization: "Bearer view-before" });
      pending = page.waitForResponse(r => new URL(r.url()).pathname === "/model/" + id + "/update" && r.request().method() === "PATCH");
      await toggle.click();
      expect((await pending).status()).toBe(200);
      await expect(toggle).toBeChecked();
      expect((await page.request.post(GATEWAY + "/model/update", { headers, data: { id, litellm_params: { model: "wire-after" } } })).status()).toBe(200);
      expect((await page.request.patch(GATEWAY + "/credentials/" + credential, { headers, data: { credential_values: { api_key: "view-after" } } })).status()).toBe(200);
      const results = [completed];
      for (const path of ["/v1/chat/completions", "/bypass/openai/v1/chat/completions"]) {
        const result = await call(path);
        expect(result.status, result.text).toBe(200);
        expect(result.text).toContain("View accepted");
        results.push(result);
      }
      expect(received).toEqual([{ model: "wire-before", authorization: "Bearer view-before" }, { model: "wire-after", authorization: "Bearer view-after" }, { model: "wire-after", authorization: "Bearer view-after" }]);
      for (const result of results) {
        expect(result.callID).toBeTruthy();
        await expect.poll(async () => {
          const response = await page.request.get(GATEWAY + "/spend/logs/ui/" + result.callID, { headers });
          if (!response.ok()) return null;
          const log = await response.json();
          return { status: log.status, spend: Number(log.spend) };
        }).toEqual({ status: "success", spend: 0.000012 });
      }
      await stableGoto(page, "/logs?log_id=" + results[2].callID);
      await expect(page.getByRole("dialog")).toContainText(model);
    } finally {
      release();
      try {
        if (id) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
        if (credentialCreated) expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
      } finally {
        upstream.closeAllConnections();
        await new Promise<void>((resolve, reject) => upstream.close(err => err ? reject(err) : resolve()));
      }
    }
  });
}
