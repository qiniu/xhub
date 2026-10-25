import { expect, test, type Page } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { startProbeFixture, type ProbeFixture } from "./probe-fixture";
import { matchesGatewayResponse } from "./gateway-response.cjs";

type Domain = "prompts" | "search_tools";
type Operation = "创建" | "更新" | "删除" | "测试" | "测试连接";
type ProbeContext = { page: Page; locale: Locale; domain: Domain; fixture: ProbeFixture; model: string; id: string; headers: Record<string, string> };

/** 填写可执行配置；ctx包含隔离浏览器/上游/模型，name为本轮唯一名称，返回无。
 * 保留用户变量原文；仅在测试专属配置中使用固定fixture Key，不使用真实账号或商业上游。
 */
async function fillProbe(ctx: ProbeContext, name: string) {
  const { page, locale, domain, fixture, model } = ctx;
  const tr = (key: string) => translate(locale, key);
  await page.getByLabel(tr("probes.name"), { exact: true }).fill(name);
  if (domain === "prompts") {
    await page.getByLabel(tr("probes.model"), { exact: true }).fill(model);
    await page.getByRole("textbox", { name: tr("probes.messages"), exact: true }).fill(JSON.stringify([{ role: "user", content: "Write a greeting for {{name}}." }]));
    await page.getByRole("textbox", { name: tr("probes.variables"), exact: true }).fill(JSON.stringify({ name: "Original 用户输入" }));
  } else {
    await page.getByRole("combobox", { name: tr("probes.provider"), exact: true }).selectOption("tavily");
    await page.getByLabel(tr("probes.apiBase"), { exact: true }).fill(fixture.url + "/search");
    await page.getByLabel(tr("probes.apiKey"), { exact: true }).fill("fixture-probe-key");
    await page.getByLabel(tr("probes.query"), { exact: true }).fill("Find the official API documentation.");
  }
}

/** 等待真实管理请求并检查成功；ctx确定页面与资源，method/action指定本次操作，返回服务器对象。
 * 只观察直连或 /gw 代理的真实网络，不拦截或伪造响应；请求失败使测试失败，资源由外层finally清理。
 */
async function saveProbe(ctx: ProbeContext, method: string, action: string) {
  const endpoint = `/${ctx.domain}${ctx.id ? "/" + encodeURIComponent(ctx.id) : ""}`;
  const pending = ctx.page.waitForResponse(response => matchesGatewayResponse(response.url(), response.request().method(), endpoint, method));
  await ctx.page.getByRole("button", { name: translate(ctx.locale, action), exact: true }).click();
  const response = await pending;
  expect(response.status(), "配置操作必须由真实后台完成").toBe(200);
  return response.json();
}

/** 以保存配置实际执行并验证上下游证据；ctx包含本地HTTP记录，参数无真实Key，返回无。
 * 检查原始查询/渲染文本、输出、用量；再注入失败检查界面清除旧成功结果，最后恢复后再次成功。
 */
async function exerciseProbe(ctx: ProbeContext) {
  const { page, locale, domain, fixture } = ctx;
  const tr = (key: string) => translate(locale, key);
  const path = `/${domain}/${domain === "prompts" ? "test" : "test_connection"}`;
  const before = fixture.requests.length;
  const pending = page.waitForResponse(response => matchesGatewayResponse(response.url(), response.request().method(), path, "POST"));
  await page.getByRole("button", { name: tr("probes.test"), exact: true }).click();
  const response = await pending;
  expect(response.status(), "连接/提示词测试应返回真实执行结果").toBe(200);
  expect(fixture.requests.length, "每次成功必须实际触达上游一次").toBe(before + 1);
  const outbound = fixture.requests[before];
  expect(outbound.authorization).toBe("Bearer fixture-probe-key");
  const result = page.getByTestId("probe-result");
  if (domain === "prompts") {
    expect(outbound.path).toBe("/v1/chat/completions");
    expect(outbound.body.messages).toEqual([{ role: "user", content: "Write an updated greeting for Original 用户输入." }]);
    await expect(result).toContainText("Rendered: Write an updated greeting for Original 用户输入.");
    const callID = response.headers()["x-litellm-call-id"];
    expect(callID, "提示词执行应关联真实日志和费用").toBeTruthy();
    await expect.poll(async () => {
      const usage = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers: ctx.headers });
      if (usage.status() !== 200) return null;
      const row = await usage.json();
      return { model: row.model, input: row.prompt_tokens, output: row.completion_tokens, spend: Number(row.spend) };
    }).toEqual({ model: ctx.model, input: 8, output: 2, spend: 0.000012 });
    // 未提供必需变量时不能发上游请求，也不能继续显示上一次成功结果。
    await page.getByRole("textbox", { name: tr("probes.variables"), exact: true }).fill("{}");
    const invalid = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), path, "POST"));
    await page.getByRole("button", { name: tr("probes.test"), exact: true }).click();
    expect((await invalid).status()).toBe(400);
    expect(fixture.requests.length).toBe(before + 1);
    await expect(page.getByRole("main").getByRole("alert")).toContainText(translate(locale, "probes.errors.promptMissingVariable", { name: "name" }));
    await expect(result).toHaveCount(0);
    await page.getByRole("textbox", { name: tr("probes.variables"), exact: true }).fill(JSON.stringify({ name: "Original 用户输入" }));
  } else {
    expect(outbound.path).toBe("/search");
    expect(outbound.body.query).toBe("Find the official API documentation.");
    await expect(result).toContainText("Found: Find the official API documentation.");
    await expect(page.getByText(translate(locale, "probes.resultsCount", { count: 1 }), { exact: true })).toBeVisible();
  }
  const failureCalls = fixture.requests.length;
  fixture.failure = true;
  const rejected = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), path, "POST"));
  await page.getByRole("button", { name: tr("probes.test"), exact: true }).click();
  expect((await rejected).status()).toBe(domain === "prompts" ? 403 : 429);
  expect(fixture.requests.length, "失败必须来自本次真实上游调用，不能使用旧缓存").toBe(failureCalls + 1);
  await expect(page.getByRole("main").getByRole("alert")).toBeVisible();
  await expect(result).toHaveCount(0);
  await expect(page.getByRole("heading", { name: tr(domain === "prompts" ? "probes.promptsTitle" : "probes.searchTitle"), exact: true })).toBeVisible();
  const recoveryCalls = fixture.requests.length;
  fixture.failure = false;
  const recovered = page.waitForResponse(r => matchesGatewayResponse(r.url(), r.request().method(), path, "POST"));
  await page.getByRole("button", { name: tr("probes.test"), exact: true }).click();
  expect((await recovered).status()).toBe(200);
  expect(fixture.requests.length, "恢复后必须重新调用而不是复用旧成功").toBe(recoveryCalls + 1);
  await expect(result).toBeVisible();
}

/** 运行独立目录用户流程；domain/operation/locale确定页面及验证范围。
 * 前置真实登录、隔离数据库、本机协议上游；页面创建/编辑/测试/删除，finally清理资源、模型及HTTP监听，不依赖其他测试。
 */
async function runProbeFlow(page: Page, domain: Domain, operation: Operation, locale: Locale) {
  const fixture = await startProbeFixture();
  let modelID = "";
  const ctx: ProbeContext = { page, domain, locale, fixture, model: "e2e-probe-" + domain + "-" + Date.now(), id: "", headers: {} };
  const tr = (key: string) => translate(locale, key);
  try {
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    ctx.headers = { Authorization: "Bearer " + await sessionBearer(page) };
    if (domain === "prompts") {
      const created = await page.request.post(GATEWAY + "/model/new", { headers: ctx.headers, data: {
        model_name: ctx.model,
        litellm_params: { model: "fixture-prompt", custom_llm_provider: "openai", api_base: fixture.url + "/v1", api_key: "fixture-probe-key", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
      } });
      expect(created.status()).toBe(200);
      modelID = (await created.json()).model_info.id;
    }
    await stableGoto(page, domain === "prompts" ? "/prompts" : "/search-tools");
    const name = ctx.model + "-config";
    await fillProbe(ctx, name);
    const created = await saveProbe(ctx, "POST", "probes.create");
    ctx.id = created[domain === "prompts" ? "prompt_id" : "search_tool_id"];
    expect(ctx.id).toBeTruthy();
    expect(JSON.stringify(created)).not.toContain("fixture-probe-key");
    const detail = await page.request.get(`${GATEWAY}/${domain}/${encodeURIComponent(ctx.id)}`, { headers: ctx.headers });
    expect(detail.status()).toBe(200);
    expect((await detail.json()).name).toBe(name);
    if (operation !== "创建") {
      await page.getByLabel(tr("probes.name"), { exact: true }).fill(name + "-updated");
      if (domain === "prompts") {
        // 编辑模板正文而非只改显示名称，刷新后真实推理必须采用新模板。
        await page.getByRole("textbox", { name: tr("probes.messages"), exact: true }).fill(JSON.stringify([{ role: "user", content: "Write an updated greeting for {{name}}." }]));
      }
      await saveProbe(ctx, domain === "prompts" ? "PATCH" : "PUT", "probes.save");
      await page.reload();
      await page.getByRole("combobox", { name: tr("probes.record"), exact: true }).selectOption(ctx.id);
      await expect(page.getByLabel(tr("probes.name"), { exact: true })).toHaveValue(name + "-updated");
      if (domain === "search_tools") await expect(page.getByLabel(tr("probes.apiKey"), { exact: true })).toHaveValue("");
      if (domain === "prompts") {
        const messages = page.getByRole("textbox", { name: tr("probes.messages"), exact: true });
        expect(JSON.parse(await messages.inputValue())).toEqual([{ role: "user", content: "Write an updated greeting for {{name}}." }]);
        const rejected = await page.request.patch(`${GATEWAY}/prompts/${ctx.id}`, { headers: { ...ctx.headers, "Content-Type": "application/json" }, data: '{"messages":' });
        expect(rejected.status(), "非法编辑必须失败且不得覆盖保存模板").toBe(400);
      }
    }
    if (operation === "测试" || operation === "测试连接") {
      if (domain === "prompts") await page.getByRole("textbox", { name: tr("probes.variables"), exact: true }).fill(JSON.stringify({ name: "Original 用户输入" }));
      else await page.getByLabel(tr("probes.query"), { exact: true }).fill("Find the official API documentation.");
      await exerciseProbe(ctx);
    }
    // 即便此用例针对创建或更新，删除仍走真实页面和接口，结束时证明资源确实不存在。
    await saveProbe(ctx, "DELETE", "probes.delete");
    const missing = await page.request.get(`${GATEWAY}/${domain}/${encodeURIComponent(ctx.id)}`, { headers: ctx.headers });
    expect(missing.status()).toBe(404);
    ctx.id = "";
  } finally {
    test.setTimeout(150_000);
    try {
      if (ctx.id) {
        const deleted = await page.request.delete(`${GATEWAY}/${domain}/${encodeURIComponent(ctx.id)}`, { headers: ctx.headers });
        expect.soft(deleted.status(), "清理自建测试配置").toBe(200);
      }
    } finally {
      try {
        if (modelID) {
          const deleted = await page.request.post(GATEWAY + "/model/delete", { headers: ctx.headers, data: { id: modelID } });
          expect.soft(deleted.status(), "清理自建测试模型").toBe(200);
        }
      } finally { await fixture.close(); }
    }
  }
}

/** 替换既有通用表单场景的弱断言；route/action保留原登记名称，真实流程自行清理，无返回值。 */
export function registerProbeColumn(route: "/prompts" | "/search-tools", action: Operation) {
  test("通用页面 " + route + " " + action, async ({ page }) => {
    test.setTimeout(120_000);
    await runProbeFlow(page, route === "/prompts" ? "prompts" : "search_tools", action, "zh-CN");
  });
}

/** 注册英文完整探测回归；domain选择真实页面，流程验证中英文同一契约，独立资源finally清理。 */
export function registerProbeEnglish(domain: Domain) {
  test("real " + domain + " probe and upstream failure en", async ({ page }) => {
    test.setTimeout(120_000);
    await runProbeFlow(page, domain, "测试", "en");
  });
}
