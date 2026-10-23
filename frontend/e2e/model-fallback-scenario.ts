import { expect, test } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";
/** 用途：注册独立场景「per-model fallback editor and data plane」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC091() {


test("per-model fallback editor and data plane", async ({ page }) => {
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  const suffix = Date.now().toString();
  const names = ["fallback-primary-", "fallback-backup-", "fallback-third-"].map((name) => name + suffix);
  const ids = names.map((name) => "dep-" + name);
  let key = "";
  let templateID = "";
  try {
    for (let i = 0; i < names.length; i++) {
      const result = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: names[i],
          litellm_params: {
            model: i === 0 ? "e2e-fallback-500-" + suffix : "custom/fallback-good-" + i,
            api_base: UPSTREAM,
            api_key: "sk-fake",
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
      expect(result.status(), await result.text()).toBe(200);
    }
    await stableGoto(page, "/models-and-endpoints");
    await page.getByLabel("搜索模型").fill(names[0]);
    const group = page.getByRole("region", { name: "公开模型 " + names[0], exact: true });
    await group.getByRole("button", { name: "配置回退", exact: true }).click();
    const editor = page.getByRole("dialog", { name: names[0] + " 回退配置", exact: true });
    await expect(editor).toBeVisible();
    // 未保存关闭不改变策略，Escape 后焦点回到卡片入口；再打开开始完整配置。
    await page.keyboard.press("Escape");
    await expect(editor).toBeHidden();
    await expect(group.getByRole("button", { name: "配置回退", exact: true })).toBeFocused();
    await expect(group).toContainText("未配置回退");
    await group.getByRole("button", { name: "配置回退", exact: true }).click();
    await expect(editor.getByRole("status")).toHaveCount(0);
    for (const [category, targets] of [
      ["通用错误", [names[1], names[2]]],
      ["上下文超限", [names[2]]],
      ["内容策略错误", [names[1]]],
    ] as const) {
      for (let i = 0; i < targets.length; i++) {
        await editor.getByRole("button", { name: "添加" + category + "回退", exact: true }).click();
        await editor.getByLabel(category + "目标 " + (i + 1), { exact: true }).selectOption(targets[i]);
      }
    }
    await editor.getByRole("button", { name: "通用错误下移 1", exact: true }).click();
    await expect(editor.getByLabel("通用错误目标 1", { exact: true })).toHaveValue(names[2]);
    await editor.getByRole("button", { name: "通用错误上移 2", exact: true }).click();
    const saved = page.waitForResponse(
      (r) => new URL(r.url()).pathname === "/model/fallback" && r.request().method() === "PUT",
    );
    await editor.getByRole("button", { name: "保存回退", exact: true }).click();
    expect((await saved).status()).toBe(200);
    await expect(editor).toBeHidden();
    await page.reload();
    await page.getByLabel("搜索模型").fill(names[0]);
    await expect(group).toContainText("通用错误：" + names[1] + " → " + names[2]);
    const titleBox = await group.getByRole("heading", { name: names[0], exact: true }).boundingBox();
    const buttonBox = await group.getByRole("button", { name: "配置回退", exact: true }).boundingBox();
    expect(buttonBox!.x).toBeGreaterThan(titleBox!.x + titleBox!.width);
    expect(buttonBox!.x - titleBox!.x - titleBox!.width).toBeLessThan(100);
    expect(Math.abs(buttonBox!.y - titleBox!.y)).toBeLessThan(12);
    const persisted = await (await page.request.get(GATEWAY + "/model/groups?search=" + names[0], { headers })).json();
    expect(persisted.data[0].fallback_policy).toEqual({
      fallbacks: [names[1], names[2]],
      context_window_fallbacks: [names[2]],
      content_policy_fallbacks: [names[1]],
    });
    await page.screenshot({ path: "../.e2e/fallback-validation/model-card.png", fullPage: true });
    const teams = await (await page.request.get(GATEWAY + "/v2/team/list?page=1&page_size=500", { headers })).json();
    const team = teams.teams.find((row: any) => row.team_alias === "e2e-fixture-team");
    const jwt = (await page.context().cookies()).find((c) => c.name === "token")!.value;
    const userID = JSON.parse(Buffer.from(jwt.split(".")[1], "base64url").toString()).user_id;
    // 此场景逐次验证三类回退和错误码，冷却另有专用用例；独立模板避免前轮失败污染后轮。
    const template = await page.request.post(GATEWAY + "/route_template/new", { headers, data: {
      name: "fallback-isolation-" + suffix,
      body: { model_routes: [], routing_groups: [], fallbacks: [], context_window_fallbacks: [], content_policy_fallbacks: [],
        retry_policy: { max_attempts: 1, timeout_seconds: 60, failure_threshold: 0, cooldown_seconds: 0 } },
    } });
    expect(template.status(), await template.text()).toBe(200);
    templateID = (await template.json()).id;
    const minted = await page.request.post(GATEWAY + "/key/generate", {
      headers,
      data: { key_alias: "fallback-" + suffix, key_type: "llm_api", team_id: team.team_id, user_id: userID, route_template_id: templateID },
    });
    expect(minted.status(), await minted.text()).toBe(200);
    key = (await minted.json()).key;
    let round = 0;
    /** call 经真实网关验证回退响应、流式终结及非零账单；参数为入口路径、预期目标序号和流标记，返回完成后的 Promise<void>。每轮使用独立英文正文避免响应缓存，接口或账单不符由 Playwright 报错；调用记录随场景私有 schema 清理。 */
    const call = async (path: string, target: number, stream = false) => {
      const result = await page.request.post(GATEWAY + path, {
        headers: { Authorization: "Bearer " + key },
        // 每次正文独立，避免缓存命中让本应验证真实回退和计费的调用得到零费用。
        data: { model: names[0], messages: [{ role: "user", content: "Verify fallback round " + (++round) + " " + path + target + stream }], stream },
      });
      expect(result.status(), await result.text()).toBe(200);
      if (stream) expect(await result.text()).toContain("[DONE]");
      else {
        const doc = await result.json();
        expect(doc.model).toBe(path.startsWith("/bypass") ? "custom/fallback-good-" + target : names[0]);
      }
      const callID = result.headers()["x-litellm-call-id"];
      await expect
        .poll(async () => {
          const row = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
          return row.ok() ? Number((await row.json()).spend) : 0;
        })
        .toBeGreaterThan(0);
    };
    await call("/v1/chat/completions", 1);
    await call("/bypass/openai/v1/chat/completions", 1);
    await call("/v1/chat/completions", 1, true);
    // 显式禁用只作用于当前请求，下一次正常调用仍使用持久化回退策略。
    for (const path of ["/v1/chat/completions", "/bypass/openai/v1/chat/completions"]) {
      const disabled = await page.request.post(GATEWAY + path, {
        headers: { Authorization: "Bearer " + key },
        data: { model: names[0], messages: [{ role: "user", content: "request-disable" }], disable_fallbacks: true },
      });
      expect(disabled.status()).toBe(500);
    }
    await call("/v1/chat/completions", 1);
    for (const [kind, target] of [
      ["context", 2],
      ["content", 1],
    ] as const) {
      expect(
        (
          await page.request.post(GATEWAY + "/model/update", {
            headers,
            data: { id: ids[0], litellm_params: { model: "e2e-fallback-" + kind + "-" + suffix } },
          })
        ).status(),
      ).toBe(200);
      await call("/bypass/openai/v1/chat/completions", target);
    }
    // 仅配置上下文回退时，中间目标服务错误仍继续后续目标，验证真实原生执行链。
    expect(
      (
        await page.request.put(GATEWAY + "/model/fallback", {
          headers,
          data: { model_name: names[0], policy: { context_window_fallbacks: [names[1], names[2]] } },
        })
      ).status(),
    ).toBe(200);
    for (const [id, model] of [
      [ids[0], "e2e-fallback-context-" + suffix],
      [ids[1], "e2e-fallback-500-" + suffix],
    ]) {
      expect(
        (
          await page.request.post(GATEWAY + "/model/update", { headers, data: { id, litellm_params: { model } } })
        ).status(),
      ).toBe(200);
    }
    await call("/bypass/openai/v1/chat/completions", 2);
    expect(
      (
        await page.request.post(GATEWAY + "/model/update", {
          headers,
          data: { id: ids[1], litellm_params: { model: "custom/fallback-good-1" } },
        })
      ).status(),
    ).toBe(200);
    expect(
      (
        await page.request.put(GATEWAY + "/model/fallback", {
          headers,
          data: { model_name: names[0], policy: persisted.data[0].fallback_policy },
        })
      ).status(),
    ).toBe(200);
    // 普通请求错误必须保留原状态，不使用通用回退隐藏无效输入。
    expect(
      (
        await page.request.post(GATEWAY + "/model/update", {
          headers,
          data: { id: ids[0], litellm_params: { model: "e2e-fallback-normal-" + suffix } },
        })
      ).status(),
    ).toBe(200);
    const bad = await page.request.post(GATEWAY + "/v1/chat/completions", {
      headers: { Authorization: "Bearer " + key },
      data: { model: names[0], messages: [{ role: "user", content: "normal error" }] },
    });
    expect(bad.status()).toBe(400);
    expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: ids[1] } })).status()).toBe(409);
    await group.getByRole("button", { name: "配置回退", exact: true }).click();
    await expect(editor.getByLabel("通用错误目标 1", { exact: true })).toHaveValue(names[1]);
    await expect(editor.getByRole("status")).toHaveCount(0);
    await page.screenshot({
      path: "../.e2e/fallback-validation/modal-verification/model-dialog.png",
      fullPage: true,
      animations: "disabled",
    });
    await editor.getByRole("button", { name: "通用错误删除 2", exact: true }).click();
    await expect(editor.getByLabel("通用错误目标 2", { exact: true })).toHaveCount(0);
    await editor.getByRole("button", { name: "清空回退", exact: true }).click();
    const cleared = page.waitForResponse(
      (r) => new URL(r.url()).pathname === "/model/fallback" && r.request().method() === "PUT",
    );
    await editor.getByRole("button", { name: "保存回退", exact: true }).click();
    expect((await cleared).status()).toBe(200);
    await expect(editor).toBeHidden();
    await expect(group).toContainText("未配置回退");
    expect(
      (await (await page.request.get(GATEWAY + "/model/groups?search=" + names[0], { headers })).json()).data[0]
        .fallback_policy,
    ).toEqual({ fallbacks: null, context_window_fallbacks: null, content_policy_fallbacks: null });
    expect(
      (
        await page.request.post(GATEWAY + "/model/update", {
          headers,
          data: { id: ids[0], litellm_params: { model: "e2e-fallback-500-" + suffix } },
        })
      ).status(),
    ).toBe(200);
    for (const path of ["/v1/chat/completions", "/bypass/openai/v1/chat/completions"]) {
      const clearedCall = await page.request.post(GATEWAY + path, {
        headers: { Authorization: "Bearer " + key },
        data: { model: names[0], messages: [{ role: "user", content: "cleared-policy" }] },
      });
      expect(clearedCall.status()).toBe(500);
    }
  } finally {
    for (const name of names)
      await page.request.put(GATEWAY + "/model/fallback", { headers, data: { model_name: name, policy: {} } });
    for (const id of ids) await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } });
    if (key) await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } });
    if (templateID) await page.request.post(GATEWAY + "/route_template/" + templateID + "/delete", { headers, data: {} });
  }
});
}
/** 用途：注册独立场景「fallback modal rejects cycles and preserves independent policies - desktop」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC092() {
const viewport: {height:number;name:string;width:number} = {"name":"desktop","width":1440,"height":900};

test("fallback modal rejects cycles and preserves independent policies - desktop", async ({ page }) => {
    await loginAdmin(page);
    // 登录辅助函数验证展开侧栏，登录后切换窄屏，回退流程仍完整运行于目标视口。
    await page.setViewportSize(viewport);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const names = ["modal-a-", "modal-b-", "modal-c-"].map((prefix) => prefix + viewport.name + "-" + Date.now());
    const ids = names.map((name) => "dep-" + name);
    try {
      for (let i = 0; i < names.length; i++) {
        const created = await page.request.post(GATEWAY + "/model/new", {
          headers,
          data: {
            model_name: names[i],
            litellm_params: {
              model: "custom/modal-good-" + i,
              api_base: UPSTREAM,
              api_key: "sk-fake",
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
        expect(created.status(), await created.text()).toBe(200);
      }
      await stableGoto(page, "/models-and-endpoints");
      /** 打开指定卡片的真实弹窗并等待目录读取；参数为模型序号，返回弹窗Locator，不写入任何数据。 */
      const open = async (index: number) => {
        await page.getByLabel("搜索模型").fill(names[index]);
        await page
          .getByRole("region", { name: "公开模型 " + names[index], exact: true })
          .getByRole("button", { name: "配置回退", exact: true })
          .click();
        const dialog = page.getByRole("dialog", { name: names[index] + " 回退配置", exact: true });
        await expect(dialog).toBeVisible();
        await expect(dialog.getByRole("status")).toHaveCount(0);
        const bounds = await dialog.boundingBox();
        expect(bounds!.x).toBeGreaterThanOrEqual(0);
        expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(viewport.width);
        expect(bounds!.y).toBeGreaterThanOrEqual(0);
        expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height);
        return dialog;
      };
      /** 保存当前真实弹窗并等待后端响应；参数为预期状态，返回无，失败保留表单供调用方验证。 */
      const save = async (status: number) => {
        const response = page.waitForResponse(
          (r) => new URL(r.url()).pathname === "/model/fallback" && r.request().method() === "PUT",
        );
        await page.getByRole("dialog").getByRole("button", { name: "保存回退", exact: true }).click();
        expect((await response).status()).toBe(status);
        if (status === 200) await expect(page.getByRole("dialog")).toHaveCount(0);
      };
      /** 通过真实目录接口读取指定模型策略；参数为序号，返回持久化策略，无服务器副作用。 */
      const read = async (index: number) => {
        const result = await page.request.get(GATEWAY + "/model/groups?search=" + names[index], { headers });
        expect(result.status()).toBe(200);
        return (await result.json()).data.find((row: any) => row.model_name === names[index]).fallback_policy;
      };
      for (const index of [0, 2]) {
        const dialog = await open(index);
        await dialog.getByRole("button", { name: "添加通用错误回退", exact: true }).click();
        await expect(dialog.getByRole("button", { name: "保存回退", exact: true })).toBeDisabled();
        await dialog.getByLabel("通用错误目标 1", { exact: true }).selectOption(names[1]);
        await save(200);
      }
      const primary = await read(0),
        independent = await read(2);
      const cancelled = await open(2);
      let writes = 0;
      /** 统计取消期间的回退写入请求；参数为浏览器请求，返回无，仅修改本地计数，随后移除监听器。 */
      const observe = (request: import("@playwright/test").Request) => {
        if (new URL(request.url()).pathname === "/model/fallback" && request.method() === "PUT") writes++;
      };
      page.on("request", observe);
      await cancelled.getByRole("button", { name: "清空回退", exact: true }).click();
      await cancelled.getByRole("button", { name: "取消", exact: true }).click();
      await expect(cancelled).toBeHidden();
      expect(await read(2)).toEqual(independent);
      expect(writes).toBe(0);
      page.off("request", observe);
      const cycle = await open(1);
      await cycle.getByRole("button", { name: "添加内容策略错误回退", exact: true }).click();
      await cycle.getByLabel("内容策略错误目标 1", { exact: true }).selectOption(names[0]);
      await save(400);
      await expect(cycle.getByRole("alert")).toContainText("cycle");
      await expect(cycle.getByLabel("内容策略错误目标 1", { exact: true })).toHaveValue(names[0]);
      expect(await read(1)).toEqual({
        fallbacks: null,
        context_window_fallbacks: null,
        content_policy_fallbacks: null,
      });
      expect(await read(0)).toEqual(primary);
      expect(await read(2)).toEqual(independent);
      await cycle.getByRole("button", { name: "清空回退", exact: true }).click();
      await save(200);
      await expect(
        page
          .getByRole("region", { name: "公开模型 " + names[1], exact: true })
          .getByRole("button", { name: "配置回退", exact: true }),
      ).toBeFocused();
      expect(await read(1)).toEqual({
        fallbacks: null,
        context_window_fallbacks: null,
        content_policy_fallbacks: null,
      });
    } finally {
      for (const name of names) {
        const result = await page.request.put(GATEWAY + "/model/fallback", {
          headers,
          data: { model_name: name, policy: {} },
        });
        expect([200, 404]).toContain(result.status());
      }
      for (const id of ids) {
        const result = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } });
        expect([200, 404]).toContain(result.status());
      }
    }
  });
}
/** 用途：注册独立场景「fallback modal rejects cycles and preserves independent policies - mobile」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC093() {
const viewport: {height:number;name:string;width:number} = {"name":"mobile","width":390,"height":844};

test("fallback modal rejects cycles and preserves independent policies - mobile", async ({ page }) => {
    await loginAdmin(page);
    // 登录辅助函数验证展开侧栏，登录后切换窄屏，回退流程仍完整运行于目标视口。
    await page.setViewportSize(viewport);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const names = ["modal-a-", "modal-b-", "modal-c-"].map((prefix) => prefix + viewport.name + "-" + Date.now());
    const ids = names.map((name) => "dep-" + name);
    try {
      for (let i = 0; i < names.length; i++) {
        const created = await page.request.post(GATEWAY + "/model/new", {
          headers,
          data: {
            model_name: names[i],
            litellm_params: {
              model: "custom/modal-good-" + i,
              api_base: UPSTREAM,
              api_key: "sk-fake",
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
        expect(created.status(), await created.text()).toBe(200);
      }
      await stableGoto(page, "/models-and-endpoints");
      /** 打开指定卡片的真实弹窗并等待目录读取；参数为模型序号，返回弹窗Locator，不写入任何数据。 */
      const open = async (index: number) => {
        await page.getByLabel("搜索模型").fill(names[index]);
        await page
          .getByRole("region", { name: "公开模型 " + names[index], exact: true })
          .getByRole("button", { name: "配置回退", exact: true })
          .click();
        const dialog = page.getByRole("dialog", { name: names[index] + " 回退配置", exact: true });
        await expect(dialog).toBeVisible();
        await expect(dialog.getByRole("status")).toHaveCount(0);
        const bounds = await dialog.boundingBox();
        expect(bounds!.x).toBeGreaterThanOrEqual(0);
        expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(viewport.width);
        expect(bounds!.y).toBeGreaterThanOrEqual(0);
        expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height);
        return dialog;
      };
      /** 保存当前真实弹窗并等待后端响应；参数为预期状态，返回无，失败保留表单供调用方验证。 */
      const save = async (status: number) => {
        const response = page.waitForResponse(
          (r) => new URL(r.url()).pathname === "/model/fallback" && r.request().method() === "PUT",
        );
        await page.getByRole("dialog").getByRole("button", { name: "保存回退", exact: true }).click();
        expect((await response).status()).toBe(status);
        if (status === 200) await expect(page.getByRole("dialog")).toHaveCount(0);
      };
      /** 通过真实目录接口读取指定模型策略；参数为序号，返回持久化策略，无服务器副作用。 */
      const read = async (index: number) => {
        const result = await page.request.get(GATEWAY + "/model/groups?search=" + names[index], { headers });
        expect(result.status()).toBe(200);
        return (await result.json()).data.find((row: any) => row.model_name === names[index]).fallback_policy;
      };
      for (const index of [0, 2]) {
        const dialog = await open(index);
        await dialog.getByRole("button", { name: "添加通用错误回退", exact: true }).click();
        await expect(dialog.getByRole("button", { name: "保存回退", exact: true })).toBeDisabled();
        await dialog.getByLabel("通用错误目标 1", { exact: true }).selectOption(names[1]);
        await save(200);
      }
      const primary = await read(0),
        independent = await read(2);
      const cancelled = await open(2);
      let writes = 0;
      /** 统计取消期间的回退写入请求；参数为浏览器请求，返回无，仅修改本地计数，随后移除监听器。 */
      const observe = (request: import("@playwright/test").Request) => {
        if (new URL(request.url()).pathname === "/model/fallback" && request.method() === "PUT") writes++;
      };
      page.on("request", observe);
      await cancelled.getByRole("button", { name: "清空回退", exact: true }).click();
      await cancelled.getByRole("button", { name: "取消", exact: true }).click();
      await expect(cancelled).toBeHidden();
      expect(await read(2)).toEqual(independent);
      expect(writes).toBe(0);
      page.off("request", observe);
      const cycle = await open(1);
      await cycle.getByRole("button", { name: "添加内容策略错误回退", exact: true }).click();
      await cycle.getByLabel("内容策略错误目标 1", { exact: true }).selectOption(names[0]);
      await save(400);
      await expect(cycle.getByRole("alert")).toContainText("cycle");
      await expect(cycle.getByLabel("内容策略错误目标 1", { exact: true })).toHaveValue(names[0]);
      expect(await read(1)).toEqual({
        fallbacks: null,
        context_window_fallbacks: null,
        content_policy_fallbacks: null,
      });
      expect(await read(0)).toEqual(primary);
      expect(await read(2)).toEqual(independent);
      await cycle.getByRole("button", { name: "清空回退", exact: true }).click();
      await save(200);
      await expect(
        page
          .getByRole("region", { name: "公开模型 " + names[1], exact: true })
          .getByRole("button", { name: "配置回退", exact: true }),
      ).toBeFocused();
      expect(await read(1)).toEqual({
        fallbacks: null,
        context_window_fallbacks: null,
        content_policy_fallbacks: null,
      });
    } finally {
      for (const name of names) {
        const result = await page.request.put(GATEWAY + "/model/fallback", {
          headers,
          data: { model_name: name, policy: {} },
        });
        expect([200, 404]).toContain(result.status());
      }
      for (const id of ids) {
        const result = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } });
        expect([200, 404]).toContain(result.status());
      }
    }
  });
}
