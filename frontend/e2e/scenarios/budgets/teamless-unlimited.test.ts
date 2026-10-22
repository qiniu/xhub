import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, login, loginAdmin, sessionBearer, stableGoto, t } from "../../helpers";
import { translate } from "../../../src/i18n/translate";

/** 无团队空预算完整流程：独立管理员经真实页面切换中英、流式调用、编辑上限及清空恢复。
 * 参数为 Playwright 页面，返回异步完成；本地上游提供响应，验证个人记账及拒绝无转发。
 * finally 删除密钥、账号和定价明确的测试模型，运行器回收私有 schema；不使用外部服务凭据。 */
test("teamless blank budgets allow streaming and personal caps can be cleared", async ({ page }) => {
  test.setTimeout(150_000);
  await loginAdmin(page);
  const adminHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
  const email = "teamless-unlimited-" + Date.now() + "@xhub.local";
  const password = "teamless-unlimited-password";
  const created = await page.request.post(GATEWAY + "/user/new", { headers: adminHeaders, data: { user_email: email, password, user_role: "proxy_admin" } });
  expect(created.status(), await created.text()).toBe(200);
  const uid = (await created.json()).user_id;
  const model = "teamless-priced-" + uid;
  let modelCreated = false;
  let keyID = "";
  try {
    // 显式定价排除目录价格变化，让流式消费与个人预算断言具有确定性。
    const deployment = await page.request.post(GATEWAY + "/model/new", { headers: adminHeaders, data: {
      model_name: model,
      litellm_params: { model, api_base: UPSTREAM, api_key: "sk-fake", deployment_id: model, custom_llm_provider: "custom", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
      model_info: { id: model, transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
    } });
    expect(deployment.status(), await deployment.text()).toBe(200);
    modelCreated = true;
    await login(page, email, password);
    await expect(page.getByTestId("create-key-button")).toBeVisible();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const infoURL = GATEWAY + "/user/info?user_id=" + uid;
    const initial = await (await page.request.get(infoURL, { headers })).json();
    expect(initial.user_info.max_budget).toBeNull();
    expect(initial.user_info.teams ?? []).toEqual([]);
    const minted = await page.request.post(GATEWAY + "/key/generate", { headers, data: { key_alias: "teamless-unlimited" } });
    expect(minted.status(), await minted.text()).toBe(200);
    const key = await minted.json();
    keyID = key.token_id;
    await stableGoto(page, "/users?user=" + uid);
    await expect(page.getByText(t("pages.users.unlimited"), { exact: true }).filter({ visible: true })).toBeVisible();
    for (const locale of ["en", "zh-CN"] as const) {
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      await expect(page.getByText(translate(locale, "pages.users.unlimited"), { exact: true }).filter({ visible: true })).toBeVisible();
      await page.getByRole("button", { name: translate(locale, "pages.users.editUser"), exact: true }).click();
      await expect(page.getByLabel(translate(locale, "pages.users.maxBudget"))).toHaveAccessibleDescription(translate(locale, "quotaGuide.personalBillingHelp"));
      await page.getByRole("button", { name: translate(locale, "common.cancel"), exact: true }).click();
      await page.getByRole("tab", { name: translate(locale, "common.overview"), exact: true }).click();
    }
    // 浏览器保存预算后，既有会话和密钥立即遵循同一个个人累计上限。
    /** 使用指定会话或密钥提交不同正文，返回真实响应；由当前场景验证预算及清理数据。 */
    const invoke = (credential: string, content: string) => page.request.post(GATEWAY + "/chat/completions", { headers: { Authorization: "Bearer " + credential }, data: { model, messages: [{ role: "user", content }] } });
    await editBudget(page, uid, "0");
    const before = await (await page.request.get(UPSTREAM + "/e2e/request-count")).json();
    const spend = (await (await page.request.get(infoURL, { headers })).json()).user_info.spend;
    expect(Number(spend)).toBe(0);
    for (const credential of [headers.Authorization.slice(7), key.key]) {
      const blocked = await invoke(credential, "zero budget must block");
      expect(blocked.status(), await blocked.text()).toBe(403);
      expect(await blocked.text()).toContain("User budget");
    }
    expect(await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).toEqual(before);
    expect((await (await page.request.get(infoURL, { headers })).json()).user_info.spend).toBe(spend);
    await editBudget(page, uid, "");
    // 保留用户原始中文；英文示例规则不能翻译实际用户输入。
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(t("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: model, exact: true }).click();
    await page.getByPlaceholder(t("Type your message... (Shift+Enter for new line)")).filter({ visible: true }).fill("写一首诗");
    const called = page.waitForResponse(response => /^(\/v1)?\/chat\/completions$/.test(new URL(response.url()).pathname) && response.request().method() === "POST");
    await page.getByRole("button", { name: t("Send message") }).click();
    const response = await called;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON()).toMatchObject({ stream: true, messages: [{ role: "user", content: "写一首诗" }] });
    await expect(page.getByText("e2e-ok", { exact: true })).toBeVisible();
    const callID = response.headers()["x-litellm-call-id"];
    await expect.poll(async () => {
      const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
      return detail.ok() ? Number((await detail.json()).spend) : 0;
    }, { message: "流式请求应按8输入2输出记录个人费用" }).toBeCloseTo(0.000012, 10);
    await expect.poll(async () => Number((await (await page.request.get(infoURL, { headers })).json()).user_info.spend)).toBeGreaterThan(0);

    await editBudget(page, uid, "10");
    expect((await invoke(key.key, "finite personal budget")).status()).toBe(200);
    await editBudget(page, uid, "");
    expect((await (await page.request.get(infoURL, { headers })).json()).user_info.max_budget).toBeNull();
    expect((await invoke(headers.Authorization.slice(7), "cleared personal budget")).status()).toBe(200);
  } finally {
    if (modelCreated) expect((await page.request.post(GATEWAY + "/model/delete", { headers: adminHeaders, data: { id: model } })).status()).toBe(200);
    if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", { headers: adminHeaders, data: { keys: [keyID] } })).status()).toBe(200);
    expect((await page.request.post(GATEWAY + "/user/delete", { headers: adminHeaders, data: { user_id: uid } })).status()).toBe(200);
    expect((await page.request.get(GATEWAY + "/user/info?user_id=" + uid, { headers: adminHeaders })).status()).toBe(404);
  }
});

/** 通过用户详情编辑个人预算；page 为已登录管理员，uid 为测试账户，value 空字符串表示不限额。
 * 返回保存完成状态；验证真实更新响应及持久化字段，失败保留页面证据；清理由调用方负责。 */
async function editBudget(page: Page, uid: string, value: string) {
  await stableGoto(page, "/users?user=" + uid);
  await page.getByRole("button", { name: t("pages.users.editUser"), exact: true }).click();
  await page.getByLabel(t("pages.users.maxBudget")).fill(value);
  const saved = page.waitForResponse(response => response.url().includes("/user/update") && response.request().method() === "POST");
  await page.getByRole("button", { name: t("pages.users.saveChanges"), exact: true }).click();
  const response = await saved;
  expect(response.status(), await response.text()).toBe(200);
  expect(response.request().postDataJSON().max_budget).toBe(value === "" ? null : Number(value));
  await expect(page.getByRole("button", { name: t("pages.users.editUser"), exact: true })).toBeVisible();
}
