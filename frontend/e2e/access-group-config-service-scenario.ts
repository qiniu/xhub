import { expect, test as base, type APIRequestContext } from "@playwright/test";
import { GATEWAY, login, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate, type Locale } from "../src/i18n/translate";

type Resources = { headers: Record<string, string>; name: string; userID?: string };

/** 清理已登记资源；api独立于页面，state只包含本测试标识，返回完成Promise。
 * 调用方确保管理员头存在；任一清理失败仍尝试剩余资源，最终抛错而非吞掉失败。
 */
async function cleanupAccessResources(api: APIRequestContext, state: Resources) {
  const failures: unknown[] = [];
  try {
    const response = await api.get(GATEWAY + "/v1/access_group", { headers: state.headers });
    expect(response.status(), "访问组清理前列表须可读").toBe(200);
    const rows = await response.json() as { id: string; name: string }[];
    const results = await Promise.allSettled(rows.filter(row => row.name === state.name).map(async row => {
      const deleted = await api.delete(GATEWAY + "/v1/access_group/" + encodeURIComponent(row.id), { headers: state.headers });
      expect(deleted.status(), "访问组清理须成功").toBe(200);
    }));
    failures.push(...results.filter(result => result.status === "rejected"));
  } catch (error) { failures.push(error); }
  // 即便组清理失败也继续删用户，避免首个错误导致剩余测试资源泄漏。
  if (state.userID) {
    try {
      const deleted = await api.post(GATEWAY + "/user/delete", { headers: state.headers, data: { user_id: state.userID } });
      expect(deleted.status(), "用户及Key清理须成功").toBe(200);
    } catch (error) { failures.push(error); }
  }
  expect(failures, "所有独占资源均须清理").toEqual([]);
}

/** 访问组及用户测试夹具；Playwright注入请求工厂，返回独占资源名和管理员头。
 * 独立HTTP上下文保证页面关闭/业务超时后仍清理；按名称查回已保存组，删除用户及Key，清理失败必须使测试失败。
 */
export const test = base.extend<{ accessResources: Resources }>({
  accessResources: [async ({ playwright }, runTest) => {
    const api = await playwright.request.newContext({ timeout: 10_000 });
    const state: Resources = { headers: {}, name: `access-service-${Date.now()}-${Math.random().toString(36).slice(2)}` };
    try { await runTest(state); }
    finally {
      try {
        if (state.headers.Authorization) {
          await cleanupAccessResources(api, state);
        }
      } finally { await api.dispose(); }
    }
  }, { timeout: 30_000 }],
});

/** 注册双语访问组隔离流程；locale控制真实控制台语言，无新增管理页面。
 * 管理API创建/编辑/读回旧配置，普通用户页面模型列表及真实调试台对话不变、500预算不被覆盖；独立夹具清理数据。
 * 不把未接入的访问组UI或模型授权声明为已实现，精细权限和计费由后台回归验证。
 */
export function registerAccessGroupConfigService(locale: Locale) {
  test("access group configuration persistence and isolation " + locale, async ({ page, accessResources: resources }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = resources.headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const email = resources.name + "@example.invalid";
    const user = await page.request.post(GATEWAY + "/user/new", { headers, data: {
      user_email: email, password: "access-test-password", user_role: "user", max_budget: 500,
    } });
    expect(user.status(), await user.text()).toBe(200);
    resources.userID = (await user.json()).user_id;
    const created = await page.request.post(GATEWAY + "/access_group/new", { headers, data: {
      name: resources.name, models: ["not-a-granted-model"], access_model_names: ["not-a-granted-model"], budget_id: "no-budget",
    } });
    expect(created.status(), await created.text()).toBe(200);
    const group = await created.json();
    expect(group.access_group_id).toMatch(/^access_group_/);
    const path = GATEWAY + "/v1/access_group/" + encodeURIComponent(group.access_group_id);
    const updated = await page.request.put(path, { headers, data: { access_group_name: "Original 用户 input", models: null } });
    expect(updated.status()).toBe(200);
    const invalid = await page.request.put(path, { headers: { ...headers, "Content-Type": "application/json" }, data: '{"access_group_name":' });
    expect(invalid.status()).toBe(400);
    const read = await page.request.get(path, { headers });
    expect(read.status()).toBe(200);
    expect(await read.json()).toMatchObject({ access_group_name: "Original 用户 input", models: [], budget_id: "no-budget" });

    await login(page, email, "access-test-password", locale);
    await expect(page.getByTestId("create-key-button")).toBeVisible();
    const session = { Authorization: "Bearer " + await sessionBearer(page) };
    const forbidden = await page.request.put(path, { headers: session, data: { models: ["gpt-4o-mini"] } });
    expect(forbidden.status()).toBe(403);
    await stableGoto(page, "/mine-models");
    await expect(page.getByTestId("my-model-gpt-4o-mini")).toBeVisible();
    await expect(page.getByTestId("my-model-not-a-granted-model")).toHaveCount(0);
    await stableGoto(page, "/usage");
    await expect(page.getByText(translate(locale, "{value0} limit", { value0: "$500.0000" }), { exact: true })).toBeVisible();
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: /gpt-4o-mini/ }).click();
    await page.getByTestId("chat-composer-input").fill("Verify access group configuration preserves this conversation.");
    await page.getByTestId("chat-send-button").click();
    await expect(page.getByTestId("message-surface").getByText("e2e-ok", { exact: true })).toBeVisible();
    const deleted = await page.request.delete(path, { headers });
    expect(deleted.status()).toBe(200);
    expect((await page.request.get(path, { headers })).status()).toBe(404);
    await stableGoto(page, "/mine-models");
    await expect(page.getByTestId("my-model-gpt-4o-mini")).toBeVisible();
  });
}
