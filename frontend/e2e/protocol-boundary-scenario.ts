import { expect, test } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册旧供应商文件/实时协议的双语真实回归；locale 控制页面可访问名称，返回注册结果。
 * 前置隔离数据库和本地 HTTP 模型；登录后核对五个旧文件接口与两条实时别名明确失败、零上游调用，再由页面完成正常对话。
 * 不 mock 路由、不创建业务配置；登录夹具和账单由运行器回收专属 schema，不能指向生产。
 */
export function registerProtocolBoundary(locale: Locale) {
  test("legacy protocol aliases reject false success " + locale, async ({ page }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const before = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(before.status()).toBe(200);
    const baseline = await before.json();
    for (const [method, path] of [
      ["GET", "/fixture-provider/v1/files"], ["POST", "/fixture-provider/v1/files"],
      ["GET", "/fixture-provider/v1/files/job"], ["GET", "/fixture-provider/v1/files/job/content"],
      ["DELETE", "/fixture-provider/v1/files/job"],
    ]) {
      const response = await page.request.fetch(GATEWAY + path, {
        method, headers, data: { filename: "input-fixture", private_fixture: "do-not-echo" },
      });
      expect(response.status(), path + ": " + await response.text()).toBe(501);
      expect(response.headers()["cache-control"]).toBe("no-store");
      expect(await response.text()).toContain("unsupported_operation");
      expect(await response.text()).not.toContain("do-not-echo");
    }
    for (const path of ["/openai_passthrough/responses", "/vertex_ai/live"]) {
      const response = await page.request.get(GATEWAY + path, { headers });
      expect(response.status(), path + ": " + await response.text()).toBe(501);
      expect(await response.text()).toContain("unsupported_protocol_operation");
      expect(response.headers()["cache-control"]).toBe("no-store");
      // 浏览器原生WebSocket不携带Bearer；必须拒绝匿名握手且不能触发open。
      // 授权握手另由真实RFC6455后台回归覆盖，不能用page.request冒充已建连。
      const result = await page.evaluate((url) => new Promise<string>((resolve) => {
        const socket = new WebSocket(url);
        const finish = (result: string) => { clearTimeout(timer); socket.close(); resolve(result); };
        const timer = setTimeout(() => finish("timeout"), 5000);
        socket.onopen = () => finish("opened");
        socket.onerror = () => finish("rejected");
      }), GATEWAY.replace(/^http/, "ws") + path);
      expect(result, "未实现协议不能假握手或悬挂").toBe("rejected");
    }
    const after = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(after.status()).toBe(200);
    expect(await after.json(), "旧操作不得调用供应商").toEqual(baseline);
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
    await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /\/v1\/chat\/completions$/ }).click();
    const prompt = "Verify normal chat after unsupported protocol operations.";
    await page.getByTestId("chat-composer-input").fill(prompt);
    const pending = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/chat/completions") && r.request().method() === "POST");
    await page.getByTestId("chat-send-button").click();
    const response = await pending;
    expect(response.status()).toBe(200);
    await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
    await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
    const called = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(called.status()).toBe(200);
    expect(await called.json(), "正常对话必须真实调用供应商").not.toEqual(baseline);
  });
}
