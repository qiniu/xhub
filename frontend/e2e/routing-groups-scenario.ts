import { test, expect } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, watchGateway, t } from "./helpers";
/** 用途：注册独立场景「complete routing template drives real group routing and fallbacks」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC123() {
  test("complete routing template drives real group routing and fallbacks", async ({ page }) => {
    test.setTimeout(120_000);
    const guard = watchGateway(page);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const suffix = Date.now().toString(),
      name = "template-" + suffix,
      group = "group-" + suffix;
    const models = ["primary-" + suffix, "backup-" + suffix],
      ids = models.map((m) => "dep-" + m),
      upstreams = ["e2e-fallback-500-" + suffix, "good-" + suffix];
    let key = "",
      templateID = "";
    try {
      for (let i = 0; i < 2; i++) {
        const r = await page.request.post(GATEWAY + "/model/new", {
          headers,
          data: {
            model_name: models[i],
            litellm_params: {
              model: upstreams[i],
              api_base: UPSTREAM,
              api_key: "sk-fake",
              deployment_id: ids[i],
              custom_llm_provider: "custom",
              input_cost_per_token: 0.000001,
              output_cost_per_token: 0.000002,
            },
            model_info: {
              id: ids[i],
              transport: "bypass_openai_chat",
              endpoint_types: ["chat", "bypass:openai-chat"],
              pricing_source: "manual",
            },
          },
        });
        expect(r.status(), await r.text()).toBe(200);
      }
      await stableGoto(page, "/route-templates");
      await page.getByRole("button", { name: /新建模板|New template/, exact: true }).first().click();
      const editor = page.getByRole("region", { name: /新建模板|New template/ });
      await editor.getByLabel(/^(名称|Name)$/).fill(name);
      await expect(editor.getByText("设置模板默认策略")).toHaveCount(0);
      await editor.getByLabel("公开模型", { exact: true }).selectOption(models[1]);
      await editor.getByRole("button", { name: "添加模型规则" }).click();
      await editor.getByLabel(models[1] + " 路由逻辑").selectOption("least-busy");
      await editor.getByRole("tab", { name: "路由组", exact: true }).click();
      await editor.getByRole("button", { name: "创建路由组", exact: true }).click();
      let dialog = page.getByRole("dialog", { name: "创建路由组" });
      await expect(dialog.getByRole("button", { name: "保存到模板" })).toBeDisabled();
      await dialog.getByLabel("路由组名称").fill(group);
      await dialog.getByLabel("搜索成员模型").fill(models[0]);
      await dialog.getByLabel("成员模型 " + models[0], { exact: true }).check();
      await dialog.getByLabel("路由组策略").selectOption("traffic-split");
      await dialog.getByLabel("路由组部署 " + ids[0] + " 权重", { exact: true }).fill("3");
      await page.screenshot({ path: "../.e2e/routing-templates/group-dialog.png", fullPage: true });
      await dialog.getByRole("button", { name: "保存到模板" }).click();
      await expect(dialog).toHaveCount(0);
      await editor.getByRole("button", { name: "展开路由组 " + group, exact: true }).click();
      await expect(editor.getByText('"model": "' + group + '"', { exact: false })).toBeVisible();
      await editor.getByRole("tab", { name: "故障转移", exact: true }).click();
      await editor.getByRole("button", { name: "添加故障转移" }).click();
      dialog = page.getByRole("dialog", { name: "配置模板故障转移" });
      await dialog.getByLabel("故障转移主模型").selectOption(group);
      await dialog.getByRole("button", { name: "添加回退目标" }).click();
      await dialog.getByLabel("回退目标 1").selectOption(models[1]);
      await page.screenshot({ path: "../.e2e/routing-templates/fallback-dialog.png", fullPage: true });
      await dialog.getByRole("button", { name: "保存到模板" }).click();
      await editor.getByRole("tab", { name: "JSON", exact: true }).click();
      const json = editor.getByRole("textbox", { name: "JSON", exact: true });
      const body = JSON.parse(await json.inputValue());
      expect(body.routing_strategy).toBeUndefined();
      expect(body.routing_groups[0].group_name).toBe(group);
      expect(body.fallbacks).toEqual([{ [group]: [models[1]] }]);
      await json.fill("{");
      await expect(editor.getByRole("button", { name: "保存模板" })).toBeDisabled();
      await json.fill(JSON.stringify({ ...body, routing_strategy: "simple-shuffle" }));
      await expect(editor.getByRole("button", { name: "保存模板" })).toBeDisabled();
      body.retry_policy.max_attempts = 2;
      body.retry_policy.failure_threshold = 0;
      await json.fill(JSON.stringify(body, null, 2));
      const saved = page.waitForResponse(
        (r) => new URL(r.url()).pathname === "/route_template/new" && r.request().method() === "POST",
      );
      await editor.getByRole("button", { name: "保存模板" }).click();
      const response = await saved;
      expect(response.status(), await response.text()).toBe(200);
      templateID = (await response.json()).id;
      const teams = await (await page.request.get(GATEWAY + "/v2/team/list?page=1&page_size=500", { headers })).json();
      const team = teams.teams.find((r: any) => r.team_alias === "e2e-fixture-team");
      const jwt = (await page.context().cookies()).find((c) => c.name === "token")!.value;
      const userID = JSON.parse(Buffer.from(jwt.split(".")[1], "base64url").toString()).user_id;
      const minted = await page.request.post(GATEWAY + "/key/generate", {
        headers,
        data: { key_alias: name, key_type: "llm_api", team_id: team.team_id, user_id: userID },
      });
      expect(minted.status(), await minted.text()).toBe(200);
      const mintedBody = await minted.json();
      key = mintedBody.key;
      const binding = await page.request.post(GATEWAY + "/route_template/binding", {
        headers,
        data: { scope: "key", scope_id: mintedBody.token_id || mintedBody.key_id, route_template_id: templateID },
      });
      expect(binding.status(), await binding.text()).toBe(200);
      // 适配入口的组回退成功响应可缓存；命中前仍复核实际备用成员权限。
      const cachedRequest = { model: group, session_id: "group-cache-" + suffix, messages: [{ role: "user", content: "Reply only OK. group cache" }] };
      for (let round = 0; round < 2; round++) {
        const beforeCache = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
        const result = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + key }, data: cachedRequest });
        expect(result.status(), await result.text()).toBe(200);
        expect(result.headers()["cache_hit"] === "true").toBe(round > 0);
        expect((await result.json()).model).toBe(group);
        const afterCache = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
        expect(afterCache - beforeCache).toBe(round === 0 ? 3 : 0);
        await expect.poll(async () => {
          const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + result.headers()["x-litellm-call-id"], { headers });
          if (!detail.ok()) return false;
          const log = await detail.json();
          return round === 0 ? Number(log.spend) > 0 : Number(log.spend) === 0 && log.cache_hit === "true";
        }).toBe(true);
      }
      expect((await page.request.post(GATEWAY + "/key/update", { headers, data: { key, models: [group, models[0]] } })).status()).toBe(200);
      const beforeDeniedCache = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
      const deniedCache = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + key }, data: cachedRequest });
      expect(deniedCache.status(), await deniedCache.text()).toBe(401);
      expect((await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests).toBe(beforeDeniedCache);
      expect((await page.request.post(GATEWAY + "/key/update", { headers, data: { key, models: [] } })).status()).toBe(200);
      // 单工作进程和私有上游的计数器区分首次重试/回退与后续粘性复用。
      const before = await (await page.request.get(UPSTREAM + "/e2e/request-count")).json();
      const call = await page.request.post(GATEWAY + "/bypass/openai/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key, "X-Session-Id": "session-" + suffix },
        data: { model: group, messages: [{ role: "user", content: "real group fallback" }] },
      });
      expect(call.status(), await call.text()).toBe(200);
      expect((await call.json()).model).toBe(upstreams[1]);
      expect(call.headers()["x-session-id"]).toBe("session-" + suffix);
      const afterFirst = await (await page.request.get(UPSTREAM + "/e2e/request-count")).json();
      expect(afterFirst.requests - before.requests, "首次应为主部署两次加备用一次").toBe(3);
      // 同一会话下一轮复用已成功的回退目标，撤销回退链后必须立即失效。
      const reused = await page.request.post(GATEWAY + "/bypass/openai/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key, "X-Session-Id": "session-" + suffix },
        data: { model: group, messages: [{ role: "user", content: "Continue the same session" }] },
      });
      expect(reused.status(), await reused.text()).toBe(200);
      expect((await reused.json()).model).toBe(upstreams[1]);
      const afterReused = await (await page.request.get(UPSTREAM + "/e2e/request-count")).json();
      expect(afterReused.requests - afterFirst.requests, "下一轮只调用成功备用部署").toBe(1);
      for (const result of [call, reused]) {
        await expect.poll(async () => {
          const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + result.headers()["x-litellm-call-id"], { headers });
          if (!detail.ok()) return null;
          const log = await detail.json();
          return [log.model, log.session_id, log.session_source];
        }).toEqual([group, "session-" + suffix, "explicit"]);
      }
      const callID = call.headers()["x-litellm-call-id"];
      expect(callID).toBeTruthy();
      await expect
        .poll(async () => {
          const log = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
          return log.ok() ? Number((await log.json()).spend) : 0;
        })
        .toBeGreaterThan(0);
      await page.reload();
      const row = page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) });
      await row.getByRole("button", { name: /^(编辑|Edit)$/ }).click();
      const edited = page.getByRole("region", { name: /^(编辑|Edit)$/ });
      await edited.getByRole("tab", { name: "路由组", exact: true }).click();
      await edited.getByRole("button", { name: "编辑路由组 " + group, exact: true }).click();
      dialog = page.getByRole("dialog", { name: "编辑路由组" });
      await expect(dialog.getByLabel("路由组名称")).toBeDisabled();
      await expect(dialog.getByLabel("路由组部署 " + ids[0] + " 权重")).toHaveValue("3");
      await dialog.getByRole("button", { name: "取消" }).click();
      await edited.getByRole("tab", { name: "故障转移", exact: true }).click();
      await edited.getByRole("button", { name: "编辑故障转移 " + group + " 通用错误", exact: true }).click();
      dialog = page.getByRole("dialog", { name: "配置模板故障转移" });
      await dialog.getByRole("button", { name: "清空回退" }).click();
      await dialog.getByRole("button", { name: "保存到模板" }).click();
      const updated = page.waitForResponse(
        (r) => new URL(r.url()).pathname === "/route_template/" + templateID + "/update",
      );
      await edited.getByRole("button", { name: "保存模板" }).click();
      expect((await updated).status()).toBe(200);
      const disabled = await page.request.post(GATEWAY + "/bypass/openai/v1/chat/completions", {
        headers: { Authorization: "Bearer " + key, "X-Session-Id": "session-" + suffix },
        data: { model: group, messages: [{ role: "user", content: "disabled" }] },
      });
      expect(disabled.status()).toBeGreaterThanOrEqual(400);
      const afterDisabled = await (await page.request.get(UPSTREAM + "/e2e/request-count")).json();
      expect(afterDisabled.requests - afterReused.requests, "撤销回退后只重试主部署两次").toBe(2);
      await page.getByRole("button", { name: "完整 JSON 配置指南" }).click();
      const guide = page.getByRole("dialog", { name: "完整 JSON 配置指南" });
      await expect(guide.getByRole("cell", { name: "routing_groups", exact: true })).toBeVisible();
      await guide.getByRole("tab", { name: "可导入示例", exact: true }).click();
      await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
      await guide.getByRole("button", { name: "复制示例", exact: true }).click();
      await expect(guide.getByRole("status")).toHaveText("已复制");
      expect(JSON.parse(await page.evaluate(() => navigator.clipboard.readText())).routing_groups).toHaveLength(1);
      await page.evaluate(() => {
        Object.defineProperty(navigator, "clipboard", {
          configurable: true,
          value: { writeText: () => Promise.reject(new Error("denied for E2E")) },
        });
      });
      await guide.getByRole("button", { name: "复制示例", exact: true }).click();
      await expect(guide.getByRole("status")).toHaveText("复制失败，请手动选择代码复制。");
      await page.keyboard.press("Escape");
      await expect(guide).toHaveCount(0);
      await page.getByRole("button", { name: "English", exact: true }).click();
      await row.getByRole("button", { name: "Edit", exact: true }).click();
      const englishEditor = page.getByRole("region", { name: "Edit", exact: true });
      await expect(englishEditor.getByLabel("Name", { exact: true })).toHaveValue(name);
      await expect(englishEditor.getByRole("tab", { name: "Load balancing", exact: true })).toBeVisible();
      await englishEditor.getByRole("tab", { name: "Routing groups", exact: true }).click();
      await englishEditor.getByRole("button", { name: "Edit routing group " + group, exact: true }).click();
      const englishDialog = page.getByRole("dialog", { name: "Edit routing group", exact: true });
      await expect(englishDialog.getByLabel("Routing group name", { exact: true })).toHaveValue(group);
      await expect(
        englishDialog.getByLabel("Group deployment " + ids[0] + " weight", { exact: true }),
      ).toHaveValue("3");
      await englishDialog.getByRole("button", { name: "Cancel", exact: true }).click();
      await englishEditor.getByRole("button", { name: "Expand routing group " + group, exact: true }).click();
      await expect(englishEditor.getByText('"content": "Hello"', { exact: false }).first()).toBeVisible();
      await englishEditor.getByRole("button", { name: "Cancel", exact: true }).click();
      await page.getByRole("button", { name: "Complete JSON configuration guide", exact: true }).click();
      const englishGuide = page.getByRole("dialog", { name: "Complete JSON configuration guide" });
      await englishGuide.getByRole("tab", { name: "Importable example", exact: true }).click();
      await expect(englishGuide.getByRole("button", { name: "Copy example", exact: true })).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(englishGuide).toHaveCount(0);
      await page.getByRole("button", { name: "中文", exact: true }).click();
      await page.getByRole("button", { name: "完整 JSON 配置指南", exact: true }).click();
      await guide.getByRole("tab", { name: "可导入示例", exact: true }).click();
      await expect(guide.getByRole("button", { name: "复制示例", exact: true })).toBeVisible();
      await page.screenshot({ path: "../.e2e/routing-templates/guide-desktop.png", fullPage: true });
      await page.setViewportSize({ width: 390, height: 844 });
      expect((await guide.boundingBox())!.width).toBeLessThanOrEqual(390);
      await page.screenshot({ path: "../.e2e/routing-templates/guide-mobile.png", fullPage: true });
      await page.keyboard.press("Escape");
      // 解除本测试密钥绑定后从模板列表删除，验证真实页面到后台的删除流程。
      expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })).ok()).toBeTruthy();
      key = "";
      await page.setViewportSize({ width: 1280, height: 720 });
      await page.reload();
      const deletionRow = page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) });
      await deletionRow.getByRole("button", { name: name + " 的操作" }).click();
      await page.getByRole("menuitem", { name: t("pages.routeTemplates.delete"), exact: true }).click();
      const deletion = page.waitForResponse(
        (r) => new URL(r.url()).pathname === "/route_template/" + templateID + "/delete",
      );
      await page
        .getByRole("dialog")
        .getByRole("button", { name: t("pages.routeTemplates.delete"), exact: true })
        .click();
      expect((await deletion).status()).toBe(200);
      await expect(deletionRow).toHaveCount(0);
      templateID = "";
      await guard.assertOk();
    } finally {
      if (key)
        expect(
          (await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })).ok(),
        ).toBeTruthy();
      if (templateID)
        expect(
          (await page.request.post(GATEWAY + "/route_template/" + templateID + "/delete", { headers, data: {} })).ok(),
        ).toBeTruthy();
      for (const id of ids) await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } });
    }
  });
}
