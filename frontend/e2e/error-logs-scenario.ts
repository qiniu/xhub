import { expect, test, type APIRequestContext, type Page, type Request } from "@playwright/test";

import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";


const JSON_DIAGNOSTIC =
  "E2E upstream JSON diagnostic begin | " +
  "0123456789abcdef".repeat(128) +
  " | E2E upstream JSON diagnostic end";


/**
 * 创建仅属于当前隔离 E2E schema 的模型部署。
 * 参数：request 为浏览器请求客户端，headers 为管理员鉴权头，公开模型名与上游模型名用于隔离本次数据。
 * 返回值：后台生成的部署 ID，供 finally 精确删除。
 * 调用场景：错误日志流程分别创建失败和成功部署；创建失败会直接抛出且不会修改用户数据。
 */
async function createDeployment(
  request: APIRequestContext,
  headers: Record<string, string>,
  modelName: string,
  upstreamModel: string,
): Promise<string> {
  const response = await request.post(GATEWAY + "/model/new", {
    headers,
    data: {
      model_name: modelName,
      litellm_params: {
        model: upstreamModel,
        api_base: UPSTREAM,
        api_key: "sk-fake",
        custom_llm_provider: "custom",
        input_cost_per_token: 0.000001,
        output_cost_per_token: 0.000002,
      },
      model_info: { transport: "bypass_openai_chat", pricing_source: "manual" },
    },
  });
  expect(response.status(), await response.text()).toBe(200);
  return (await response.json()).model_info.id;
}


/**
 * 等待真实数据面请求进入隔离日志存储。
 * 参数：page 为当前浏览器页、headers 为管理员鉴权头、callId 为网关响应关联 ID。
 * 返回值：日志详情可读取后结束。
 * 调用场景：打开日志页前消除异步落库竞态；仅查询当前请求，不创建或覆盖任何数据。
 */
async function waitForStoredLog(page: Page, headers: Record<string, string>, callId: string): Promise<void> {
  await expect
    .poll(
      async () => {
        const response = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
        return response.status();
      },
      { message: `请求 ${callId} 应写入隔离日志存储` },
    )
    .toBe(200);
}


/**
 * 在当前日志页按请求 ID 定位并打开详情。
 * 参数：page 为日志页面，callId 为本测试真实网关请求 ID。
 * 返回值：详情抽屉可见后结束。
 * 调用场景：错误日志和普通日志 tab 共用；只操作可访问名称与当前测试行，不触碰其他日志。
 */
async function openLogDetails(page: Page, callId: string): Promise<void> {
  // 页签切换动画中两个面板可能同时可见，按选中页签的可访问名称定位，不能全页面找同名输入。
  const selected = page.getByRole("tab", { selected: true, name: /^(日志|Logs|错误日志|Error Logs)$/ });
  const panel = page.getByRole("tabpanel", { name: (await selected.innerText()).trim(), exact: true });
  const search = panel.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/);
  await search.fill(callId);
  const row = panel.getByRole("row").filter({ hasText: callId });
  await expect(row, `应在当前日志 tab 找到请求 ${callId}`).toBeVisible();
  await row.click();
  await expect(page.getByRole("dialog")).toBeVisible();
}


/**
 * 验证真实日志诊断默认折叠、展开内容与后台一致并可再次收起。
 * 参数为当前详情页、管理员头、真实调用 ID 和预期上游状态；返回验证完成的 Promise。
 * 用于成功及失败请求流程；只读取隔离日志并操作当前抽屉，日志由 schema 清理。
 */
async function verifyUpstreamCollapse(page: Page, headers: Record<string, string>, callId: string, status: number): Promise<void> {
  const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + callId, { headers });
  expect(detail.status()).toBe(200);
  const stored = await detail.json();
  expect(stored.api_base, "日志应保存调用时的上游基础地址").toBe(UPSTREAM);
  expect(stored.credential_name, "内联部署应使用脱敏地址标识连接").toBe(UPSTREAM);
  await expect(page.getByRole("dialog").getByText(UPSTREAM, { exact: true })).toHaveCount(2);
  await expect(page.getByRole("dialog").getByText(/^(凭证名称|Credential Name)$/)).toBeVisible();
  await expect(page.getByRole("dialog").getByText(translate("zh-CN", "API Base"), { exact: true }).or(page.getByRole("dialog").getByText(translate("en", "API Base"), { exact: true }))).toBeVisible();
  const upstream = stored.metadata.upstream_response;
  expect(upstream.status_code, "后台应保存真实上游 HTTP 状态").toBe(status);
  const region = page.getByRole("dialog").getByRole("region", { name: /^(上游响应|Upstream Response)$/ });
  const trigger = region.getByRole("button", { name: /^(上游响应|Upstream Response)$/ });
  const header = region.getByRole("heading", { name: /上游响应头|Upstream Response Headers/ });
  await expect(trigger, "打开日志详情时上游响应应默认折叠").toHaveAttribute("aria-expanded", "false");
  await expect(header).toBeHidden();
  await expect(region.getByRole("button", { name: /复制上游响应|Copy upstream response/ })).toBeVisible();
  await trigger.click();
  await expect(trigger).toHaveAttribute("aria-expanded", "true");
  await expect(header).toBeVisible();
  await expect(region.getByText(/^(HTTP 状态码|HTTP Status):/)).toContainText(String(status));
  await expect(region).toContainText(JSON.stringify(upstream.headers, null, 2));
  await expect(region).toContainText(JSON.stringify(upstream.billing_usage ?? {}, null, 2));
  if (upstream.usage_reported) {
    await expect(region).toContainText(JSON.stringify(upstream.usage, null, 2));
  } else {
    await expect(region).toContainText(/未上报|Not reported/);
  }
  await trigger.click();
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
  await expect(header).toBeHidden();
}
/** 验证真实错误列表的组合筛选和双语显示；参数是本例模型与调用 ID，返回完成信号。
 * 通过页面提交到真实网关并核对分页结果，零匹配也必须验证响应；只读查询，数据由场景 finally 清理。
 */
async function verifyLogFilters(page: Page, model: string, callId: string): Promise<void> {
  await page.getByRole("button", { name: "English", exact: true }).click();
  const refresh = page.getByRole("switch", { name: "Refresh every 15 seconds" });
  await refresh.uncheck();
  await page.getByRole("button", {name:/^Filters(?:\s*\d+)?$/}).click();
  const drawer = page.getByRole("dialog");
  await drawer.getByLabel("Model", {exact:true}).fill(model);
  await drawer.getByLabel("Error Message", {exact:true}).fill("e2e-json-terminal-cause");
  await drawer.getByPlaceholder("Select or type an error code").click();
  await page.getByRole("option",{name:"502 - Bad Gateway",exact:true}).click();
  const filtered = page.waitForResponse(r => r.url().includes("/spend/logs/ui?") && r.url().includes("error_code=502") && r.url().includes("error_message="));
  await drawer.getByRole("button", {name:"Apply Filters",exact:true}).click();
  const response=await filtered;
  expect(response.status()).toBe(200);
  const body=await response.json();
  expect(body.total).toBe(1);
  expect(body.data[0].request_id).toBe(callId);
  await expect(page.getByRole("row").filter({hasText:callId})).toBeVisible();
  await expect(page.getByRole("columnheader",{name:"Error summary"})).toBeVisible();
  await expect(page.getByRole("columnheader",{name:"Cost",exact:true})).toHaveCount(0);
  await page.screenshot({path:"../.e2e/logs-error-list.png",fullPage:true});

  await page.getByRole("button", {name:/^Filters(?:\s*\d+)?$/}).click();
  await drawer.getByLabel("Error Message", {exact:true}).fill("no-match-"+callId);
  const empty=page.waitForResponse(r=>r.url().includes("/spend/logs/ui?") && r.url().includes("no-match-"));
  await drawer.getByRole("button",{name:"Apply Filters",exact:true}).click();
  expect((await (await empty).json()).total).toBe(0);
  await expect(page.getByText("No matching requests",{exact:true})).toBeVisible();

  await page.getByRole("button",{name:/^Filters(?:\s*\d+)?$/}).click();
  // 语言切换保留用户草稿，且应用/重置/高级字段均同步翻译。
  await drawer.getByLabel("Error Message", {exact:true}).fill("用户原文");
  await drawer.getByRole("button",{name:"Apply Filters",exact:true}).click();
  await page.getByRole("button",{name:"中文",exact:true}).click();
  await page.getByRole("button",{name:/^筛选(?:\s*\d+)?$/}).click();
  await expect(drawer.getByLabel("错误消息",{exact:true})).toHaveValue("用户原文");
  await expect(drawer.getByText("高级筛选",{exact:true})).toBeVisible();
  await expect(drawer.getByRole("button",{name:/应用筛选|应用过滤/})).toBeVisible();
  await page.keyboard.press("Escape");
  await page.getByRole("button",{name:"English",exact:true}).click();
  await page.getByRole("button",{name:"Reset Filters",exact:true}).click();
  await expect(page.getByRole("row").filter({hasText:callId})).toBeVisible();
}

/** 验证日期到秒的真实查询和详情深链接；前置本例请求已落库。
 * 页面选择不含请求的历史窗口后应返回零，再从普通日志链接打开失败详情；只读操作，不修改日志。
 */
async function verifyLogDatesAndLink(page: Page, failedCallId: string): Promise<void> {
  await page.getByRole("button",{name:"Reset Filters",exact:true}).click();
  await page.getByRole("button",{name:"Last 24 Hours",exact:true}).click();
  await page.getByRole("button",{name:"Custom Range",exact:true}).click();
  await page.keyboard.press("Escape");
  await page.getByLabel("Start time",{exact:true}).fill("2020-01-01T00:00");
  const empty=page.waitForResponse(r=>r.url().includes("/spend/logs/ui?") && new URL(r.url()).searchParams.get("end_date")?.startsWith("2020-01-02") === true);
  await page.getByLabel("End time",{exact:true}).fill("2020-01-02T12:00");
  const response=await empty;
  expect(response.status()).toBe(200);
  expect((await response.json()).total).toBe(0);
  await expect(page.getByText("No records in this time range",{exact:true})).toBeVisible();
  const invalid=page.waitForResponse(r=>r.url().includes("/spend/logs/ui?") && new URL(r.url()).searchParams.get("start_date")?.startsWith("2021-") === true);
  await page.getByLabel("Start time",{exact:true}).fill("2021-01-01T12:00");
  expect((await invalid).status()).toBe(400);
  await expect(page.getByRole("alert").filter({hasText:"Logs could not be loaded"})).toBeVisible();
  const detailPath = "/spend/logs/ui/" + failedCallId;
  const detailRequests: Request[] = [];
  // 只统计浏览器页面发出的真实详情读取，忽略测试夹具的预检请求；完整打开/关闭后必须恰好一次。
  const observeDetail = (request: Request) => { if (new URL(request.url()).pathname === detailPath) detailRequests.push(request); };
  page.on("request", observeDetail);
  try {
    const detail = page.waitForResponse(r => new URL(r.url()).pathname === detailPath);
    await stableGoto(page, "/logs?log_id=" + failedCallId);
    const direct = await detail;
    expect(direct.status()).toBe(200);
    expect(new URL(direct.url()).searchParams.get("start_date") || "").toBe("");
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("alert")).toContainText("502");
    await expect(dialog.getByRole("region", { name: "Full Error Details" })).toContainText("e2e-json-terminal-cause");
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    expect(detailRequests.length, "深链接与抽屉应复用同一次完整正文读取").toBe(1);
  } finally { page.off("request", observeDetail); }
}

/** 在纽约夏令时重复小时选择最近一小时；参数为已登录页面及本例真实成功调用 ID。
 * 使用独立浏览器时区与固定 Date 验证实际网关收到 05:30–06:30 UTC，再恢复当前时间查到真实日志；finally 关闭上下文，数据仍由场景清理。
 */
async function verifyDSTPreset(page: Page, successCallId: string): Promise<void> {
  const browser = page.context().browser();
  expect(browser).not.toBeNull();
  const context = await browser!.newContext({ timezoneId: "America/New_York", baseURL: new URL(page.url()).origin,
    storageState: await page.context().storageState() });
  try {
    const dstPage = await context.newPage();
    await dstPage.clock.setFixedTime(new Date("2026-11-01T06:30:00Z"));
    await stableGoto(dstPage, "/logs");
    await dstPage.getByRole("button", { name: "English", exact: true }).click();
    await dstPage.getByRole("switch", { name: "Refresh every 15 seconds" }).uncheck();
    await dstPage.getByRole("button", { name: "Last 24 Hours", exact: true }).click();
    const responsePromise = dstPage.waitForResponse(response => {
      const url = new URL(response.url());
      return url.pathname === "/spend/logs/ui" && url.searchParams.get("end_date") === "2026-11-01 06:30:00"
        && url.searchParams.get("start_date") !== "2026-10-31 06:30:00";
    });
    await dstPage.getByRole("button", { name: "Last Hour", exact: true }).click();
    const response = await responsePromise;
    expect(response.status()).toBe(200);
    const params = new URL(response.url()).searchParams;
    expect(params.get("start_date")).toBe("2026-11-01 05:30:00");
    expect(params.get("end_date")).toBe("2026-11-01 06:30:00");
    const result = await response.json();
    expect(Array.isArray(result.data)).toBe(true);
    expect(typeof result.total).toBe("number");
    // 恢复实际时间后，同一浏览器预设仍能查询本例刚才通过真实数据面生成的日志。
    await dstPage.clock.setFixedTime(new Date());
    await dstPage.getByRole("tabpanel", { name: "Logs", exact: true }).getByPlaceholder(/Search logs by ID/).fill(successCallId);
    await expect(dstPage.getByRole("row").filter({ hasText: successCallId })).toBeVisible();
  } finally { await context.close(); }
}

/** 验证真实日志在桌面边界宽度的无障碍范围及手机展开、折叠操作；page 为浏览器，callId 为真实请求。
 * 通过深链接读取隔离后台日志并操作真实页面；finally 恢复视口及本地宽度偏好，日志由测试运行器清理。 */
async function verifyResponsiveLogDrawer(page: Page, callId: string): Promise<void> {
  const originalViewport = page.viewportSize();
  const priorWidth = await page.evaluate(() => localStorage.getItem("xhub.logDetails.widthPercent"));
  try {
    await page.setViewportSize({ width: 1030, height: 800 });
    await page.evaluate(() => localStorage.setItem("xhub.logDetails.widthPercent", "40"));
    await stableGoto(page, "/logs?log_id=" + encodeURIComponent(callId));
    const dialog = page.getByRole("dialog");
    const resizer = dialog.getByRole("separator", { name: /拖动或左右方向键调整详情宽度|Resize detail panel with drag or arrow keys/ });
    await expect(resizer).toBeVisible();
    await expect(resizer).toHaveAttribute("aria-valuemin", "63");
    await expect(resizer).toHaveAttribute("aria-valuenow", "63");
    await page.keyboard.press("Escape");

    await page.setViewportSize({ width: 390, height: 844 });
    await stableGoto(page, "/logs?log_id=" + encodeURIComponent(callId));
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("separator", { name: /拖动或左右方向键调整详情宽度|Resize detail panel with drag or arrow keys/ })).toHaveCount(0);
    const expand = dialog.getByRole("button", { name: /展开追踪侧栏|Expand trace sidebar/ });
    await expand.click();
    const collapse = dialog.getByRole("button", { name: /折叠跟踪侧边栏|Collapse trace sidebar/ });
    await expect(collapse).toBeVisible();
    // 使用真实点击检验命中测试；如果按钮被详情头或侧栏遮挡，Playwright 会报告拦截而非假通过。
    await collapse.click();
    await expect(expand).toBeVisible();
    await page.keyboard.press("Escape");
  } finally {
    await page.evaluate((value) => value === null ? localStorage.removeItem("xhub.logDetails.widthPercent") : localStorage.setItem("xhub.logDetails.widthPercent", value), priorWidth);
    if (originalViewport) await page.setViewportSize(originalViewport);
  }
}

/** 用途：注册独立场景「错误日志详情完整显示上游正文并可返回普通日志」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC020() {


test("错误日志详情完整显示上游正文并可返回普通日志", async ({ page }) => {
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  const suffix = Date.now().toString();
  const failedModel = "error-logs-failed-" + suffix;
  const successModel = "error-logs-success-" + suffix;
  const deploymentIds: string[] = [];
  const settings = await page.request.get(GATEWAY + "/config/list?config_type=general_settings", { headers });
  expect(settings.status()).toBe(200);
  const logging = (await settings.json()).find((field: any) => field.field_name === "store_prompts_in_spend_logs");

  try {
    // 正文详情是本场景的断言前置；显式配置，防止用例选择或执行顺序改变结果。
    const enabled = await page.request.post(GATEWAY + "/config/update", { headers,
      data: { general_settings: { store_prompts_in_spend_logs: true } } });
    expect(enabled.status()).toBe(200);
    deploymentIds.push(
      await createDeployment(page.request, headers, failedModel, "e2e-error-log-json-" + suffix),
      await createDeployment(page.request, headers, successModel, "e2e-error-log-success-" + suffix),
    );

    const failed = await page.request.post(GATEWAY + "/chat/completions", {
      headers,
      data: { model: failedModel, messages: [{ role: "user", content: "Record the full upstream error response." }] },
    });
    expect(failed.status(), await failed.text()).toBe(502);
    const failedCallId = failed.headers()["x-litellm-call-id"];
    expect(failedCallId, "失败响应应包含可关联日志的 call id").toBeTruthy();

    const succeeded = await page.request.post(GATEWAY + "/chat/completions", {
      headers,
      data: { model: successModel, messages: [{ role: "user", content: "Record a successful request log." }] },
    });
    expect(succeeded.status(), await succeeded.text()).toBe(200);
    expect((await succeeded.json()).choices[0].message.content).toBe("e2e-ok");
    const successCallId = succeeded.headers()["x-litellm-call-id"];
    expect(successCallId, "成功响应应包含可关联日志的 call id").toBeTruthy();

    await waitForStoredLog(page, headers, failedCallId);
    await waitForStoredLog(page, headers, successCallId);
    await stableGoto(page, "/logs");

    // 验证互斥筛选通过后台分页生效，普通日志不出现失败请求，错误日志不出现成功请求。
    const search = page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true });
    await search.fill(failedCallId);
    await expect(page.getByRole("row").filter({ hasText: failedCallId })).toHaveCount(0);
    await expect(page.getByTestId("pagination-range")).toHaveText(t("consoleDirectory.pagination.empty"));
    await page.getByRole("tab", { name: /错误日志|Error Logs/, exact: true }).click();
    await search.fill(successCallId);
    await expect(page.getByRole("row").filter({ hasText: successCallId })).toHaveCount(0);
    await expect(page.getByTestId("pagination-range")).toHaveText(t("consoleDirectory.pagination.empty"));
    await search.fill("");
    await verifyLogFilters(page, failedModel, failedCallId);
    await openLogDetails(page, failedCallId);
    const failedDialog = page.getByRole("dialog");
    await verifyUpstreamCollapse(page, headers, failedCallId, 502);
    await expect(failedDialog.getByRole("alert")).toContainText(/HTTP 状态码:\s*502|HTTP Status:\s*502/);
    const errorDetails = failedDialog.getByRole("region", {
      name: /完整错误详情|全部错误详情|Full Error Details/,
    });
    await expect(errorDetails.getByRole("button", { name: /复制错误详情|Copy error details/ })).toBeVisible();
    const errorBody = errorDetails.locator("pre");
    await expect(errorBody, "原始错误默认折叠，避免长正文占满界面").toBeHidden();
    await errorDetails.getByText(/^(展开完整错误|Expand full error)$/).click();
    await expect(errorBody).toBeVisible();
    await expect(errorBody).toContainText("E2E_UPSTREAM_502");
    await expect(errorBody).toContainText("e2e-upstream-json-request");
    await expect(errorBody).toContainText("e2e-json-terminal-cause");
    expect(await errorBody.textContent(), "完整错误详情的 pre 必须保留上游长诊断正文").toContain(JSON_DIAGNOSTIC);
    // 在正文断言通过后保存可视化证据，确保截图呈现的是已展开且内容完整的错误详情。
    await page.screenshot({ path: "../.e2e/error-logs-detail.png", fullPage: true });
    await page.keyboard.press("Escape");
    await expect(failedDialog).toBeHidden();

    await page.getByRole("tab", { name: /^(日志|Logs)$/, exact: true }).click();
    await openLogDetails(page, successCallId);
    const successDialog = page.getByRole("dialog");
    // 键盘调整、全屏和恢复均操作真实抽屉；完成后继续读取真实后台日志。
    const resizer = successDialog.getByRole("separator", { name: /拖动或左右方向键调整详情宽度|Resize detail panel with drag or arrow keys/ });
    await expect(resizer).toBeVisible();
    const widthBefore = (await successDialog.boundingBox())!.width;
    await resizer.focus();
    await page.keyboard.press("ArrowLeft");
    await expect.poll(async () => (await successDialog.boundingBox())!.width).toBeGreaterThan(widthBefore);
    await successDialog.getByRole("button", { name: /展开详情|Expand details/ }).click();
    await expect(successDialog.getByRole("button", { name: /恢复详情宽度|Restore details width/ })).toBeVisible();
    await successDialog.getByRole("button", { name: /恢复详情宽度|Restore details width/ }).click();
    // 真实成功请求落库后检查技术缩写，防止日志页面重新出现学位误译；schema 由全局清理回收。
    await expect(successDialog).not.toContainText("法学硕士");
    await verifyUpstreamCollapse(page, headers, successCallId, 200);
    await expect(successDialog).toContainText("e2e-ok");
    await expect(successDialog).toContainText(/成功|Success/);
    // 费用来自后台日志详情；保留真实小额费用，不能把浮点尾数直接显示给用户。
    const successDetail = await page.request.get(GATEWAY + "/spend/logs/ui/" + successCallId, { headers });
    expect(successDetail.status()).toBe(200);
    const successSpend = Number((await successDetail.json()).spend ?? 0);
    expect(successSpend, "成功请求必须有明确的正费用，不能跳过金额断言").toBeGreaterThan(0);
    {
      expect(successSpend).toBe(0.000012);
      const formattedSpend = "$0.000012";
      await expect(successDialog).toContainText(formattedSpend);
      await expect(successDialog).not.toContainText("0.008289600000000001");
    }
    await page.screenshot({ path: "../.e2e/upstream-response-collapsed.png", fullPage: true });
    await page.keyboard.press("Escape");
    // 关闭正文保存后发起真实请求：已记录的用量及上游证据仍可查看，原因未知不能展示伪配置结论。
    const disabled = await page.request.post(GATEWAY + "/config/update", { headers,
      data: { general_settings: { store_prompts_in_spend_logs: false } } });
    expect(disabled.status()).toBe(200);
    const noBody = await page.request.post(GATEWAY + "/chat/completions", {
      headers, data: { model: successModel, messages: [{ role: "user", content: "Verify the request summary remains available." }] },
    });
    expect(noBody.status()).toBe(200);
    const noBodyId = noBody.headers()["x-litellm-call-id"];
    expect(noBodyId).toBeTruthy();
    await waitForStoredLog(page, headers, noBodyId);
    const noBodyDetail = await page.request.get(GATEWAY + "/spend/logs/ui/" + noBodyId, { headers });
    const noBodyData = await noBodyDetail.json();
    expect(noBodyData.metadata.upstream_response.status_code).toBe(200);
    expect(Number(noBodyData.spend)).toBeGreaterThan(0);
    await openLogDetails(page, noBodyId);
    const noBodyDialog = page.getByRole("dialog");
    await expect(noBodyDialog.getByRole("note")).toContainText(/原因未记录|reason was not recorded/);
    await expect(noBodyDialog).not.toContainText("store_model_in_db");
    await verifyUpstreamCollapse(page, headers, noBodyId, 200);
    await page.keyboard.press("Escape");
    // 删除配置后仍读取调用时快照，切换语言不改变原始上游地址或用户请求正文。
    for (const id of deploymentIds) {
      const deleted = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } });
      expect(deleted.status(), await deleted.text()).toBe(200);
    }
    deploymentIds.length = 0;
    await page.getByRole("button", { name: "English", exact: true }).click();
    await openLogDetails(page, successCallId);
    await expect(page.getByRole("dialog").getByRole("separator", { name: translate("en", "logsUX.resizeDrawer") })).toBeVisible();
    await verifyUpstreamCollapse(page, headers, successCallId, 200);
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: translate("en", "language.zhCN"), exact: true }).click();
    await openLogDetails(page, successCallId);
    await verifyUpstreamCollapse(page, headers, successCallId, 200);
    await page.keyboard.press("Escape");
    await page.getByRole("button", {name:"English",exact:true}).click();
    await verifyLogDatesAndLink(page, failedCallId);
    await verifyDSTPreset(page, successCallId);
    await verifyResponsiveLogDrawer(page, successCallId);
  } finally {
    try {
      const restored = logging?.stored_in_db
        ? await page.request.post(GATEWAY + "/config/update", { headers,
          data: { general_settings: { store_prompts_in_spend_logs: logging.field_value } } })
        : await page.request.post(GATEWAY + "/config/field/delete", { headers,
          data: { config_type: "general_settings", field_name: "store_prompts_in_spend_logs" } });
      expect(restored.status()).toBe(200);
    } finally {
      for (const id of deploymentIds) {
        const deleted = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } });
        expect(deleted.status(), await deleted.text()).toBe(200);
      }
    }
  }
});
}
/** 用途：注册独立场景「退役接口不进入模型日志且真实推理错误继续可见」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC021() {


test("退役接口不进入模型日志且真实推理错误继续可见", async ({ page }) => {
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  const retiredIds: string[] = [];
  for (const path of ["/v1/agents/agent1", "/v1/tool/x"]) {
    const response = await page.request.get(GATEWAY + path, { headers });
    expect(response.status(), await response.text()).toBe(410);
    expect((await response.json()).error.type).toBe("removed");
    const id = response.headers()["x-litellm-call-id"];
    expect(id).toBeTruthy();
    retiredIds.push(id);
  }
  const rejected = await page.request.post(GATEWAY + "/v1/chat/completions", {
    headers, data: { model: "retired-control-missing-" + Date.now(), messages: [] },
  });
  expect(rejected.status()).toBeGreaterThanOrEqual(400);
  const callId = rejected.headers()["x-litellm-call-id"];
  await waitForStoredLog(page, headers, callId);
  await stableGoto(page, "/logs");
  const search = page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true });
  for (const tabName of [/^(日志|Logs)$/, /错误日志|Error Logs/]) {
    await page.getByRole("tab", { name: tabName, exact: true }).click();
    for (const id of retiredIds) {
      const detail = await page.request.get(GATEWAY + "/spend/logs/ui/" + id, { headers });
      expect(detail.status(), "退役请求不得写入真实日志存储").toBe(404);
      await search.fill(id);
      await expect(page.getByTestId("pagination-range")).toHaveText(t("consoleDirectory.pagination.empty"));
      await expect(page.getByRole("row").filter({ hasText: id })).toHaveCount(0);
    }
  }
  await openLogDetails(page, callId);
  const diagnostic = page.getByRole("dialog").getByRole("region", { name: /完整错误详情|全部错误详情|Full Error Details/ }).locator("pre");
  expect(await diagnostic.textContent()).toBe(await rejected.text());
});
}
/** 用途：注册独立场景「错误日志独立记录并显示诊断：text」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC022() {
const kind: string = "text";

test("错误日志独立记录并显示诊断：text", async ({ page }) => {
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const suffix = Date.now().toString();
    const model = "error-log-" + kind + "-" + suffix;
    let deployment: string | undefined;
    try {
      if (kind !== "rejected") {
        deployment = await createDeployment(page.request, headers, model, "e2e-error-log-" + kind + "-" + suffix);
      }
      const response = await page.request.post(GATEWAY + "/chat/completions", {
        headers, data: { model, messages: [{ role: "user", content: "Verify error diagnostics." }] },
      });
      expect(response.status()).toBeGreaterThanOrEqual(400);
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId).toBeTruthy();
      await waitForStoredLog(page, headers, callId);
      await stableGoto(page, "/logs");
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(callId);
      await expect(page.getByTestId("pagination-range")).toHaveText(t("consoleDirectory.pagination.empty"));
      await expect(page.getByRole("row").filter({ hasText: callId })).toHaveCount(0);
      await page.getByRole("tab", { name: /错误日志|Error Logs/, exact: true }).click();
      await openLogDetails(page, callId);
      const diagnostic = page.getByRole("dialog").getByRole("region", { name: /完整错误详情|全部错误详情|Full Error Details/ }).locator("pre");
      if (kind === "text") {
        expect(await diagnostic.textContent()).toBe("E2E upstream plain-text diagnostic begin | " + "fedcba9876543210".repeat(128) + " | E2E upstream plain-text diagnostic end");
      } else if (kind === "network") {
        await expect(diagnostic).toContainText(/EOF|connection|network/i);
      } else {
        expect(await diagnostic.textContent()).toBe(await response.text());
      }
    } finally {
      if (deployment) {
        const deleted = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: deployment } });
        expect(deleted.status(), await deleted.text()).toBe(200);
      }
    }
  });
}
/** 用途：注册独立场景「错误日志独立记录并显示诊断：network」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC023() {
const kind: string = "network";

test("错误日志独立记录并显示诊断：network", async ({ page }) => {
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const suffix = Date.now().toString();
    const model = "error-log-" + kind + "-" + suffix;
    let deployment: string | undefined;
    try {
      if (kind !== "rejected") {
        deployment = await createDeployment(page.request, headers, model, "e2e-error-log-" + kind + "-" + suffix);
      }
      const response = await page.request.post(GATEWAY + "/chat/completions", {
        headers, data: { model, messages: [{ role: "user", content: "Verify error diagnostics." }] },
      });
      expect(response.status()).toBeGreaterThanOrEqual(400);
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId).toBeTruthy();
      await waitForStoredLog(page, headers, callId);
      await stableGoto(page, "/logs");
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(callId);
      await expect(page.getByTestId("pagination-range")).toHaveText(t("consoleDirectory.pagination.empty"));
      await expect(page.getByRole("row").filter({ hasText: callId })).toHaveCount(0);
      await page.getByRole("tab", { name: /错误日志|Error Logs/, exact: true }).click();
      await openLogDetails(page, callId);
      const diagnostic = page.getByRole("dialog").getByRole("region", { name: /完整错误详情|全部错误详情|Full Error Details/ }).locator("pre");
      if (kind === "text") {
        expect(await diagnostic.textContent()).toBe("E2E upstream plain-text diagnostic begin | " + "fedcba9876543210".repeat(128) + " | E2E upstream plain-text diagnostic end");
      } else if (kind === "network") {
        await expect(diagnostic).toContainText(/EOF|connection|network/i);
      } else {
        expect(await diagnostic.textContent()).toBe(await response.text());
      }
    } finally {
      if (deployment) {
        const deleted = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: deployment } });
        expect(deleted.status(), await deleted.text()).toBe(200);
      }
    }
  });
}
/** 用途：注册独立场景「错误日志独立记录并显示诊断：rejected」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC024() {
const kind: string = "rejected";

test("错误日志独立记录并显示诊断：rejected", async ({ page }) => {
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const suffix = Date.now().toString();
    const model = "error-log-" + kind + "-" + suffix;
    let deployment: string | undefined;
    try {
      if (kind !== "rejected") {
        deployment = await createDeployment(page.request, headers, model, "e2e-error-log-" + kind + "-" + suffix);
      }
      const response = await page.request.post(GATEWAY + "/chat/completions", {
        headers, data: { model, messages: [{ role: "user", content: "Verify error diagnostics." }] },
      });
      expect(response.status()).toBeGreaterThanOrEqual(400);
      const callId = response.headers()["x-litellm-call-id"];
      expect(callId).toBeTruthy();
      await waitForStoredLog(page, headers, callId);
      await stableGoto(page, "/logs");
      await page.getByPlaceholder(/按 ID 搜索日志|Search logs by ID/).filter({ visible: true }).fill(callId);
      await expect(page.getByTestId("pagination-range")).toHaveText(t("consoleDirectory.pagination.empty"));
      await expect(page.getByRole("row").filter({ hasText: callId })).toHaveCount(0);
      await page.getByRole("tab", { name: /错误日志|Error Logs/, exact: true }).click();
      await openLogDetails(page, callId);
      const diagnostic = page.getByRole("dialog").getByRole("region", { name: /完整错误详情|全部错误详情|Full Error Details/ }).locator("pre");
      if (kind === "text") {
        expect(await diagnostic.textContent()).toBe("E2E upstream plain-text diagnostic begin | " + "fedcba9876543210".repeat(128) + " | E2E upstream plain-text diagnostic end");
      } else if (kind === "network") {
        await expect(diagnostic).toContainText(/EOF|connection|network/i);
      } else {
        expect(await diagnostic.textContent()).toBe(await response.text());
      }
    } finally {
      if (deployment) {
        const deleted = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: deployment } });
        expect(deleted.status(), await deleted.text()).toBe(200);
      }
    }
  });
}
