import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册中英文软预算闭环：页面创建微小额度Key，真实调用允许超支后拒绝下一次，页面调额恢复。
 * 前置隔离PG、生产前端和本地供应商；验证实测扣费/无预占/文案及零额度，finally删除自建Key和模型。 */
export function registerSoftBudgetScenario(locale: "en" | "zh-CN") {
  test("soft budget completes requests and stops after exhaustion " + locale, async ({ page }) => {
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    await page.getByRole("button", {name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true}).click();
    const tr = (key: string) => translate(locale, key);
    const model = "soft-budget-" + locale.toLowerCase() + "-" + Date.now();
    let keyID = "", modelID = "";
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {headers, data: {
        model_name: model, litellm_params: {model, custom_llm_provider: "openai", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.00001, output_cost_per_token: 0.00002},
        model_info: {transport: "bypass_openai_chat", pricing_source: "manual", endpoint_types: ["chat"]},
      }});
      expect(created.status(), await created.text()).toBe(200);
      modelID = (await created.json()).model_info.id;
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name")).fill("软预算-test-" + locale);
      await page.getByLabel(tr("Max Budget (USD)"), {exact: true}).fill("0.000001");
      await expect(page.getByText(tr("hardBudget.contract"))).toBeVisible();
      const minted = page.waitForResponse(r => r.url().includes("/key/generate") && r.request().method() === "POST");
      await page.getByRole("button", {name: tr("pages.apiKeys.createSubmit"), exact:true}).click();
      const response = await minted; expect(response.status()).toBe(200);
      const key = await response.json(); keyID = key.token_id;
      await page.keyboard.press("Escape");
      const invoke = () => page.request.post(GATEWAY + "/v1/chat/completions", {headers: {Authorization: "Bearer " + key.key}, data: {model, messages:[{role:"user",content:"Reply briefly."}]}});
      const first = await invoke(); expect(first.status(), await first.text()).toBe(200);
      expect(first.headers()["x-xhub-budget-guarantee"]).toBe("soft");
      expect(first.headers()["x-xhub-budget-reservation-id"]).toBeUndefined();
      const info = await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, {headers})).json();
      expect(info.info.spend).toBeGreaterThan(0.000001);
      expect(info.info.budget_pending).toBe(0);
      expect((await invoke()).status()).toBe(429);
      await stableGoto(page, "/api-keys?key=" + keyID);
      await expect(page.getByText(tr("hardBudget.contract"))).toBeVisible();
      await page.getByLabel(tr("Max Budget (USD)"), {exact: true}).fill("1");
      const updated = page.waitForResponse(r => r.url().includes("/key/update") && r.request().method() === "POST");
      await page.getByRole("button", {name: tr("Save Changes"), exact:true}).click();
      expect((await updated).status()).toBe(200);
      expect((await invoke()).status()).toBe(200);
    } finally {
      if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", {headers,data:{keys:[keyID]}})).status()).toBe(200);
      if (modelID) expect((await page.request.post(GATEWAY + "/model/delete", {headers,data:{id:modelID}})).status()).toBe(200);
    }
  });
}
