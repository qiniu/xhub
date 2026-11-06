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
    let modelID = "", completionID = "",
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
          model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat", "responses"], pricing_source: "manual" },
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
      // 直接按已确认的失败请求查找；成功请求在独立 Redis 中可能尚未刷入，不作为进入错误页的前置条件。
      await page.getByRole("tab", {name:tr("Error Logs"),exact:true}).click();
      // 页签过渡时两个面板可能同时可见；限定目标页签，保持严格唯一匹配，不依赖动画时长。
      const errorPanel = page.getByRole("tabpanel", {name:tr("Error Logs"),exact:true});
      await errorPanel.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).fill(calls[0]);
      await errorPanel.getByRole("row").filter({hasText:calls[0]}).click();
      const diagnostic=page.getByRole("region",{name:tr("logDiagnostics.title")});
      await expect(diagnostic).toContainText("request_canceled");
      await expect(diagnostic).toContainText(tr("logDiagnostics.cancelUnknown"));
      await expect(diagnostic).toContainText(tr("logDiagnostics.noUpstreamError"));
      await page.keyboard.press("Escape");

      const completionName = name + "-completion";
      const completion = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: completionName,
        litellm_params: { model: completionName, api_base: `http://127.0.0.1:${(upstream.address() as AddressInfo).port}/v1`, api_key:"local-fixture", custom_llm_provider:"openai", input_cost_per_token:0.000002, output_cost_per_token:0.000008 },
        model_info: {transport:"openai_completions",endpoint_types:["completion"],pricing_source:"manual"},
      } });
      expect(completion.status(), await completion.text()).toBe(200);
      completionID = (await completion.json()).model_info.id;
      const beforeStopped = stopped;
      const rawCall = await page.evaluate(async ({token, model}) => {
        const controller = new AbortController();
        // eslint-disable-next-line no-restricted-syntax
        const response = await fetch("/gw/completions", {method:"POST",headers:{Authorization:token,"Content-Type":"application/json"},body:JSON.stringify({model,prompt:"Test cancellation.",stream:true}),signal:controller.signal});
        const reader = response.body!.getReader();
        await reader.read();
        const id = response.headers.get("x-litellm-call-id");
        controller.abort();
        return {id,status:response.status};
      }, {token:headers.Authorization,model:completionName});
      expect(rawCall.status).toBe(200);
      expect(rawCall.id).toBeTruthy();
      await expect.poll(() => stopped).toBe(beforeStopped + 1);
      await expect.poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + rawCall.id, {headers});
        if (!detail.ok()) return null;
        const row = await detail.json();
        return {status:row.status,spend:Number(row.spend)};
      }).toEqual({status:"error",spend:0});
      await stableGoto(page, "/logs?log_id=" + rawCall.id);
      await expect(page.getByRole("dialog").getByRole("alert")).toBeVisible();
      await page.keyboard.press("Escape");

      // Responses 经 Chat 上游转换；真实浏览器读取首帧后中断，确认中断传播及零费用日志。
      const beforeBridgeStopped = stopped;
      const bridgeCall = await page.evaluate(async ({token, model}) => {
        const controller = new AbortController();
        const response = await fetch("/gw/responses", {method:"POST",headers:{Authorization:token,"Content-Type":"application/json"},body:JSON.stringify({model,input:"Test cancellation through conversion.",stream:true}),signal:controller.signal});
        const reader = response.body!.getReader();
        await reader.read();
        const id = response.headers.get("x-litellm-call-id");
        controller.abort();
        return {id,status:response.status};
      }, {token:headers.Authorization,model:name});
      expect(bridgeCall.status).toBe(200);expect(bridgeCall.id).toBeTruthy();
      await expect.poll(() => stopped).toBe(beforeBridgeStopped + 1);
      await expect.poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + bridgeCall.id, {headers});
        if (!detail.ok()) return null;
        const row = await detail.json();
        return {status:row.status,spend:Number(row.spend)};
      }).toEqual({status:"error",spend:0});
      await stableGoto(page, "/logs?log_id=" + bridgeCall.id);
      await expect(page.getByRole("dialog").getByRole("alert")).toBeVisible();

      await page.keyboard.press("Escape");

      // 通过只存在于隔离测试网关的写入器注入刷新失败，验证真实日志页面与零费用。
      const flushCall = await page.evaluate(async ({token, model}) => {
        // eslint-disable-next-line no-restricted-syntax
        const response = await fetch("/gw/completions", {method:"POST",headers:{Authorization:token,"Content-Type":"application/json","X-Xhub-E2E-Flush-Failure":"1"},body:JSON.stringify({model,prompt:"Test flush failure.",stream:true})});
        await response.text();
        return response.headers.get("x-litellm-call-id");
      }, {token:headers.Authorization,model:completionName});
      expect(flushCall).toBeTruthy();
      await expect.poll(async () => {
        const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + flushCall, {headers});
        if (!detail.ok()) return null;
        const row = await detail.json();
        return {status:row.status,spend:Number(row.spend),kind:row.metadata.upstream_response.failure?.kind};
      }).toEqual({status:"error",spend:0,kind:"client_write_error"});
      await stableGoto(page, "/logs?log_id=" + flushCall);
      await expect(page.getByRole("dialog")).toContainText(tr("logsUX.clientWriteFailed"));
      await expect(page.getByRole("region",{name:tr("logDiagnostics.title")})).toContainText(tr("logDiagnostics.clientWriteError"));
    } finally {
      if (key)
        expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })).status()).toBe(
          200,
        );
      if (modelID)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(
          200,
        );
      if (completionID)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: completionID } })).status()).toBe(200);
      upstream.closeAllConnections();
      await new Promise<void>((resolve, reject) => upstream.close((error) => (error ? reject(error) : resolve())));
    }
  });
}
