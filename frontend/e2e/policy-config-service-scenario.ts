import { expect, test as base } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { matchesGatewayResponse } from "./gateway-response.cjs";
import type { Locale } from "../src/i18n/translate";

type Resources = { headers: Record<string, string>; name: string };

/** 策略场景资源夹具；独立请求上下文在页面关闭或测试超时后仍可清理。
 * 参数由 Playwright 注入；返回独占名称与鉴权头，teardown 按名称查回已落库记录并删除。
 * 清理失败继续尝试其他记录并汇总报错；不扩大业务超时，私有 schema 由运行脚本二次清理。
 */
export const test = base.extend<{ policyResources: Resources }>({
  policyResources: [async ({ playwright }, runTest) => {
    const api = await playwright.request.newContext({ timeout: 10_000 });
    const state: Resources = { headers: {}, name: `policy-service-${Date.now()}-${Math.random().toString(36).slice(2)}` };
    try { await runTest(state); }
    finally {
      try {
        if (state.headers.Authorization) {
          const response = await api.get(GATEWAY + "/policies/list", { headers: state.headers });
          expect(response.status(), "策略清理前列表必须可读").toBe(200);
          const rows = (await response.json()).policies as { id: string; name: string }[];
          const results = await Promise.allSettled(rows.filter(row => row.name === state.name).map(async row => {
            const deleted = await api.delete(GATEWAY + "/policies/" + encodeURIComponent(row.id), { headers: state.headers });
            expect(deleted.status(), "策略清理必须成功").toBe(200);
          }));
          const failures = results.filter(result => result.status === "rejected");
          expect(failures, "所有策略清理均须成功").toEqual([]);
        }
      } finally { await api.dispose(); }
    }
  }, { timeout: 30_000 }],
});

/** 注册真实策略配置流程；locale 切换控制台语言，原页面中文控件保持既有行为，本次不修改前端文案。
 * 前置独立浏览器、PostgreSQL及离线模型；页面创建/刷新，HTTP编辑、拒绝、恢复、对话、删除，清理由独立夹具保证。
 * 配置存储不等于策略执行；对话只验证管理重构未破坏现有模型通路，计费和权限由真实后台回归验证。
 */
export function registerPolicyConfigService(locale: Locale) {
  test("policy configuration persistence and isolation " + locale, async ({ page, policyResources: resources }) => {
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = resources.headers = { Authorization: "Bearer " + await sessionBearer(page) };
    await stableGoto(page, "/policies");
    await page.getByLabel("名称", { exact: true }).fill(resources.name);
    const created = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/policies", "POST"));
    await page.getByRole("button", { name: "创建", exact: true }).click();
    const response = await created;
    expect(response.status(), await response.text()).toBe(200);
    const obj = await response.json();
    expect(obj.policy_id).toMatch(/^policie_/);
    expect(obj.status).toBe("active");
    expect(obj.version).toBe(1);
    const path = GATEWAY + "/policies/" + encodeURIComponent(obj.policy_id);
    await expect(page.getByTestId("column-list")).toContainText(resources.name);
    await page.reload();
    await expect(page.getByTestId("column-list")).toContainText(resources.name);
    const updated = await page.request.put(path, { headers, data: { policy_name: "Original 用户 input", status: "inactive", version: 0 } });
    expect(updated.status(), await updated.text()).toBe(200);
    await page.reload();
    await expect(page.getByTestId("column-list")).toContainText("Original 用户 input");
    const invalid = await page.request.put(path, { headers: { ...headers, "Content-Type": "application/json" }, data: '{"policy_name":' });
    expect(invalid.status()).toBe(400);
    const read = await page.request.get(path, { headers });
    expect(read.status()).toBe(200);
    expect(await read.json()).toMatchObject({ policy_name: "Original 用户 input", status: "inactive", version: 0 });
    const recovered = await page.request.put(path, { headers, data: { status: "active", version: null } });
    expect(recovered.status()).toBe(200);
    expect(await recovered.json()).toMatchObject({ status: "active", version: 1 });
    const chat = await page.request.post(GATEWAY + "/v1/chat/completions", { headers, data: {
      model: "gpt-4o-mini", messages: [{ role: "user", content: "Reply with a short greeting." }],
    } });
    expect(chat.status(), await chat.text()).toBe(200);
    expect((await chat.json()).choices[0].message.content).toBe("e2e-ok");
    const deleted = await page.request.delete(path, { headers });
    expect(deleted.status()).toBe(200);
    expect((await page.request.get(path, { headers })).status()).toBe(404);
    await page.reload();
    await expect(page.getByTestId("column-list")).not.toContainText(resources.name);
  });
}
