import net from "node:net";
import { expect, test } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

/** 通过真实 TCP 发送截断的 HTTP 正文并半关闭写入；返回网关状态与 call id。仅使用隔离 E2E 网关，结束销毁 socket，不记录凭据。 */
async function truncatedUpload(token: string): Promise<{ status: number; id: string }> {
  const address = new URL(GATEWAY);
  return new Promise((resolve, reject) => {
    const socket = net.connect(Number(address.port), address.hostname);
    let response = "";
    const deadline = setTimeout(() => socket.destroy(new Error("Truncated upload fixture did not finish")), 5000);
    socket.on("close", () => clearTimeout(deadline));
    socket.on("connect", () => socket.end(`POST /v1/chat/completions HTTP/1.1\r\nHost: ${address.host}\r\nAuthorization: Bearer ${token}\r\nContent-Length: 100\r\nConnection: close\r\n\r\n{`));
    socket.on("data", (chunk) => { response += chunk.toString(); });
    socket.on("error", reject);
    socket.on("end", () => {
      socket.destroy();
      const status = Number(/^HTTP\/1.1 (\d+)/.exec(response)?.[1]);
      const id = /\r\nx-litellm-call-id:\s*([^\r\n]+)/i.exec(response)?.[1] ?? "";
      resolve({ status, id });
    });
  });
}

/** 注册一个语言下的完整流程：真实截断及超限上传及时返回并保存有界日志、页面显示诊断、自定义时间排除与恢复、切换语言。隔离 schema 由运行器清理。 */
export function registerRequestDiagnostics(locale: "zh-CN" | "en") {
  test(`request failure diagnostics and exact time filtering ${locale}`, async ({ page }) => {
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const token = await sessionBearer(page);
    const headers = { Authorization: `Bearer ${token}` };
    const failure = await truncatedUpload(token);
    expect(failure.status, "截断上传不应再报告413").toBe(400);
    expect(failure.id).toBeTruthy();
    await expect.poll(async () => (await page.request.get(`${GATEWAY}/spend/logs/ui/${failure.id}`, { headers })).status()).toBe(200);
    const detail = await (await page.request.get(`${GATEWAY}/spend/logs/ui/${failure.id}`, { headers })).json();
    expect(detail.metadata.upstream_response.failure).toMatchObject({ phase: "request_body", kind: "unexpected_eof", bytes_read: 1, body_limit: 67108864 });
    // 显式开启正文保存，保证超大上传验证覆盖导致CI变慢的真实路径，并在finally恢复配置。
    const settings = await page.request.get(GATEWAY + "/config/list?config_type=general_settings", { headers });
    expect(settings.status()).toBe(200);
    const logging = (await settings.json()).find((field: {field_name: string}) => field.field_name === "store_prompts_in_spend_logs");
    try {
      expect((await page.request.post(GATEWAY + "/config/update", {headers, data:{general_settings:{store_prompts_in_spend_logs:true}}})).status()).toBe(200);
      const oversized = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers, data:Buffer.alloc((64 << 20) + 1, "x"), timeout:5000,
      });
      expect(oversized.status(), "超大正文应在原5秒时限内返回413").toBe(413);
      const oversizedID = oversized.headers()["x-litellm-call-id"];
      const oversizedDetail = await page.request.get(`${GATEWAY}/spend/logs/ui/${oversizedID}`, {headers});
      expect(oversizedDetail.status()).toBe(200);
      expect(Buffer.byteLength(await oversizedDetail.body()), "超限日志详情不能重复保存64MiB正文").toBeLessThan(16384);
      expect((await oversizedDetail.json()).metadata.upstream_response.failure).toMatchObject({kind:"body_too_large",bytes_read:67108864,body_limit:67108864,request_body_omitted:true});
      await stableGoto(page, "/logs");
      await page.getByRole("tab", { name: translate(locale, "Error Logs"), exact: true }).click();
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(failure.id);
      const row = page.getByRole("row").filter({ hasText: failure.id });
      await expect(row).toBeVisible();
      await row.click();
      const diagnosis = page.getByRole("region", { name: translate(locale, "logDiagnostics.title") });
      await expect(diagnosis).toContainText("unexpected EOF");
      await expect(diagnosis).toContainText(translate(locale, "logDiagnostics.bodyOmitted"));
      await expect(diagnosis).toContainText(translate(locale, "logDiagnostics.noUpstreamError"));
      await page.keyboard.press("Escape");
      await page.getByRole("button", { name: translate(locale, "Last 24 Hours"), exact: true }).click();
      await page.getByRole("button", { name: translate(locale, "Custom Range"), exact: true }).click();
      // 先通过界面选择历史窗口，后台必须返回零行；不 mock 网络或伪造日志时间。
      const start = page.getByLabel(translate(locale, "logDiagnostics.startTime"));
      const end = page.getByLabel(translate(locale, "logDiagnostics.endTime"));
      await start.fill("2020-01-01T00:00");
      const filtered = page.waitForResponse((r) => r.url().includes("/spend/logs/ui?") && r.url().includes("end_date=2020"));
      await end.fill("2020-01-02T00:00");
      expect((await (await filtered).json()).total).toBe(0);
      await expect(row).toHaveCount(0);
      // 将终点移到未来，再收窄起点；完整时间窗口应重新包含这条真实请求。
      await end.fill("2099-01-02T00:00");
      await start.fill("2026-01-01T00:00");
      await expect(row).toBeVisible();
      const target = locale === "en" ? "zh-CN" : "en";
      await page.getByRole("button", { name: target === "en" ? "English" : translate("en", "language.zhCN"), exact: true }).click();
      await expect(page.getByLabel(translate(target, "logDiagnostics.startTime"))).toBeVisible();
      await row.click();
      await expect(page.getByRole("region", { name: translate(target, "logDiagnostics.title") })).toContainText("unexpected EOF");
      await page.keyboard.press("Escape");
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(oversizedID);
      await page.getByRole("row").filter({hasText:oversizedID}).click();
      const oversizedRegion = page.getByRole("region", {name:translate(target,"logDiagnostics.title")});
      await expect(oversizedRegion).toContainText(translate(target,"logDiagnostics.bodyOmitted"));
      await expect(oversizedRegion).toContainText("67108864");
      await expect(oversizedRegion).toContainText("http: request body too large");
      await page.keyboard.press("Escape");
      const issued = await page.request.post(GATEWAY + "/key/generate", {headers,data:{key_alias:"diagnostic-rpm-"+Date.now(),rpm_limit:0}});
      expect(issued.status(),await issued.text()).toBe(200);
      const key=(await issued.json()).key;
      try {
        const limited=await page.request.post(GATEWAY+"/v1/chat/completions",{headers:{Authorization:"Bearer "+key},data:{model:"diagnostic-missing",messages:[{role:"user",content:"Check rate diagnostics."}]}});
        expect(limited.status(),await limited.text()).toBe(429);
        const missing=await page.request.post(GATEWAY+"/v1/chat/completions",{headers,data:{model:"diagnostic-missing",messages:[{role:"user",content:"Check route diagnostics."}]}});
        expect(missing.status(),await missing.text()).toBe(400);
        for (const [response,expected] of [[limited,"key rpm_limit"],[missing,"candidates"]] as const) {
          const id=response.headers()["x-litellm-call-id"];
          await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(id);
          await page.getByRole("row").filter({hasText:id}).click();
          await expect(page.getByRole("region",{name:translate(target,"logDiagnostics.title")})).toContainText(expected);
          await page.keyboard.press("Escape");
        }
      } finally {
        expect((await page.request.post(GATEWAY+"/key/delete",{headers,data:{keys:[key]}})).status()).toBe(200);
      }
    } finally {
      const restored = logging?.stored_in_db
        ? await page.request.post(GATEWAY + "/config/update", {headers,data:{general_settings:{store_prompts_in_spend_logs:logging.field_value}}})
        : await page.request.post(GATEWAY + "/config/field/delete", {headers,data:{config_type:"general_settings",field_name:"store_prompts_in_spend_logs"}});
      expect(restored.status(), "诊断E2E必须恢复正文存储配置").toBe(200);
    }
  });
}
