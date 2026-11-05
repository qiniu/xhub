import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, login, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { waitForLog } from "./product-fixes-scenario";
import { translate } from "../src/i18n/translate";

/** 按 locale 注册个人记录场景，无返回值；真实浏览器连接隔离网关、PostgreSQL 和本地上游，finally 关闭开关，由运行器回收独立库。 */
export function registerPersonalLogCapture(locale: "zh-CN" | "en") {
  test(`personal request body recording ${locale}`, async ({ page }) => {
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const headers = { Authorization: `Bearer ${await sessionBearer(page)}` };
    await stableGoto(page, "/logs");
    const toggle = page.getByRole("switch", { name: translate(locale, "logsUX.captureTitle") });
    await expect(toggle).toBeVisible();
    try {
      if (await toggle.isChecked()) await toggle.click();
      const saving = page.waitForResponse(r => r.url().includes("/account/log-capture") && r.request().method() === "PUT");
      await toggle.click();
      expect((await saving).status()).toBe(200);
      await expect(toggle).toBeChecked();
      const call = await page.request.post(`${GATEWAY}/v1/chat/completions`, { headers, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Synthetic capture browser request." }] } });
      expect(call.status(), await call.text()).toBe(200);
      const id = call.headers()["x-litellm-call-id"];
      const detail = await waitForLog(page.request, headers, id);
      expect(detail.body_capture_status).toBe("saved");
      expect(JSON.stringify(detail.messages)).toContain("Synthetic capture browser request.");
      expect(detail.response).toBeTruthy();
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(id);
      await page.getByRole("row").filter({ hasText: id }).click();
      await expect(page.getByText("Synthetic capture browser request.").first()).toBeVisible();
      await page.keyboard.press("Escape");
      await verifyCapturedFailures(page, headers, locale);
      await stableGoto(page, "/logs");
      await toggle.click();
      await expect(toggle).not.toBeChecked();
      const offCall = await page.request.post(`${GATEWAY}/v1/chat/completions`, { headers, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Do not retain this synthetic request." }] } });
      expect(offCall.status(), await offCall.text()).toBe(200);
      const offDetail = await waitForLog(page.request, headers, offCall.headers()["x-litellm-call-id"]);
      expect(offDetail.body_capture_status).toBe("disabled");
      expect(JSON.stringify(offDetail.messages)).not.toContain("Do not retain this synthetic request.");
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(offCall.headers()["x-litellm-call-id"]);
      await page.getByRole("row").filter({ hasText: offCall.headers()["x-litellm-call-id"] }).click();
      await expect(page.getByRole("note").filter({ hasText: translate(locale, "logsUX.captureDisabled") })).toBeVisible();
    } finally {
      await page.request.put(`${GATEWAY}/account/log-capture`, { headers, data: { enabled: false } });
    }
  });
}

/** 验证已开启记录后的参数拒绝和异步失败；参数是已登录页面、会话头及语言，返回流程完成的 Promise。
 * 真实浏览器读取网关和私有 PostgreSQL 的结果；独立模型在 finally 删除，任务和日志由运行器清理。 */
async function verifyCapturedFailures(page: Page, headers: Record<string, string>, locale: "zh-CN" | "en") {
  const rejected = await page.request.post(`${GATEWAY}/v1/chat/completions`, { headers, data: {
    model: "missing-capture-model", messages: [{ role: "user", content: "Synthetic rejected capture request." }],
  } });
  expect(rejected.status(), await rejected.text()).toBe(400);
  const rejectedID = rejected.headers()["x-litellm-call-id"];
  const rejectedDetail = await waitForLog(page.request, headers, rejectedID);
  expect(rejectedDetail.body_capture_status).toBe("saved");
  expect(JSON.stringify(rejectedDetail.messages)).toContain("Synthetic rejected capture request.");
  await stableGoto(page, "/logs/?log_id=" + rejectedID);
  await expect(page.getByRole("dialog").getByText("Synthetic rejected capture request.").first()).toBeVisible();
  const errorRegion = page.getByRole("region", { name: translate(locale, "Full Error Details") });
  await errorRegion.getByText(translate(locale, "logsUX.rawError"), { exact: true }).click();
  await expect(errorRegion).toContainText("model not found");
  await page.keyboard.press("Escape");

  const name = "capture-failed-task-" + Date.now();
  const model = await page.request.post(`${GATEWAY}/model/new`, { headers, data: {
    model_name: name,
    litellm_params: { model: "volcengine/doubao-seedance-2-0-260128", custom_llm_provider: "volcengine", api_base: UPSTREAM, api_key: "sk-fake", output_cost_per_token: 0.00001 },
    model_info: { transport: "ark_contents_generation", endpoint_types: ["bypass:ark-video"], pricing_source: "manual" },
  } });
  expect(model.status(), await model.text()).toBe(200);
  const deploymentID = (await model.json()).model_info.id;
  try {
    const created = await page.request.post(`${GATEWAY}/api/v3/contents/generations/tasks`, { headers, data: {
      model: name, content: [{ type: "text", text: "e2e-task-failure" }],
    } });
    expect(created.status(), await created.text()).toBe(200);
    const task = (await created.json()).id;
    for (const status of ["running", "failed"]) {
      const poll = await page.request.get(`${GATEWAY}/api/v3/contents/generations/tasks/${task}`, { headers });
      expect(poll.status(), await poll.text()).toBe(200);
      expect((await poll.json()).status).toBe(status);
    }
    const id = created.headers()["x-litellm-call-id"];
    const detail = await waitForLog(page.request, headers, id);
    expect(detail.status).toBe("failed");
    expect(detail.body_capture_status).toBe("saved");
    expect(detail.response).toEqual({ id: task });
    expect(detail.error).toContain("failed");
    expect(Number(detail.spend)).toBe(0);
    await stableGoto(page, "/logs/?log_id=" + id);
    await expect(errorRegion).toBeVisible();
    await errorRegion.getByText(translate(locale, "logsUX.rawError"), { exact: true }).click();
    await expect(errorRegion).toContainText(detail.error);
    await expect(errorRegion).not.toContainText(task);
    await page.keyboard.press("Escape");
  } finally {
    const deleted = await page.request.post(`${GATEWAY}/model/delete`, { headers, data: { id: deploymentID } });
    expect(deleted.status(), await deleted.text()).toBe(200);
  }
}

/** 注册普通用户记录场景，无参数或返回值；真实会话发起同步和流式对话，核对页面与后台，finally 删除测试账号。 */
export function registerPersonalLogCaptureOrdinary() {
  test("personal body recording for an ordinary user", async ({ page }) => {
  await loginAdmin(page);
  const adminHeaders = { Authorization: `Bearer ${await sessionBearer(page)}` };
  const email = `capture-browser-${Date.now()}@example.test`;
  const password = "capture-browser-password";
  const created = await page.request.post(`${GATEWAY}/user/new`, { headers: adminHeaders, data: { user_email: email, user_role: "internal_user", password } });
  expect(created.status(), await created.text()).toBe(200);
  const userID = (await created.json()).user_id;
  try {
    await login(page, email, password);
    await expect(page.getByText(translate("zh-CN", "nav.apiKeys")).first()).toBeVisible();
    const headers = { Authorization: `Bearer ${await sessionBearer(page)}` };
    await stableGoto(page, "/logs");
    const toggle = page.getByRole("switch", { name: translate("zh-CN", "logsUX.captureTitle") });
    await expect(toggle).not.toBeChecked();
    await toggle.click();
    await expect(toggle).toBeChecked();
    const response = await page.request.post(`${GATEWAY}/v1/chat/completions`, { headers, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Ordinary user capture request." }] } });
    expect(response.status(), await response.text()).toBe(200);
    const detail = await waitForLog(page.request, headers, response.headers()["x-litellm-call-id"]);
    expect(detail.body_capture_status).toBe("saved");
    expect(JSON.stringify(detail.messages)).toContain("Ordinary user capture request.");
    await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(response.headers()["x-litellm-call-id"]);
    await page.getByRole("row").filter({ hasText: response.headers()["x-litellm-call-id"] }).click();
    await expect(page.getByText("Ordinary user capture request.").first()).toBeVisible();
    await page.keyboard.press("Escape");
    // 流式对话也必须实际保存拼接后的响应，不能只验证非流式请求。
    const streamed = await page.request.post(`${GATEWAY}/v1/chat/completions`, { headers, data: { model: "gpt-4o-mini", stream: true, messages: [{ role: "user", content: "Ordinary user streaming capture request." }] } });
    expect(streamed.status(), await streamed.text()).toBe(200);
    expect(await streamed.text()).toContain("data:");
    const streamDetail = await waitForLog(page.request, headers, streamed.headers()["x-litellm-call-id"]);
    expect(streamDetail.body_capture_status).toBe("saved");
    expect(JSON.stringify(streamDetail.messages)).toContain("Ordinary user streaming capture request.");
    expect(JSON.stringify(streamDetail.response)).toContain("e2e-ok");
    await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(streamed.headers()["x-litellm-call-id"]);
    await page.getByRole("row").filter({ hasText: streamed.headers()["x-litellm-call-id"] }).click();
    await expect(page.getByText("Ordinary user streaming capture request.").first()).toBeVisible();
    await page.getByRole("tab", { name: "JSON", exact: true }).click();
    await expect(page.getByText(/e2e-ok/).first()).toBeVisible();
  } finally {
    const removed = await page.request.post(`${GATEWAY}/user/delete`, { headers: adminHeaders, data: { user_id: userID } });
    expect(removed.status(), await removed.text()).toBe(200);
  }
});

}
