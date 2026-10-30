import { expect, test } from "@playwright/test";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";
import { waitForLog } from "./product-fixes-scenario";

/** 双语真实页面创建/编辑价格、同源对话及账单原因验证；本地上游报告缓存Token，不访问供应商。
 * 缺价保留原准入，补价和显式零价精确收费；finally删除模型/凭据并关闭服务，runner回收schema。 */
for (const locale of ["en", "zh-CN"] as const) {
  test("cache pricing warning and actual bill " + locale, async ({ page }) => {
    const tr = (key: string, params?: Record<string, string>) => translate(locale, key, params);
    const upstream = createServer(async (req, res) => {
      for await (const _ of req) {
        /* 完整消费真实请求正文后才返回。 */
      }
      res.setHeader("Content-Type", "application/json");
      if (req.method === "GET") {
        res.end(JSON.stringify({ data: [{ id: "cache-price-model" }] }));
        return;
      }
      res.end(
        JSON.stringify({
          id: "cache-price-answer",
          choices: [{ index: 0, message: { role: "assistant", content: "PRICE_OK" }, finish_reason: "stop" }],
          usage: {
            prompt_tokens: 8,
            completion_tokens: 2,
            total_tokens: 10,
            prompt_tokens_details: { cached_tokens: 4 },
          },
        }),
      );
    });
    await new Promise<void>((resolve) => upstream.listen(0, "127.0.0.1", resolve));
    const base = "http://127.0.0.1:" + (upstream.address() as AddressInfo).port;
    let id = "",
      createdCredential = false;
    const credential = "cache-price-" + locale + "-" + Date.now(),
      model = credential;
    let headers: Record<string, string> = {};
    try {
      await loginAdmin(page);
      headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      const cr = await page.request.post(GATEWAY + "/credentials", {
        headers,
        data: {
          credential_name: credential,
          credential_info: { custom_llm_provider: "openai" },
          credential_values: { api_base: base, api_key: "fixture-price-key" },
        },
      });
      expect(cr.status(), await cr.text()).toBe(200);
      createdCredential = true;
      // 通过真实页面创建缺价模型，之后编辑补价；不依赖固定价格目录。
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("button", { name: tr("pages.models.add"), exact: true }).click();
      await page.getByLabel(tr("providerGuideUI.providerLabel"), { exact: true }).selectOption(credential);
      const upstreamModel = page.getByRole("combobox", { name: tr("providerGuideUI.upstreamLabel"), exact: true });
      await upstreamModel.fill("cache-price-model");
      await upstreamModel.press("Escape");
      await page.getByLabel(tr("providerGuideUI.publicName"), { exact: true }).fill(model);
      await page.getByRole("combobox", { name: tr("Upstream protocol"), exact: true }).click();
      await page.getByRole("option", { name: "OpenAI · Chat Completions", exact: true }).click();
      await page.getByLabel(tr("providerGuideUI.priceSource"), { exact: true }).selectOption("manual");
      await page.locator("#editor-input_cost_per_token").fill("1");
      await page.locator("#editor-output_cost_per_token").fill("2");
      const read = page.locator("#editor-cache_read_input_token_cost"),
        write = page.locator("#editor-cache_creation_input_token_cost");
      await expect(page.getByRole("status", { name: tr("cachePrice.warningTitle") })).toBeVisible();
      await read.fill("0");
      await write.fill("0");
      await expect(page.getByRole("status", { name: tr("cachePrice.warningTitle") })).toHaveCount(0);
      await read.fill("");
      await expect(page.getByRole("status", { name: tr("cachePrice.warningTitle") })).toBeVisible();
      const creating = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/model/new") && r.request().method() === "POST");
      await page.locator("form").filter({ has: read }).getByRole("button", { name: tr("providerGuideUI.addModel"), exact: true }).click();
      const created = await creating;
      expect(created.status(), await created.text()).toBe(200);
      id = (await created.json()).model_info.id;
      await expect(read).toBeHidden();
      for (const [index, rate, want] of [
        [0, "", 0],
        [1, "1", 0.000012],
        [2, "0", 0.000008],
      ] as const) {
        if (index > 0) {
          await stableGoto(page, "/models-and-endpoints?model=" + id);
          await page.getByRole("button", { name: tr("providerGuideUI.editModel"), exact: true }).click();
          await read.fill(rate);
          await write.fill("0");
          await expect(page.getByRole("status", { name: tr("cachePrice.warningTitle") })).toHaveCount(0);
          await page.getByRole("button", { name: tr("providerGuideUI.saveChanges"), exact: true }).click();
          await expect(read).toBeHidden();
        }
        const result = await page.evaluate(
          async ({ model, index, headers }) => {
            const r = await fetch("/gw/v1/chat/completions", {
              method: "POST",
              headers: { ...headers, "Content-Type": "application/json" },
              body: JSON.stringify({
                model,
                messages: [{ role: "user", content: "Verify cache price round " + index }],
              }),
            });
            return { status: r.status, text: await r.text(), id: r.headers.get("x-litellm-call-id") };
          },
          { model, index, headers },
        );
        expect(result.status, result.text).toBe(200);
        expect(result.text).toContain("PRICE_OK");
        const log = await waitForLog(page.request, headers, result.id!);
        expect(Number(log.spend)).toBeCloseTo(want, 12);
        expect(log.metadata.cost_breakdown.pricing_status).toBe(index === 0 ? "unpriced" : "priced");
        if (index === 0) {
          expect(log.metadata.cost_breakdown.usage.PricingBlocked).toBe("cache_read_price_missing");
          await stableGoto(page, "/logs?log_id=" + result.id);
          await page.getByText(tr("Cost Breakdown"), { exact: true }).click();
          await expect(page.getByText(tr("cachePrice.readMissing"), { exact: true })).toBeVisible();
        }
      }
    } finally {
      try {
        if (id)
          expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).status()).toBe(200);
        if (createdCredential)
          expect((await page.request.delete(GATEWAY + "/credentials/" + credential, { headers })).status()).toBe(200);
      } finally {
        upstream.closeAllConnections();
        await new Promise<void>((resolve, reject) => upstream.close((e) => (e ? reject(e) : resolve())));
      }
    }
  });
}
