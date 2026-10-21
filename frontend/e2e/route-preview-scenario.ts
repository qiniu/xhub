import { translate, type Locale } from "../src/i18n/translate";
import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 独立验证页面诊断预览只读；前置专属数据库、本地供应商及自建模板/密钥。
 * 三次预览不得调用上游、增加费用或日志；随后真实请求可用且产生账单，finally 删除自建资源。
 */
export function registerPreviewScenario(locale: Locale) {
  /** 翻译固定标签；参数为词条键，返回当前场景语言文本，不修改用户数据。 */
  const t = (key: string) => translate(locale, key);
  test(`页面路由预览只读及保存文案 ${locale}`, async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    await page.context().addCookies([{ name: "xhub_locale", value: locale, url: "http://127.0.0.1:3100" }]);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const suffix = "readonly-preview-" + Date.now();
    let templateID = "",
      keyID = "",
      secret = "";
    /** 读取只读快照；path 为真实接口，返回 JSON；非成功响应不能用于比较，无数据副作用。 */
    const read = async (path: string) => {
      const response = await page.request.get(path, { headers });
      expect(response.status(), await response.text()).toBe(200);
      return response.json();
    };
    /** 为本场景创建真实对象；path/data 为契约，返回 JSON；失败即停止，finally 清理已创建部分。 */
    const create = async (path: string, data: Record<string, unknown>) => {
      const response = await page.request.post(GATEWAY + path, { headers, data });
      expect(response.status(), await response.text()).toBe(200);
      return response.json();
    };
    try {
      await create("/model/new", {
        model_name: suffix,
        litellm_params: {
          model: "custom/readonly-preview",
          api_base: UPSTREAM,
          api_key: "sk-fake",
          deployment_id: suffix,
          custom_llm_provider: "custom",
          input_cost_per_token: 0.000001,
          output_cost_per_token: 0.000002,
        },
        model_info: { id: suffix, transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
      });
      templateID = (
        await create("/route_template/new", {
          name: suffix,
          body: {
            model_routes: [],
            routing_groups: [],
            fallbacks: [],
            context_window_fallbacks: [],
            content_policy_fallbacks: [],
            retry_policy: { max_attempts: 1, timeout_seconds: 60, failure_threshold: 3, cooldown_seconds: 60 },
          },
        })
      ).id;
      const key = await create("/key/generate", { key_alias: suffix, rpm_limit: 1, route_template_id: templateID });
      keyID = key.token_id;
      secret = key.key;
      await stableGoto(page, "/route-templates");
      const row = page.getByRole("row").filter({ has: page.getByText(suffix, { exact: true }) });
      await row.getByRole("button", { name: t("pages.routeTemplates.edit"), exact: true }).click();
      await expect(page.getByRole("button", { name: t("pages.routeTemplates.save"), exact: true })).toBeVisible();
      await page.getByRole("spinbutton", { name: t("pages.routeTemplates.timeout"), exact: true }).fill("75");
      const preview = page.getByRole("region", { name: t("pages.routeTemplates.preview.region"), exact: true });
      await preview.getByLabel(t("pages.routeTemplates.preview.model"), { exact: true }).selectOption(suffix);
      await preview.getByLabel(t("pages.routeTemplates.preview.endpoint"), { exact: true }).selectOption("chat");
      const beforeUpstream = await read(UPSTREAM + "/e2e/request-count");
      const beforeKey = (await read(GATEWAY + "/key/info?key=" + keyID)).info;
      const logsPath =
        GATEWAY +
        "/spend/logs/ui?" +
        new URLSearchParams({ model: suffix, page: "1", page_size: "100", group_by_session: "false" });
      const beforeLogs = await read(logsPath);
      for (let index = 0; index < 3; index++) {
        const waiting = page.waitForResponse(
          (r) => new URL(r.url()).pathname === "/route_template/preview" && r.request().method() === "POST",
        );
        await preview.getByRole("button", { name: t("pages.routeTemplates.preview.run"), exact: true }).click();
        const response = await waiting;
        expect(response.status(), await response.text()).toBe(200);
        expect(response.request().postDataJSON().body.retry_policy.timeout_seconds).toBe(75);
        expect((await response.json()).data).toEqual(
          expect.arrayContaining([expect.objectContaining({ deployment_id: suffix })]),
        );
        await expect(preview).toContainText(suffix);
        await expect(
          preview.getByRole("button", { name: t("pages.routeTemplates.preview.run"), exact: true }),
        ).toBeEnabled();
      }
      expect(await read(UPSTREAM + "/e2e/request-count"), "预览不得转发供应商").toEqual(beforeUpstream);
      expect((await read(GATEWAY + "/key/info?key=" + keyID)).info, "预览不得修改密钥配额或花费").toEqual(beforeKey);
      expect(await read(logsPath), "预览不得制造推理日志").toEqual(beforeLogs);
      await page.getByRole("tab", { name: "JSON", exact: true }).click();
      const json = page.getByRole("textbox", { name: "JSON", exact: true });
      const validJSON = await json.inputValue();
      await json.fill("{invalid");
      await expect(
        preview.getByRole("button", { name: t("pages.routeTemplates.preview.run"), exact: true }),
      ).toBeDisabled();
      await expect(page.getByRole("button", { name: t("pages.routeTemplates.save"), exact: true })).toBeDisabled();
      await json.fill(validJSON);
      await page.getByRole("button", { name: t("pages.routeTemplates.save"), exact: true }).click();
      await expect(
        page.getByRole("region", { name: t("pages.routeTemplates.preview.region"), exact: true }),
      ).not.toBeVisible();
      await row.getByRole("button", { name: t("pages.routeTemplates.edit"), exact: true }).click();
      await expect(page.getByRole("spinbutton", { name: t("pages.routeTemplates.timeout"), exact: true })).toHaveValue(
        "75",
      );
      const response = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + secret },
        data: { model: suffix, messages: [{ role: "user", content: "Verify read-only preview " + suffix }] },
      });
      expect(response.status(), await response.text()).toBe(200);
      expect((await response.json()).choices[0].message.content).toBe("e2e-ok");
      const callID = response.headers()["x-litellm-call-id"];
      expect(callID).toBeTruthy();
      await expect
        .poll(async () => {
          const log = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
          return log.status() === 200 ? Number((await log.json()).spend) : 0;
        })
        .toBeCloseTo(0.000012, 10);
      // 模拟另一管理员删除同一模板，真实后台返回失败，页面应保留草稿并明确显示错误。
      expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status()).toBe(
        200,
      );
      keyID = "";
      expect(
        (
          await page.request.post(GATEWAY + "/route_template/" + templateID + "/delete", { headers, data: {} })
        ).status(),
      ).toBe(200);
      templateID = "";
      await preview.getByLabel(t("pages.routeTemplates.preview.model"), { exact: true }).selectOption(suffix);
      await preview.getByRole("button", { name: t("pages.routeTemplates.preview.run"), exact: true }).click();
      await expect(preview.getByRole("alert")).toHaveText(t("pages.routeTemplates.preview.requestError"));
      await page.getByRole("button", { name: t("pages.routeTemplates.save"), exact: true }).click();
      await expect(page.getByText(t("pages.routeTemplates.saveError"), { exact: true })).toBeVisible();
      await expect(page.getByRole("textbox", { name: t("pages.routeTemplates.name"), exact: true })).toHaveValue(
        suffix,
      );
    } finally {
      if (keyID)
        expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status()).toBe(
          200,
        );
      if (templateID)
        expect(
          (
            await page.request.post(GATEWAY + "/route_template/" + templateID + "/delete", { headers, data: {} })
          ).status(),
        ).toBe(200);
      const deleted = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: suffix } });
      expect([200, 404]).toContain(deleted.status());
    }
  });
}
