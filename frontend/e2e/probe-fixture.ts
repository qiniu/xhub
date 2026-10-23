import { createServer } from "node:http";

export type ProbeRequest = { path: string; body: Record<string, unknown>; authorization?: string; searchKey?: string };
export type ProbeFixture = { url: string; requests: ProbeRequest[]; failure: boolean; close: () => Promise<void> };

/** 为真实浏览器→网关测试启动本机协议端点；无参数，返回独占地址、请求证据、失败开关和关闭方法。
 * 只接受测试Key，严格校验JSON协议；失败模式模拟上游403/429，不拦截网关响应。调用方finally关闭server，无生产访问。
 */
export async function startProbeFixture(): Promise<ProbeFixture> {
  const requests: ProbeRequest[] = [];
  const fixture: ProbeFixture = {
    url: "", requests, failure: false,
    close: () => new Promise<void>((resolve, reject) => {
      server.close(error => error ? reject(error) : resolve());
      server.closeAllConnections();
    }),
  };
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = [];
    for await (const chunk of request) chunks.push(Buffer.from(chunk));
    const raw = Buffer.concat(chunks).toString("utf8");
    let body: Record<string, unknown>;
    try { body = JSON.parse(raw); } catch { response.writeHead(400).end(); return; }
    const path = request.url ?? "";
    requests.push({ path, body, authorization: request.headers.authorization, searchKey: request.headers["x-api-key"] as string | undefined });
    response.setHeader("Content-Type", "application/json");
    if (request.headers.authorization !== "Bearer fixture-probe-key") {
      response.writeHead(401).end(JSON.stringify({ error: { message: "invalid fixture credential" } }));
      return;
    }
    if (fixture.failure) {
      response.writeHead(path === "/search" ? 429 : 403).end(JSON.stringify({ error: { type: "fixture_error", message: "controlled probe rejection" } }));
      return;
    }
    if (path === "/v1/chat/completions" && Array.isArray(body.messages) && body.stream !== true) {
      const last = body.messages[body.messages.length - 1] as { content: string };
      response.writeHead(200).end(JSON.stringify({
        id: "chatcmpl-real-probe", object: "chat.completion", model: body.model,
        choices: [{ index: 0, message: { role: "assistant", content: "Rendered: " + last.content }, finish_reason: "stop" }],
        usage: { prompt_tokens: 8, completion_tokens: 2, total_tokens: 10 },
      }));
      return;
    }
    if (path === "/search" && typeof body.query === "string" && body.query.length > 0) {
      response.writeHead(200).end(JSON.stringify({ results: [{ title: "Fixture result", url: "https://example.com/probe", content: "Found: " + body.query }] }));
      return;
    }
    response.writeHead(400).end(JSON.stringify({ error: { message: "unexpected fixture protocol" } }));
  });
  await new Promise<void>((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  const address = server.address();
  if (address === null || typeof address === "string") throw new Error("probe fixture did not bind a TCP port");
  fixture.url = `http://127.0.0.1:${address.port}`;
  return fixture;
}
