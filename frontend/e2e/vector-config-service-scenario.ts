import { expect, test as base } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { matchesGatewayResponse } from "./gateway-response.cjs";
import type { Locale } from "../src/i18n/translate";

type Resources = { headers: Record<string, string>; name: string };

/** 向量库配置场景资源夹具；独立请求上下文在页面关闭或测试超时后仍可清理。
 * 参数由 Playwright 注入；返回独占名称与鉴权头，teardown 按名称查回已落库记录并删除。
 * 清理失败继续尝试其他记录并汇总报错；不扩大业务超时，私有 schema 由运行脚本二次清理。
 */
export const test = base.extend<{ vectorResources: Resources }>({
  vectorResources: [async ({ playwright }, runTest) => {
    const api = await playwright.request.newContext({ timeout: 10_000 });
    const state: Resources = { headers: {}, name: `vector-service-${Date.now()}-${Math.random().toString(36).slice(2)}` };
    try { await runTest(state); }
    finally {
      try {
        if (state.headers.Authorization) {
          const response = await api.get(GATEWAY + "/vector_store/list", { headers: state.headers });
          expect(response.status(), "向量库配置清理前列表必须可读").toBe(200);
          const rows = (await response.json()).data as { id: string; name: string }[];
          const results = await Promise.allSettled(rows.filter(row => row.name === state.name).map(async row => {
            const deleted = await api.post(GATEWAY + "/vector_store/delete", { headers: state.headers, data: { vector_store_id: row.id } });
            expect(deleted.status(), "向量库配置清理必须成功").toBe(200);
            expect((await deleted.json()).deleted, "清理必须实际删除目标记录").toBe(1);
          }));
          const failures = results.filter(result => result.status === "rejected");
          expect(failures, "所有向量库配置清理均须成功").toEqual([]);
        }
      } finally { await api.dispose(); }
    }
  }, { timeout: 30_000 }],
});

/** 注册真实向量库配置配置流程；locale 切换控制台语言，原页面中文控件保持既有行为，本次不修改前端文案。
 * 前置独立浏览器、PostgreSQL及离线模型；页面创建/刷新，HTTP编辑、拒绝、恢复、对话、删除，清理由独立夹具保证。
 * 配置存储不等于向量库配置执行；对话只验证管理重构未破坏现有模型通路，计费和权限由真实后台回归验证。
 */
export function registerVectorConfigService(locale: Locale) {
  test("vector configuration persistence and isolation " + locale, async ({ page, vectorResources: resources }) => {
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = resources.headers = { Authorization: "Bearer " + await sessionBearer(page) };
    await stableGoto(page, "/vector-stores");
    await page.getByLabel("名称", { exact: true }).fill(resources.name);
    const created = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/vector_store/new", "POST"));
    await page.getByRole("button", { name: "创建", exact: true }).click();
    const response = await created;
    expect(response.status(), await response.text()).toBe(200);
    const obj = await response.json();
    expect(obj.vector_store_id).toMatch(/^vector_store_/);
    expect(obj.status).toBe("completed");
    expect(obj.file_counts.total).toBe(0);
    const data = { vector_store_id: obj.vector_store_id };
    await expect(page.getByTestId("column-list")).toContainText(resources.name);
    await page.reload();
    await expect(page.getByTestId("column-list")).toContainText(resources.name);
    const updated = await page.request.post(GATEWAY + "/vector_store/update", { headers, data: { ...data, vector_store_name: "Original 用户 input", status: "", file_counts: { total: 0 } } });
    expect(updated.status(), await updated.text()).toBe(200);
    await page.reload();
    await expect(page.getByTestId("column-list")).toContainText("Original 用户 input");
    const invalid = await page.request.post(GATEWAY + "/vector_store/update", { headers: { ...headers, "Content-Type": "application/json" }, data: '{"vector_store_name":' });
    expect(invalid.status()).toBe(400);
    const read = await page.request.post(GATEWAY + "/vector_store/info", { headers, data });
    expect(read.status()).toBe(200);
    expect(await read.json()).toMatchObject({ vector_store_name: "Original 用户 input", status: "", file_counts: { total: 0 } });
    const recovered = await page.request.post(GATEWAY + "/vector_store/update", { headers, data: { ...data, status: null, file_counts: null } });
    expect(recovered.status()).toBe(200);
    expect(await recovered.json()).toMatchObject({ status: "completed", file_counts: { total: 0 } });
    // 配置状态不能充当索引成功证明，真实文件/搜索入口仍明确拒绝。
    for (const suffix of ["files", "search"]) {
      const unsupported = await page.request.post(GATEWAY + "/v1/vector_stores/" + obj.vector_store_id + "/" + suffix, {
        headers, data: { query: "Find a document." },
      });
      expect(unsupported.status(), await unsupported.text()).toBe(501);
    }
    const chat = await page.request.post(GATEWAY + "/v1/chat/completions", { headers, data: {
      model: "gpt-4o-mini", messages: [{ role: "user", content: "Reply with a short greeting." }],
    } });
    expect(chat.status(), await chat.text()).toBe(200);
    expect((await chat.json()).choices[0].message.content).toBe("e2e-ok");
    const deleted = await page.request.post(GATEWAY + "/vector_store/delete", { headers, data });
    expect(deleted.status()).toBe(200);
    expect((await page.request.post(GATEWAY + "/vector_store/info", { headers, data })).status()).toBe(404);
    await page.reload();
    await expect(page.getByTestId("column-list")).not.toContainText(resources.name);
  });
}
