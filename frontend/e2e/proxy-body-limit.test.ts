import { expect, test } from "@playwright/test";
import { createServer, request as httpRequest } from "node:http";
import type { AddressInfo } from "node:net";
import { createHash } from "node:crypto";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 通过真实 Next 入口发送固定长度或分块正文；参数为地址、凭据、字节及传输模式，返回状态/正文/日志ID。
 * 独立客户端保留10秒失败上限，超时销毁连接；不 mock 网关，不把超时当作预期成功。 */
async function upload(
  url: string,
  token: string,
  body: Buffer,
  chunked = false,
): Promise<{ status: number; body: string; id: string }> {
  return new Promise((resolve, reject) => {
    const req = httpRequest(
      url,
      {
        method: "POST",
        headers: {
          Authorization: "Bearer " + token,
          "Content-Type": "application/json",
          ...(chunked ? {} : { "Content-Length": String(body.length) }),
        },
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("error", reject);
        res.on("end", () =>
          resolve({
            status: res.statusCode!,
            body: Buffer.concat(chunks).toString(),
            id: String(res.headers["x-litellm-call-id"] || ""),
          }),
        );
      },
    );
    const deadline = setTimeout(() => req.destroy(new Error("Proxy upload did not finish within 10 seconds")), 10000);
    req.on("close", () => clearTimeout(deadline));
    req.on("error", reject);
    // 分块模式明确不给 Content-Length，验证代理不能仅靠声明长度放行。
    for (let offset = 0; offset < body.length; offset += 64 * 1024)
      req.write(body.subarray(offset, offset + 64 * 1024));
    req.end();
  });
}

/** 构造精确字节数的合法 JSON；参数为公开模型和总长度，返回英文输入和供应商扩展数据。
 * 扩展字段不参与对话内容；SHA256验证字段完整到达上游，允许网关正常重写模型字段及JSON空白。 */
function sizedBody(model: string, size: number): Buffer {
  const fields = { model, input: "Return BODY_OK.", max_output_tokens: 8, vendor_blob: "" };
  fields.vendor_blob = "x".repeat(size - Buffer.byteLength(JSON.stringify(fields)));
  return Buffer.from(JSON.stringify(fields));
}

/** 真实浏览器登录、Next生产代理、Go网关和PG串联验证大请求、超限日志及双语页面。
 * 本地上游校验完整正文哈希；finally删除测试模型/Key、关闭服务器，运行器清理隔离schema。 */
test("console proxy preserves large bodies and rejects oversize without truncation", async ({ page, baseURL }) => {
  // 可移植控制台模式的测试域名只由Chromium解析；Node客户端仍连接同一个真实Next监听端口。
  const consoleURL = new URL(baseURL!);
  consoleURL.hostname = "127.0.0.1";
  const origin = consoleURL.origin;
  const received: string[] = [];
  const upstream = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (chunk) => chunks.push(chunk));
    req.on("end", () => {
      const payload = JSON.parse(Buffer.concat(chunks).toString());
      received.push(
        typeof payload.vendor_blob === "string"
          ? createHash("sha256").update(payload.vendor_blob).digest("hex")
          : "normalized",
      );
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(
        JSON.stringify({
          id: "resp_body",
          object: "response",
          status: "completed",
          output: [
            {
              id: "msg_body",
              type: "message",
              role: "assistant",
              status: "completed",
              content: [{ type: "output_text", text: "BODY_OK", annotations: [] }],
            },
          ],
          usage: { input_tokens: 4, output_tokens: 1, total_tokens: 5 },
        }),
      );
    });
  });
  await new Promise<void>((resolve) => upstream.listen(0, "127.0.0.1", resolve));
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  const name = "e2e-proxy-body-limit";
  let modelID = "",
    key = "";
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
          input_cost_per_token: 0.000001,
          output_cost_per_token: 0.000002,
        },
        model_info: {
          transport: "bypass_openai_responses",
          endpoint_types: ["responses", "bypass:openai-responses"],
          pricing_source: "manual",
        },
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
    for (const [size, chunked, prefix] of [
      [15 << 20, false, ""],
      [64 << 20, false, "/gw"],
      [15 << 20, true, ""],
    ] as const) {
      const body = sizedBody(name, size);
      const response = await upload(origin + prefix + "/bypass/openai/v1/responses", key, body, chunked);
      expect(response.status, response.body).toBe(200);
      expect(received.at(-1), "代理必须完整保留供应商扩展字段").toBe(
        createHash("sha256").update(JSON.parse(body.toString()).vendor_blob).digest("hex"),
      );
      expect(response.id).toBeTruthy();
    }
    // 线上故障使用标准Responses别名；标准入口仍可正常归一化，但必须先完整读取大正文。
    for (const path of ["/responses", "/gw/v1/responses"]) {
      const response = await upload(origin + path, key, sizedBody(name, 15 << 20));
      expect(response.status, response.body).toBe(200);
      expect(response.body).toContain("BODY_OK");
    }
    let failureID = "";
    for (const [size, chunked] of [
      [(64 << 20) + 1, false],
      [66 << 20, true],
    ] as const) {
      const response = await upload(origin + "/gw/bypass/openai/v1/responses", key, sizedBody(name, size), chunked);
      expect(response.status, response.body).toBe(413);
      expect(response.body).toContain("request_body_too_large");
      failureID = response.id;
      const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + failureID, { headers });
      expect(detail.status()).toBe(200);
      expect((await detail.json()).metadata.upstream_response.failure).toMatchObject({
        phase: "request_body",
        kind: "body_too_large",
        body_limit: 64 << 20,
        request_body_omitted: true,
      });
    }
    expect(received, "拒绝请求不能到达上游").toHaveLength(5);
    await stableGoto(page, "/logs");
    await page.getByRole("tab", { name: translate("zh-CN", "Error Logs"), exact: true }).click();
    await page
      .getByPlaceholder(/按 ID 搜索日志|Search logs by ID/)
      .filter({ visible: true })
      .fill(failureID);
    await page.getByRole("row").filter({ hasText: failureID }).click();
    for (const locale of ["zh-CN", "en"] as const) {
      if (locale === "en") {
        // 详情弹窗打开时外层导航不可访问；先关闭，再像用户一样切换语言并重新打开同一日志。
        await page.keyboard.press("Escape");
        await page.getByRole("button", { name: "English", exact: true }).click();
        await page.getByRole("row").filter({ hasText: failureID }).click();
      }
      const region = page.getByRole("region", { name: translate(locale, "logDiagnostics.title") });
      await expect(region).toContainText("body_too_large");
      await expect(region).toContainText(translate(locale, "logDiagnostics.noUpstreamError"));
    }
  } finally {
    try {
      const removed = await Promise.allSettled([
        ...(key ? [page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })] : []),
        ...(modelID ? [page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })] : []),
      ]);
      for (const result of removed) {
        expect(result.status, "临时资源删除必须可验证").toBe("fulfilled");
        if (result.status === "fulfilled") expect(result.value.status()).toBe(200);
      }
    } finally {
      upstream.closeAllConnections();
      await new Promise<void>((resolve, reject) => upstream.close((err) => (err ? reject(err) : resolve())));
    }
  }
});
