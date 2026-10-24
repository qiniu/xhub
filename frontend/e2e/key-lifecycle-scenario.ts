import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册 Key 生命周期双语闭环；locale 为界面语言，无返回值。
 * 前置私有数据库、真实网关与本地模型上游；浏览器创建、禁用/恢复、编辑到期、轮换恢复、回读限额、调用模型和删除。
 * 不支持的管理类型、自动轮换、宽限期必须可见且真实 API 拒绝；finally 删除仅本场景创建的 Key，运行器回收 schema。 */
export function registerKeyLifecycleScenario(locale: "en" | "zh-CN") {
  test("key lifecycle settings and atomic rotation " + locale, async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    const tr = (key: string) => translate(locale, key);
    const alias = "lifecycle-original-用户-" + locale + "-" + Date.now();
    let id = "",
      modelID = "";
    const model = "e2e-lifecycle-priced-" + locale;
    try {
      // 费用断言使用明确单价的独立模型，不能依赖目录中未定价的默认部署。
      const configured = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: model,
          litellm_params: {
            model: "gpt-4o-mini",
            api_base: UPSTREAM + "/v1",
            api_key: "sk-fake",
            custom_llm_provider: "openai",
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
          model_info: {
            transport: "bypass_openai_chat",
            endpoint_types: ["chat"],
            pricing_source: "manual",
            max_input_tokens: 1024,
            max_output_tokens: 8192,
            supported_parameters: ["max_completion_tokens"],
          },
        },
      });
      expect(configured.status(), await configured.text()).toBe(200);
      modelID = (await configured.json()).model_info.id;
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name")).fill(alias);
      await expect(page.getByText(tr("keyEditor.rotationUnsupported"))).toBeVisible();
      await expect(page.getByRole("switch")).toHaveCount(0);
      await page.getByRole("combobox", { name: tr("Key Type"), exact: true }).click();
      await expect(page.getByRole("option", { name: /Management|管理/ })).toHaveCount(0);
      await page.getByRole("option", { name: tr("AI APIs") }).click();
      await page.getByRole("textbox", { name: tr("keyEditor.expire"), exact: true }).fill("30d");
      const mintedPromise = page.waitForResponse(
        (r) => r.url().includes("/key/generate") && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
      const minted = await mintedPromise;
      expect(minted.status()).toBe(200);
      const original = await minted.json();
      id = original.token_id;
      expect(original).toMatchObject({ key_type: "llm_api", auto_rotate: false, rotation_interval: null });
      await page.keyboard.press("Escape");
      const invalidRotation = await page.request.post(GATEWAY + "/key/" + id + "/regenerate", {
        headers: { ...headers, "Content-Type": "application/json" },
        data: '{"duration":',
      });
      expect(invalidRotation.status()).toBe(400);
      expect(
        (
          await page.request.get(GATEWAY + "/v1/models", { headers: { Authorization: "Bearer " + original.key } })
        ).status(),
      ).toBe(200);
      const body = { model, messages: [{ role: "user", content: "Verify key lifecycle access." }] };
      const first = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + original.key },
        data: body,
      });
      expect(first.status(), await first.text()).toBe(200);
      expect((await first.json()).choices[0].message.content).toMatch(/ok/);
      const firstCost = Number(first.headers()["x-litellm-response-cost"]);
      expect(firstCost).toBeGreaterThan(0);
      await stableGoto(page, "/api-keys?key=" + id);
      await expect(page.getByLabel(tr("Key Alias"))).toHaveValue(alias);
      await expect(page.getByText(tr("keyEditor.apiOnly"))).toBeVisible();
      // 浏览器禁用/恢复后核验真实推理入口，避免仅根据UI乐观状态认定保存成功。
      for (const blocked of [true, false]) {
        await page.getByRole("button", { name: tr("More key actions"), exact: true }).click();
        await page.getByRole("menuitem", { name: tr(blocked ? "Block Key" : "Unblock Key"), exact: true }).click();
        const toggle = page.waitForResponse(r => r.url().endsWith(blocked ? "/key/block" : "/key/unblock") && r.request().method() === "POST");
        await page.getByRole("dialog").getByRole("button", { name: tr(blocked ? "Block" : "Unblock"), exact: true }).click();
        const saved = await toggle;
        expect(saved.status(), await saved.text()).toBe(200);
        expect(await saved.json()).toEqual({ blocked });
        await expect(page.getByRole("dialog")).toHaveCount(0);
        const detailResponse = await page.request.get(GATEWAY + "/key/info?key=" + id, { headers });
        expect(detailResponse.status()).toBe(200);
        const detail = (await detailResponse.json()).info;
        expect(detail.blocked).toBe(blocked);
        expect(detail.key).toBeUndefined();
        expect(detail.token).not.toBe(original.key);
        const inference = await page.request.post(GATEWAY + "/v1/chat/completions", {
          headers: { Authorization: "Bearer " + original.key }, data: body,
        });
        expect(inference.status(), await inference.text()).toBe(blocked ? 401 : 200);
        if (!blocked) expect((await inference.json()).choices[0].message.content).toMatch(/e2e-ok/);
      }

      await page.getByRole("textbox", { name: tr("keyEditor.expire"), exact: true }).fill("1s");
      const updatedPromise = page.waitForResponse(
        (r) => r.url().includes("/key/update") && r.request().method() === "POST",
      );
      await page.getByRole("button", { name: tr("Save Changes"), exact: true }).click();
      expect((await updatedPromise).status()).toBe(200);
      await expect
        .poll(async () =>
          (
            await page.request.get(GATEWAY + "/v1/models", { headers: { Authorization: "Bearer " + original.key } })
          ).status(),
        )
        .toBe(401);
      await stableGoto(page, "/api-keys?key=" + id);
      await page.getByRole("button", { name: tr("More key actions"), exact: true }).click();
      await page.getByRole("menuitem", { name: tr("Regenerate Key"), exact: true }).click();
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByText(tr("keyEditor.immediateRotation"))).toBeVisible();
      await expect(dialog.getByLabel(tr("Grace Period"))).toHaveCount(0);
      await dialog.getByRole("button", { name: tr("Regenerate"), exact: true }).click();
      await expect(dialog.getByText(tr("keyEditor.expiredDurationRequired"))).toBeVisible();
      await dialog.getByLabel(tr("Expire Key")).fill("1h");
      await dialog.getByLabel(tr("Max Budget (USD)")).fill("1");
      await dialog.getByLabel(tr("RPM Limit")).fill("10");
      await dialog.getByLabel(tr("TPM Limit")).fill("10000");
      const rotatedPromise = page.waitForResponse(
        (r) => r.url().includes("/regenerate") && r.request().method() === "POST",
      );
      await dialog.getByRole("button", { name: tr("Regenerate"), exact: true }).click();
      const rotatedResponse = await rotatedPromise;
      expect(rotatedResponse.status(), await rotatedResponse.text()).toBe(200);
      const rotated = await rotatedResponse.json();
      expect(rotated).toMatchObject({ key_alias: alias, max_budget: 1, rpm_limit: 10, tpm_limit: 10000 });
      expect(Date.parse(rotated.expires)).toBeGreaterThan(Date.now() + 3_500_000);
      await dialog
        .getByRole("button", { name: tr("Close"), exact: true })
        .last()
        .click();
      await stableGoto(page, "/api-keys?key=" + id);
      await expect(page.getByLabel(tr("Max Budget (USD)"))).toHaveValue("1");
      await expect(page.getByLabel(tr("Key Alias"))).toHaveValue(alias);
      const snapshot = await (await page.request.get(GATEWAY + "/key/info?key=" + id, { headers })).json();
      expect(snapshot.info).toMatchObject({
        max_budget: 1,
        rpm_limit: 10,
        tpm_limit: 10000,
        key_type: "llm_api",
        auto_rotate: false,
      });
      for (const unsupported of [
        { key_type: "management" },
        { auto_rotate: true, rotation_interval: "30d" },
        { grace_period: "60s" },
      ]) {
        const rejected = await page.request.post(GATEWAY + "/key/" + id + "/regenerate", {
          headers,
          data: unsupported,
        });
        expect(rejected.status(), await rejected.text()).toBe(400);
      }
      expect(
        (
          await page.request.get(GATEWAY + "/v1/models", { headers: { Authorization: "Bearer " + original.key } })
        ).status(),
      ).toBe(401);
      const inferred = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers: { Authorization: "Bearer " + rotated.key },
        data: body,
      });
      expect(inferred.status(), await inferred.text()).toBe(200);
      expect((await inferred.json()).choices[0].message.content).toMatch(/ok/);
      // 完整浏览器轮换后，以真实数据面的累计头验证旧凭据消费没有归零；队列未刷写边界由专用 regression 固定覆盖。
      expect(Number(inferred.headers()["x-litellm-key-spend"])).toBeGreaterThanOrEqual(firstCost * 2 - 1e-12);
      await page.getByRole("button", { name: tr("More key actions"), exact: true }).click();
      await page.getByRole("menuitem", { name: tr("Delete Key"), exact: true }).click();
      await page.getByRole("dialog").getByPlaceholder(alias, { exact: true }).fill(alias);
      await page
        .getByRole("dialog")
        .getByRole("button", { name: tr("common.delete"), exact: true })
        .click();
      await expect(page.getByTestId("create-key-button")).toBeVisible();
      expect(
        (
          await page.request.get(GATEWAY + "/v1/models", { headers: { Authorization: "Bearer " + rotated.key } })
        ).status(),
      ).toBe(401);
      id = "";
    } finally {
      if (modelID)
        expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } })).status()).toBe(
          200,
        );
      if (id)
        expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [id] } })).status()).toBe(
          200,
        );
    }
  });
}
