import { expect, test } from "@playwright/test";
import { GATEWAY, login, loginAdmin, sessionBearer, stableGoto } from "../../helpers";
import { waitForLog } from "../../product-fixes-scenario";
import { translate } from "../../../src/i18n/translate";

// 真实浏览器、隔离网关、PostgreSQL 和本地供应商验证个人记录开关与完整请求详情；结束关闭开关并由运行器回收独立库。
for (const locale of ["zh-CN", "en"] as const) {
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

// 普通用户实际登录后开启个人记录并通过个人会话发起对话，核对页面和后台结果；finally 删除测试账号。
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
