import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 创建本地夹具的原始USD千Token档位；参数为输入区间和倍率，返回完整价格，不读取或改写生产模型。 */
function tier(low: number, high: number, multiplier: number) {
  const unit = (usd: number) => ({ unit_name: "token", unit_size: 1000, unit_price_usd: usd * 1000 });
  return {
    item_order: ["ncache", "cache", "c_cache", "output"],
    input_range: [low, high],
    output_range: [0, 100],
    details_v2: {
      ncache: unit(multiplier * 0.001),
      output: unit(multiplier * 0.002),
      cache: unit(multiplier * 0.0001),
      c_cache: unit(multiplier * 0.00125),
    },
  };
}

/** 注册双语分档预算闭环；真实浏览器创建/编辑/删除Key，真实本地上游固定usage8/2验证高档账单。
 * 缺口和共享阈值不阻断对话，账单明确未定价且不冻结额度，来源认证的旧cap推理请求正常准入；finally删除专用Key/模型。 */
export function registerBudgetTierScenario(locale: "en" | "zh-CN") {
  test("tiered budget prices and ambiguous boundary " + locale, async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    const tr = (key: string) => translate(locale, key);
    const model = "tier-browser-" + locale.toLowerCase() + "-" + Date.now(),
      alias = "tier-key-" + locale;
    let modelID = "",
      keyID = "",
      sequence = 0;
    const params = (rules: ReturnType<typeof tier>[]) => ({
      model,
      custom_llm_provider: "openai",
      api_base: UPSTREAM,
      api_key: "sk-fake",
      input_cost_per_token: 0.001,
      output_cost_per_token: 0.002,
      pricing_rules_v2: rules,
    });
    const modelInfo = {
      transport: "bypass_openai_chat",
      pricing_source: "manual",
      endpoint_types: ["chat"],
      max_input_tokens: 100,
      max_output_tokens: 10,
      supported_parameters: ["max_tokens"],
      max_tokens_includes_reasoning: true,
      max_tokens_semantics_source: "https://example.test/local-fixture-cap-contract",
    };
    try {
      const created = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: { model_name: model, litellm_params: params([tier(0, 7, 1), tier(7, 200, 2)]), model_info: modelInfo },
      });
      expect(created.status(), await created.text()).toBe(200);
      modelID = (await created.json()).model_info.id;
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name")).fill(alias);
      await page.getByLabel(tr("Max Budget (USD)"), { exact: true }).fill("0.1");
      const minted = page.waitForResponse((r) => r.url().includes("/key/generate") && r.request().method() === "POST");
      await page.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
      const keyResponse = await minted;
      expect(keyResponse.status()).toBe(200);
      const key = await keyResponse.json();
      keyID = key.token_id;
      await page.keyboard.press("Escape");
      /** 每次独立请求避免缓存命中；返回真实响应，英文正文不随界面语言改变。 */
      const invoke = () =>
        page.request.post(GATEWAY + "/v1/chat/completions", {
          headers: { Authorization: "Bearer " + key.key },
          data: {
            model,
            max_tokens: 10,
            reasoning_effort: "high",
            messages: [{ role: "user", content: "Verify frozen tier pricing request " + ++sequence + "." }],
          },
        });
      const low = await invoke();
      expect(low.status()).toBe(200);
      expect(low.headers()["x-xhub-budget-guarantee"]).toBe("soft");
      await stableGoto(page, "/api-keys?key=" + keyID);
      await expect(page.getByText(translate(locale, "hardBudget.contract", { cap: 4096 }))).toBeVisible();
      await page.getByLabel(tr("Max Budget (USD)"), { exact: true }).fill("1");
      const updated = page.waitForResponse((r) => r.url().includes("/key/update") && r.request().method() === "POST");
      await page.getByRole("button", { name: tr("Save Changes"), exact: true }).click();
      expect((await updated).status()).toBe(200);
      const paid = await invoke();
      expect(paid.status(), await paid.text()).toBe(200);
      expect(paid.headers()["x-xhub-max-output-tokens"]).toBeUndefined();
      expect(paid.headers()["x-xhub-budget-reserved"]).toBeUndefined();
      await expect
        .poll(
          async () =>
            (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info.spend,
        )
        .toBeCloseTo(0.048, 9);
      const log = await page.request.get(GATEWAY + "/spend/logs/ui/" + paid.headers()["x-litellm-call-id"], {
        headers,
      });
      expect(log.status()).toBe(200);
      expect((await log.json()).spend).toBeCloseTo(0.024, 9);
      await stableGoto(page, "/api-keys?key=" + keyID);
      await expect(page.getByText(translate(locale, "hardBudget.pending", { amount: 0 }))).toBeVisible();
      /** 修改仅属于本测试的真实模型配置；规则参数返回HTTP结果，保留连接与认证cap元数据。 */
      const setRules = (rules: ReturnType<typeof tier>[]) =>
        page.request.post(GATEWAY + "/model/update", {
          headers,
          data: { model_info: { ...modelInfo, id: modelID }, model_name: model, litellm_params: params(rules) },
        });
      // 真实控制台保存习惯：切回catalog以null清除手工覆盖；本地供应商仍使用同一可信小窗口，不能把null当免费或不完整manual。
      const catalogSaved = await page.request.post(GATEWAY + "/model/update", {
        headers,
        data: {
          model_name: model,
          model_info: {
            ...modelInfo,
            id: modelID,
            pricing_source: "catalog",
            base_model: "deepseek/deepseek-v4.1-flash",
          },
          litellm_params: {
            ...params([]),
            pricing_rules_v2: null,
            input_cost_per_token: null,
            output_cost_per_token: null,
            cache_read_input_token_cost: null,
            rates: null,
          },
        },
      });
      expect(catalogSaved.status(), await catalogSaved.text()).toBe(200);
      const catalogPaid = await invoke();
      expect(catalogPaid.status(), await catalogPaid.text()).toBe(200);
      await expect
        .poll(
          async () =>
            (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info
              .budget_pending,
        )
        .toBe(0);
      const paidSpend = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info
        .spend;
      expect(paidSpend).toBeGreaterThan(0.024);
      const gap = await setRules([tier(0, 7, 1), tier(9, 200, 2)]);
      expect(gap.status(), await gap.text()).toBe(200);
      const refused = await invoke();
      expect(refused.status()).toBe(200);
      expect(refused.headers()["x-litellm-response-cost"]).toBeUndefined();
      const boundary = await setRules([tier(0, 8, 1), tier(8, 200, 2)]);
      expect(boundary.status(), await boundary.text()).toBe(200);
      const uncertain = await invoke();
      expect(uncertain.status(), await uncertain.text()).toBe(200);
      expect(uncertain.headers()["x-litellm-response-cost"]).toBeUndefined();
      const details = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, {headers})).json()).info;
      expect(details.spend).toBeCloseTo(paidSpend, 9);
      expect(details.budget_pending).toBe(0);
      expect(details.budget_reservations ?? []).toHaveLength(0);
      expect((await invoke()).status()).toBe(200);
      await page.getByRole("button", { name: tr("More key actions"), exact: true }).click();
      await page.getByRole("menuitem", { name: tr("Delete Key"), exact: true }).click();
      await page.getByRole("dialog").getByPlaceholder(alias, { exact: true }).fill(alias);
      await page
        .getByRole("dialog")
        .getByRole("button", { name: tr("common.delete"), exact: true })
        .click();
      await expect(page.getByTestId("create-key-button")).toBeVisible();
      expect((await invoke()).status()).toBe(401);
      keyID = "";
    } finally {
      if (keyID)
        expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status()).toBe(
          200,
        );
      if (modelID)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(
          200,
        );
    }
  });
}
