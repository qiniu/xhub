import { expect } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto, watchGateway } from "./helpers";
import { matchesGatewayResponse } from "./gateway-response.cjs";
import { test } from "./guard-resource-fixture";

/** 注册双语护栏配置闭环；locale 决定可访问名称，不伪造管理接口或数据面响应。
 * 前置隔离 PostgreSQL、真实管理员与本地 HTTP 上游；验证草稿调试、保存、重读、脱敏、编辑拦截和删除。
 * 独立资源夹具在成功、失败或超时后清理模型、护栏及监听，任何清理失败都会使测试失败。
 */
export function registerGuardManagementService(locale: Locale) {
  test("guard management service lifecycle " + locale, async ({ page, guardResources: resources }) => {
    const { fixture } = resources;
    const name = "guard-service-" + locale + "-" + Date.now();

    const tr = (key: string) => translate(locale, key);
    const watched = watchGateway(page);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = resources.headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const model = await page.request.post(GATEWAY + "/model/new", { headers, data: {
      model_name: name,
      litellm_params: { model: "fixture-guard", custom_llm_provider: "openai", api_base: fixture.url + "/v1", api_key: "fixture-probe-key", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
      model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
    } });
    expect(model.status(), await model.text()).toBe(200);
    resources.modelID = (await model.json()).model_info.id;
    await stableGoto(page, "/guardrails");
    await page.getByRole("tab", { name: tr("pages.guardrails.title"), exact: true }).click();
    await page.getByRole("button", { name: tr("pages.guardrails.create"), exact: true }).click();
    await page.getByRole("menuitem", { name: tr("关键词 / 正则护栏"), exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel(tr("护栏名称"), { exact: true }).fill(name);
    await dialog.getByLabel(tr("关键词（每行一个）"), { exact: true }).fill("secret-token");
    await dialog.getByRole("combobox", { name: tr("命中后的动作"), exact: true }).selectOption("redact");
    await dialog.getByLabel(tr("替换文本"), { exact: true }).fill("[hidden]");
    const input = "Keep Original 用户输入 and secret-token.";
    await dialog.getByLabel(tr("测试文本"), { exact: true }).fill(input);
    const trial = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/guardrails/apply_guardrail", "POST"));
    await dialog.getByRole("button", { name: tr("测试当前规则"), exact: true }).click();
    const trialResponse = await trial;
    expect(trialResponse.status()).toBe(200);
    expect((await trialResponse.json()).response_text).toBe("Keep Original 用户输入 and [hidden].");
    await expect(dialog.getByLabel(tr("测试文本"), { exact: true })).toHaveValue(input);
    const saved = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/guardrails", "POST"));
    await dialog.getByRole("button", { name: tr("保存护栏"), exact: true }).click();
    const response = await saved;
    expect(response.status(), await response.text()).toBe(200);
    const id = resources.id = (await response.json()).guardrail_id;
    expect(id).toBeTruthy();
    await expect(dialog).not.toBeVisible();
    const body = { model: name, guardrails: [name], messages: [{ role: "user", content: input }] };
    const redacted = await page.request.post(GATEWAY + "/v1/chat/completions", { headers, data: body });
    expect(redacted.status(), await redacted.text()).toBe(200);
    expect((await redacted.json()).choices[0].message.content).toBe("Rendered: Keep Original 用户输入 and [hidden].");
    expect(fixture.requests).toHaveLength(1);
    expect(fixture.requests[0].body.messages).toEqual([{ role: "user", content: "Keep Original 用户输入 and [hidden]." }]);
    // 刷新后从服务器重读并编辑，验证不是只改浏览器临时状态。
    await page.reload();
    await page.getByRole("tab", { name: tr("pages.guardrails.title"), exact: true }).click();
    await page.getByLabel(tr("搜索护栏"), { exact: true }).fill(name);
    await page.getByRole("button", { name: id, exact: true }).click();
    await expect(page.getByLabel(tr("关键词（每行一个）"), { exact: true })).toHaveValue("secret-token");
    await page.getByRole("combobox", { name: tr("命中后的动作"), exact: true }).selectOption("block");
    const updated = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/guardrails/" + id, "PATCH"));
    await page.getByRole("button", { name: tr("保存护栏"), exact: true }).click();
    expect((await updated).status()).toBe(200);
    await expect(page.getByLabel(tr("搜索护栏"), { exact: true })).toBeVisible();
    const blocked = await page.request.post(GATEWAY + "/v1/chat/completions", { headers, data: body });
    expect(blocked.status()).toBe(400);
    expect(await blocked.text()).toContain("guardrail_failed");
    expect(fixture.requests, "被拦截的请求不得拨上游").toHaveLength(1);
    const row = page.getByRole("row").filter({ hasText: name });
    await row.getByRole("button", { name: tr("Open guardrail actions"), exact: true }).click();
    await page.getByRole("menuitem", { name: tr("Delete"), exact: true }).click();
    const deleted = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), "/guardrails/" + id, "DELETE"));
    await page.getByRole("dialog").getByRole("button", { name: tr("common.delete"), exact: true }).click();
    expect((await deleted).status()).toBe(200);
    await expect(row).toHaveCount(0);
    expect((await page.request.get(GATEWAY + "/guardrails/" + id, { headers })).status()).toBe(404);
    resources.id = "";
    const restored = await page.request.post(GATEWAY + "/v1/chat/completions", { headers, data: { model: name, messages: body.messages } });
    expect(restored.status(), await restored.text()).toBe(200);
    expect((await restored.json()).choices[0].message.content).toBe("Rendered: " + input);
    expect(fixture.requests).toHaveLength(2);
    await watched.assertOk();
  });
}
