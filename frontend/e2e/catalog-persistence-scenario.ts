import { expect, test } from "@playwright/test";

import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, uiPath } from "./helpers";

/**
 * 注册供应商目录持久化的双语完整流程；locale 为页面语言，shortKey 选择五字节单星号边界（默认长 Key），无返回值。
 * 前置隔离数据库、真实网关、生产构建控制台和本地上游；页面创建/编辑/删除供应商，
 * 接口回读后验证错误 Key 轮换真实拒绝、恢复后经调试台推理成功，覆盖空 Key/null 扩展与列表别名，并验证非法 JSON/RPC 不覆盖已保存配置，OAuth 占位入口明确失败且不影响已登录会话。
 * 回调页验证双语提示和真实跳转，不将收到回调当作授权成功；临时无脚本上下文仅用于观察跳转前的服务端页面文案。
 * 不拦截或伪造后台响应；finally 清理部署和凭据，运行器另行回收专属 schema。
 */
export function registerCatalogPersistence(locale: Locale, shortKey = false) {
  const credentialKey = shortKey ? "abcde" : "sk-fake";
  const keyMask = shortKey ? "ab*de" : "sk***ke";
  const upstreamModel = shortKey ? "e2e-short-key-chat" : "e2e-item-id-chat";
  test("catalog persistence survives reload and rejects invalid updates " + locale + (shortKey ? " short key" : ""), async ({ page, browser }) => {
    test.setTimeout(120_000);
    const tr = (key: string) => translate(locale, key);
    // token是合法供应商名，必须走真实CRUD，不能被路径后缀误判成OAuth令牌交换。
    const name = "token";
    const modelName = "e2e-catalog-integrity-" + locale + (shortKey ? "-short-model" : "-model");
    let credentialExists = false;
    let modelID = "";
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    try {
      // 真实HTTP验证保留OAuth入口无令牌、无伪造成功，并且不会清除当前正常登录会话。
      for (const endpoint of ["/v1/mcp/server/oauth/missing/token", "/authorize/complete"]) {
        const denied = await page.request.post(GATEWAY + endpoint, {
          headers, form: { grant_type: "authorization_code", code: "invalid-local-code", code_verifier: "invalid-local-verifier" },
        });
        expect(denied.status(), "未执行的授权入口不能返回成功").toBe(501);
        expect(await denied.json()).toMatchObject({ error: { type: "not_implemented" } });
        expect(await denied.text()).not.toContain("access_token");
        expect(denied.headers()["cache-control"]).toBe("no-store");
      }
      const flow = await page.request.get(GATEWAY + "/authorize/flow?flow=e2e-fixture", { headers });
      expect(flow.status(), "旧fixture不能作为真实已连接状态").toBe(501);
      expect((await page.request.get(GATEWAY + "/auth/me", { headers })).status()).toBe(200);
      const callbackURL = new URL(uiPath("/mcp/oauth/callback?error=access_denied&state=local-fixture"), page.url()).href;
      // 禁用脚本观察真实服务端渲染文案，避免自动跳转导致断言竞争；未拦截或替换 HTTP 内容。
      const preview = await browser.newContext({ javaScriptEnabled: false, storageState: await page.context().storageState() });
      try {
        const callback = await preview.newPage();
        const result = await callback.goto(callbackURL);
        expect(result?.status()).toBe(200);
        await expect(callback.getByRole("heading", { name: tr("mcpOAuthCallback.title") })).toBeVisible();
        await expect(callback.getByText(tr("mcpOAuthCallback.pending"), { exact: true })).toBeVisible();
        await expect(callback.getByText(tr("mcpOAuthCallback.returnHelp"), { exact: true })).toBeVisible();
        await expect(callback.getByText(tr("Authorization complete. You may close this window and return to the LiteLLM dashboard."))).toHaveCount(0);
      } finally {
        await preview.close();
      }
      // 常规浏览器执行真实回调和重定向；保留上游错误，不产生令牌，当前会话仍可使用。
      await page.goto(callbackURL, { waitUntil: "commit" });
      await expect(page).not.toHaveURL(/\/mcp\/oauth\/callback/);
      const callbackPayload = await page.evaluate(() => {
        const raw = sessionStorage.getItem("litellm-tools-mcp-oauth-result");
        return raw ? JSON.parse(atob(raw)) : null;
      });
      expect(callbackPayload).toMatchObject({ code: null, error: "access_denied", state: "local-fixture" });
      expect(callbackPayload).not.toHaveProperty("access_token");
      expect((await page.request.get(GATEWAY + "/auth/me", { headers })).status()).toBe(200);
      await stableGoto(page, "/model-providers");
      await page.getByRole("button", { name: tr("modelProviders.add"), exact: true }).click();
      const create = page.getByRole("dialog", { name: tr("Add model provider"), exact: true });
      await create.getByRole("combobox", { name: tr("Provider type") }).fill("OpenAI");
      await page.getByRole("option").filter({ has: page.getByText("OpenAI", { exact: true }) }).click();
      await create.getByLabel(tr("Provider name")).fill(name);
      await create.getByLabel("API Base", { exact: true }).fill(UPSTREAM);
      await create.getByLabel("OpenAI API Key", { exact: true }).fill(credentialKey);
      const creation = page.waitForResponse(response => new URL(response.url()).pathname === "/credentials" && response.request().method() === "POST");
      await create.getByRole("button", { name: tr("Add model provider"), exact: true }).click();
      const created = await creation;
      expect(created.status(), "页面创建供应商必须由真实后台成功持久化").toBe(200);
      credentialExists = true;
      expect(await created.json()).toMatchObject({ id: name, credential_name: name });
      expect(await created.json(), "创建响应应省略凭据对象").not.toHaveProperty("credential_values");
      expect(created.request().postDataJSON()).toMatchObject({
        credential_name: name,
        credential_info: { custom_llm_provider: "openai" },
        credential_values: { api_base: UPSTREAM, api_key: credentialKey },
      });
      await expect(create).toHaveCount(0);
      await page.reload();
      await page.getByTestId("credential-actions-" + name).click();
      await page.getByTestId("credential-action-edit").click();
      const edit = page.getByRole("dialog", { name: tr("Edit model provider"), exact: true });
      await expect(edit.getByLabel("API Base", { exact: true })).toHaveValue(UPSTREAM);
      await expect(edit.getByLabel("OpenAI API Key", { exact: true })).toHaveValue(keyMask);
      await edit.getByLabel("API Base", { exact: true }).fill(UPSTREAM + "/v1");
      const update = page.waitForResponse(response => new URL(response.url()).pathname === "/credentials/" + name && response.request().method() === "PATCH");
      await edit.getByRole("button", { name: tr("Save model provider"), exact: true }).click();
      expect((await update).status(), "编辑后的供应商地址必须保存成功").toBe(200);
      await expect(edit).toHaveCount(0);

      // 真实补丁保留原 Key；null 扩展应保留，后续模型推理会证明没有把空 Key 写入供应商。
      const emptySecret = await page.request.patch(GATEWAY + "/credentials/" + name, {
        headers, data: { credential_values: { api_key: "" }, contract_note: null, contract_enabled: false },
      });
      expect(emptySecret.status(), "空 Key 补丁应沿用已有凭据").toBe(200);
      const listed = await page.request.get(GATEWAY + "/credentials", { headers });
      expect(listed.status()).toBe(200);
      const listing = await listed.json();
      expect(listing.data, "两个兼容列表字段应一致").toEqual(listing.credentials);
      expect(listing.data).toEqual(expect.arrayContaining([expect.objectContaining({
        credential_name: name, contract_note: null, contract_enabled: false,
      })]));
      const stored = await page.request.get(GATEWAY + "/credentials/by_name/" + name, { headers });
      expect(stored.status(), "刷新后的供应商应能通过独立接口回读").toBe(200);
      expect(await stored.json()).toMatchObject({
        credential_name: name,
        credential_values: { api_base: UPSTREAM + "/v1", api_key: keyMask },
      });
      // 截断 JSON 是 HTTP 契约失败，不能被当作空补丁返回成功或清空现有地址。
      const invalid = await page.request.patch(GATEWAY + "/credentials/" + name, {
        headers: { ...headers, "Content-Type": "application/json" },
        data: '{"credential_values":{"api_base":',
      });
      expect(invalid.status(), "非法 JSON 更新必须明确拒绝，不能伪报保存成功").toBe(400);
      expect((await invalid.json()).error, "失败响应应包含明确的错误对象").toBeTruthy();
      const rpc = await page.request.patch(GATEWAY + "/credentials/" + name, {
        headers, data: { jsonrpc: "2.0", id: "probe", method: "tools/list", credential_values: { api_base: "http://127.0.0.1:1" } },
      });
      expect(rpc.status(), "REST目录不能以空工具列表伪报RPC成功").toBe(400);
      expect(await rpc.json()).toMatchObject({ error: { type: "unsupported_protocol" } });
      const unchanged = await page.request.get(GATEWAY + "/credentials/by_name/" + name, { headers });
      expect(await unchanged.json()).toEqual(await stored.json());
      await page.reload();
      await page.getByTestId("credential-actions-" + name).click();
      await page.getByTestId("credential-action-edit").click();
      await expect(edit.getByLabel("API Base", { exact: true })).toHaveValue(UPSTREAM + "/v1");
      await expect(edit.getByLabel("OpenAI API Key", { exact: true })).toHaveValue(keyMask);
      await edit.getByRole("button", { name: tr("Cancel"), exact: true }).click();

      // 部署仅引用命名凭据，不另填 api_base/api_key，随后实际调用才能证明持久化配置被执行链读取。
      const deployment = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: modelName,
        litellm_params: {
          model: upstreamModel, custom_llm_provider: "openai", litellm_credential_name: name,
          input_cost_per_token: 0.000001, output_cost_per_token: 0.000002,
        },
        model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
      } });
      expect(deployment.status(), "引用真实供应商配置的部署必须可创建").toBe(200);
      modelID = (await deployment.json()).model_info.id;
      const model = await page.request.get(GATEWAY + "/v2/model/info?modelId=" + encodeURIComponent(modelID), { headers });
      expect(model.status(), "部署保存后应可独立回读").toBe(200);
      expect((await model.json()).data[0].litellm_params.litellm_credential_name).toBe(name);

      // 在重新输入任何真实 Key 前调用，单看回读掩码无法区分原值与已被错误保存的掩码。
      const preservedInference = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers, data: { model: modelName, messages: [{ role: "user", content: "Verify unchanged masked credentials still authenticate." }] },
      });
      expect(preservedInference.status(), "页面保存回传掩码后，原始供应商 Key 必须仍可鉴权").toBe(200);
      expect((await preservedInference.json()).choices[0].message.content).toBe("e2e-source");

      // 页面轮换成错误 Key 后，真实数据面必须拒绝；恢复正确 Key 后沿用原部署完成页面推理。
      // 这样既验证编辑持久化，又避免旧缓存凭据让错误配置看起来仍可用。
      await page.getByTestId("credential-actions-" + name).click();
      await page.getByTestId("credential-action-edit").click();
      await edit.getByLabel("OpenAI API Key", { exact: true }).fill("sk-invalid-rotation");
      const rejectedKeySave = page.waitForResponse(response => new URL(response.url()).pathname === "/credentials/" + name && response.request().method() === "PATCH");
      await edit.getByRole("button", { name: tr("Save model provider"), exact: true }).click();
      expect((await rejectedKeySave).status()).toBe(200);
      await expect(edit).toHaveCount(0);
      const rejectedInference = await page.request.post(GATEWAY + "/v1/chat/completions", {
        headers, data: { model: modelName, messages: [{ role: "user", content: "Verify invalid provider credentials are rejected." }] },
      });
      expect(rejectedInference.status(), "新 Key 必须立即用于真实上游，不能复用旧 Key 伪成功").toBe(401);
      expect((await page.request.get(GATEWAY + "/auth/me", { headers })).status(), "供应商 Key 错误不得退出管理会话").toBe(200);
      await page.reload();
      await page.getByTestId("credential-actions-" + name).click();
      await page.getByTestId("credential-action-edit").click();
      await edit.getByLabel("OpenAI API Key", { exact: true }).fill(credentialKey);
      const restoredKeySave = page.waitForResponse(response => new URL(response.url()).pathname === "/credentials/" + name && response.request().method() === "PATCH");
      await edit.getByRole("button", { name: tr("Save model provider"), exact: true }).click();
      expect((await restoredKeySave).status()).toBe(200);
      await expect(edit).toHaveCount(0);

      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: modelName, exact: true }).click();
      await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
      await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/chat\/completions$/ }).click();
      const before = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(before.status()).toBe(200);
      const count = (await before.json()).requests;
      const prompt = "Verify the saved provider is used without changing this prompt.";
      await page.getByTestId("chat-composer-input").fill(prompt);
      const inference = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/chat/completions") && response.request().method() === "POST");
      await page.getByTestId("chat-send-button").click();
      const answer = await inference;
      expect(answer.status(), "浏览器推理必须由本地上游实际响应").toBe(200);
      expect(answer.request().postDataJSON().model).toBe(modelName);
      await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-source" })).toBeVisible();
      await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
      const after = await page.request.get(UPSTREAM + "/e2e/request-count");
      expect(after.status()).toBe(200);
      expect((await after.json()).requests, "一次页面推理必须真实抵达上游一次").toBe(count + 1);
      // 专属上游型号严格校验当前长/五字节原始 Key；编辑时回传的脱敏值不能覆盖真实凭据。
      const callID = answer.headers()["x-litellm-call-id"];
      expect(callID, "页面调用必须可追踪至真实用量记录").toBeTruthy();
      await expect.poll(async () => {
        const usage = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
        if (usage.status() !== 200) return null;
        const row = await usage.json();
        return { model: row.model, prompt: row.prompt_tokens, completion: row.completion_tokens, spend: Number(row.spend) };
      }).toEqual({ model: modelName, prompt: 8, completion: 2, spend: 0.000012 });

      const deletedModel = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } });
      expect(deletedModel.status(), "删除测试部署应成功").toBe(200);
      modelID = "";
      await stableGoto(page, "/model-providers");
      await page.getByTestId("credential-actions-" + name).click();
      await page.getByTestId("credential-action-delete").click();
      const remove = page.getByRole("dialog");
      await remove.getByPlaceholder(name, { exact: true }).fill(name);
      const deletion = page.waitForResponse(response => new URL(response.url()).pathname === "/credentials/" + name && response.request().method() === "DELETE");
      await remove.getByRole("button", { name: tr("Delete"), exact: true }).click();
      const deleted = await deletion;
      expect(deleted.status(), "页面删除供应商必须成功").toBe(200);
      expect((await deleted.json()).deleted, "删除应报告实际删除一条记录").toBe(1);
      credentialExists = false;
      await expect(page.getByTestId("credential-actions-" + name)).toHaveCount(0);
      await page.reload();
      await expect(page.getByTestId("credential-actions-" + name)).toHaveCount(0);
      const missing = await page.request.get(GATEWAY + "/credentials/by_name/" + name, { headers });
      expect(missing.status(), "删除后详情不能合成一个伪造的成功资源").toBe(404);
    } finally {
      // 独立记录清理错误并继续清理下一资源，避免一次断言失败导致凭据残留。
      test.setTimeout(150_000);
      if (modelID) {
        const deleted = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } });
        expect.soft(deleted.status(), "finally 清理测试部署").toBe(200);
      }
      if (credentialExists) {
        const deleted = await page.request.delete(GATEWAY + "/credentials/" + name, { headers });
        expect.soft(deleted.status(), "finally 清理测试供应商").toBe(200);
      }
    }
  });
}
