import { expect, test } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, login, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册日志/用量双语契约闭环；真实用户、浏览器、网关、隔离PG和本地上游。成功及失败均实际调用，finally精确清理用户/模型。 */
export function registerCoreContract(locale: "en" | "zh-CN") {
  test("core log and usage contract " + locale, async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const name = "contract-" + randomUUID(),
      email = name + "@example.test";
    const tr = (key: string) => translate(locale, key);
    let userId = "",
      modelId = "";
    /** 创建独立测试对象并严格检查真实响应；失败抛出，返回对象的ID由finally清理。 */
    const create = async (path: string, data: unknown) => {
      const r = await page.request.post(GATEWAY + path, { headers, data });
      expect(r.status(), await r.text()).toBe(200);
      return r.json();
    };
    try {
      userId = (await create("/user/new", { user_email: email, password: "Contract-password-123", max_budget: 10 }))
        .user_id;
      modelId = (
        await create("/model/new", {
          model_name: name,
          litellm_params: {
            model: "gpt-4o-mini",
            custom_llm_provider: "openai",
            api_base: UPSTREAM + "/v1",
            api_key: "sk-fake",
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
          model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
        })
      ).model_info.id;
      await login(page, email, "Contract-password-123", "zh-CN");
      await expect(page.getByText(translate("zh-CN", "nav.apiKeys"), { exact: true }).first()).toBeVisible();
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      await stableGoto(page, "/playground");
      await expect(page.getByTestId("chat-composer-input")).toBeVisible();
      await page.evaluate(() => sessionStorage.setItem("streamingEnabled", "false"));
      await page.reload();
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name, exact: true }).click();
      const prompt = "Verify the log contract with a short greeting.";
      await page.getByTestId("chat-composer-input").fill(prompt);
      const pending = page.waitForResponse(
        (r) => /\/(chat\/completions|responses)$/.test(new URL(r.url()).pathname) && r.request().method() === "POST",
      );
      await page.getByTestId("chat-send-button").click();
      const response = await pending;
      expect(response.status()).toBe(200);
      await expect(page.getByText("e2e-ok", { exact: true }).last()).toBeVisible();
      const id = response.headers()["x-litellm-call-id"];
      expect(id).toBeTruthy();
      const member = { Authorization: "Bearer " + (await sessionBearer(page)) };
      await expect
        .poll(async () => {
          const r = await page.request.get(GATEWAY + "/spend/logs/ui/" + id, { headers: member });
          return r.status();
        })
        .toBe(200);
      const denied = await page.request.get(GATEWAY + "/v2/model/info", { headers: member });
      expect(denied.status()).toBe(403);
      await stableGoto(page, "/logs");
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).fill(id);
      const row = page.getByRole("row").filter({ hasText: id });
      await expect(row).toBeVisible();
      const detailPending = page.waitForResponse((r) => new URL(r.url()).pathname === "/spend/logs/ui/" + id);
      await row.click();
      const detail = await detailPending;
      expect(detail.status()).toBe(200);
      const stored = await detail.json();
      expect(stored).toMatchObject({ request_id: id, model: name, user: userId, status: "success" });
      expect(stored.spend).toBeGreaterThan(0);
      await expect(page.getByRole("dialog")).toBeVisible();
      await stableGoto(page, "/usage");
      await expect(page.getByTestId("daily-spend-settled")).toBeVisible();
      const activity = await page.request.get(GATEWAY + "/user/daily/activity/aggregated", { headers: member });
      expect(activity.status()).toBe(200);
      expect((await activity.json()).metadata).toMatchObject({
        total_api_requests: 1,
        total_successful_requests: 1,
        total_failed_requests: 0,
      });
      // 真实无权限失败不得清掉仍有效登录会话，也不得改变个人预算。
      expect((await page.request.get(GATEWAY + "/auth/me", { headers: member })).status()).toBe(200);
      const user = await page.request.get(GATEWAY + "/user/info?user_id=" + userId, { headers });
      expect((await user.json()).user_info.max_budget).toBe(10);
    } finally {
      test.setTimeout(test.info().timeout + 30000);
      if (userId)
        expect(
          (await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: userId } })).status(),
          "删除测试用户",
        ).toBe(200);
      if (modelId)
        expect(
          (await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelId } })).status(),
          "删除测试模型",
        ).toBe(200);
    }
  });
}
