import path from "path";
import { translate } from "../src/i18n/translate";

import { expect, test } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";
/** 用途：注册独立场景「model editor presents endpoints clearly and saves a path model with a safe alias」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC073() {


test("model editor presents endpoints clearly and saves a path model with a safe alias", async ({ page }) => {
  test.setTimeout(120_000);
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + await sessionBearer(page) };
  const credential = "e2e-alias-presentation";
  const upstreamModel = "group/model:latest";
  const name = "e2e-group/model:latest：版本";
  let id = "";
  expect((await page.request.post(GATEWAY + "/credentials", { headers, data: {
    credential_name: credential, credential_info: { custom_llm_provider: "openai" },
    credential_values: { api_base: UPSTREAM, api_key: "sk-fake" },
  } })).status()).toBe(200);
  try {
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("button", { name: t("pages.models.add") }).click();
    const form = page.locator("form").filter({ has: page.getByLabel("模型提供商 *") });
    await form.getByLabel("模型提供商 *").selectOption(credential);
    const upstream = form.getByRole("combobox", { name: "上游模型 *" });
    await upstream.fill(upstreamModel);
    await upstream.press("Escape");
    const alias = form.getByLabel("对外模型名称 *");
    await expect(alias).toHaveValue(upstreamModel);
    await form.getByRole("combobox", { name: "上游接口协议", exact: true }).click();
    await page.getByRole("option", { name: "OpenAI · Responses", exact: true }).click();
    const published = form.getByRole("region", { name: "XHub 对外接口" });
    const toggle = published.getByRole("button", { name: "XHub 对外接口", exact: true });
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(published.getByRole("list")).toBeHidden();
    await expect(form.getByText(/上游模型需填写 FAL 任务路径/)).toHaveCount(0);
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(published.getByRole("listitem")).toHaveCount(6);
    const gemini = published.getByRole("listitem", { name: "Gemini · Generate Content" });
    await expect(gemini.getByText("/v1beta/models/{model}:generateContent", { exact: true })).toBeVisible();
    await expect(gemini.getByText("/v1beta/models/{model}:streamGenerateContent", { exact: true })).toBeVisible();
    await expect(published.getByRole("checkbox")).toHaveCount(0);
    await expect(published.getByRole("listitem", { name: "Bypass · OpenAI Responses" })).toBeVisible();
    // 可见卡片和路径均不能超出容器；以实际浏览器几何验证长 Google 路径的折行。
    await page.setViewportSize({ width: 900, height: 1000 });
    expect(await published.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    await page.screenshot({ path: path.resolve(__dirname, "../../.e2e/model-discovery-fix/editor-narrow.png"), fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1100 });
    await published.screenshot({ path: path.resolve(__dirname, "../../.e2e/model-discovery-fix/endpoints.png") });
    await toggle.click();
    await expect(published.getByRole("listitem", { name: "Bypass · OpenAI Responses" })).toBeHidden();
    await toggle.click();
    await expect(published.getByRole("listitem", { name: "Bypass · OpenAI Responses" })).toBeVisible();
    await toggle.click();
    await published.screenshot({ path: path.resolve(__dirname, "../../.e2e/model-discovery-fix/endpoints-collapsed.png") });
    await form.getByLabel("价格来源").selectOption("manual");
    await form.locator("#editor-input_cost_per_token").fill("1");
    await form.locator("#editor-output_cost_per_token").fill("2");
    await alias.fill("group/../model");
    await form.getByRole("button", { name: "添加模型", exact: true }).click();
    await expect(form.getByRole("alert")).toContainText("对外模型名称的路径段不能留空");
    await expect(upstream).toHaveValue(upstreamModel);
    await alias.fill(name);
    const pending = page.waitForResponse(response => new URL(response.url()).pathname === "/model/new" && response.request().method() === "POST");
    await form.getByRole("button", { name: "添加模型", exact: true }).click();
    const saved = await pending;
    expect(saved.status(), await saved.text()).toBe(200);
    expect(saved.request().postDataJSON().litellm_params.model).toBe(upstreamModel);
    expect(saved.request().postDataJSON().model_info.endpoint_types).toContain("bypass:openai-responses");
    id = (await saved.json()).model_info.id;
    await page.reload();
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: t("pages.models.all") }).click();
    await page.getByLabel("搜索模型").fill(name);
    await page.getByRole("region", { name: "公开模型 " + name, exact: true }).getByRole("button", { name: "详情", exact: true }).click();
    await page.getByRole("button", { name: "编辑模型", exact: true }).click();
    await expect(alias).toHaveValue(name);
    await expect(upstream).toHaveValue(upstreamModel);
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await toggle.click();
    await expect(published.getByRole("listitem", { name: "Bypass · OpenAI Responses" })).toBeVisible();
    for (const target of [
      { path: "/v1/chat/completions", body: { model: name, messages: [{ role: "user", content: "hello" }] } },
      { path: "/v1beta/models/" + encodeURIComponent(name) + ":generateContent", body: { contents: [{ role: "user", parts: [{ text: "hello" }] }] } },
      { path: "/bypass/openai/v1/responses", body: { model: name, input: "hello" } },
    ]) {
      const response = await page.request.post(GATEWAY + target.path, { headers, data: target.body });
      expect(response.status(), await response.text()).toBe(200);
      expect(await response.text()).toContain("e2e-ok");
    }
    // 旧部署不补写 Bypass 声明，英文页面仍必须展示自动可用入口，并用页面所示路径完成真实调用。
    const patched = await page.request.patch(GATEWAY + "/model/" + id + "/update", { headers, data: { model_info: { endpoint_types: ["chat"] } } });
    expect(patched.status(), await patched.text()).toBe(200);
    await page.reload();
    await page.getByRole("button", { name: "English", exact: true }).click();
    await page.getByRole("button", { name: translate("en", "providerGuideUI.editModel"), exact: true }).click();
    const english = page.getByRole("region", { name: "XHub public endpoints" });
    await english.getByRole("button", { name: "XHub public endpoints", exact: true }).click();
    await expect(english).toContainText(translate("en", "modelEndpoints.automaticDescription"));
    const native = english.getByRole("listitem", { name: "Bypass · OpenAI Responses" });
    await expect(native.getByText("Enabled", { exact: true })).toBeVisible();
    await expect(english.getByRole("checkbox")).toHaveCount(0);
    await expect(page.getByLabel(translate("en", "providerGuideUI.publicName"), { exact: true })).toHaveValue(name);
    const nativePath = await native.locator("code").innerText();
    const result = await page.request.post(GATEWAY + nativePath, { headers, data: { model: name, input: "Reply with a greeting." } });
    expect(result.status(), await result.text()).toBe(200);
    expect(await result.text()).toContain("e2e-ok");

  } finally {
    if (id) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
    expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
  }
});
}
