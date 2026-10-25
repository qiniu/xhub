import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { once } from "node:events";
import { chromium, expect } from "@playwright/test";
import { serve } from "../src/serve.mjs";
import { checkSite } from "./regression.mjs";

const directory = new URL("../dist/", import.meta.url).pathname;
const reportDirectory = new URL("../test-results/", import.meta.url).pathname;
await mkdir(reportDirectory, { recursive: true });
const server = serve({ directory, port: 0, mount: "/xhub/" });
await once(server, "listening");
const base = `http://127.0.0.1:${server.address().port}/xhub/`;
const results = [];
const regression = await checkSite(directory);
let browser;

/** 运行一个真实浏览器流程并收集结果；参数为名称与异步操作，异常记录后继续，浏览器上下文由主流程最终清理。 */
async function scenario(name, action) {
  try { await action(); results.push({ name, passed: true }); console.log(`PASS ${name}`); }
  catch (error) { results.push({ name, passed: false, error: error.stack }); console.error(`FAIL ${name}: ${error.message}`); }
}

try {
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, permissions: ["clipboard-read", "clipboard-write"] });
  const page = await context.newPage();
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  // 目的：迁移后中英文首页、安装正文及源码入口均指向七牛仓库；前置真实构建与 HTTP 服务，刷新验证后关闭上下文，不写业务数据。
  await scenario("七牛仓库迁移 → 双语安装与源码链接 → 刷新", async () => {
    for (const [home, setup] of [["", "getting-started.zh-CN"], ["en/", "getting-started"]]) {
      await page.goto(base + home);
      for (const link of await page.getByRole("link", { name: /^GitHub/ }).all()) {
        await expect(link).toHaveAttribute("href", "https://github.com/qiniu/xhub");
      }
      assert.ok((await page.locator("main").innerText()).includes("git clone https://github.com/qiniu/xhub.git"), "首页安装命令必须来自新仓库");
      await page.goto(base + "docs/" + setup + ".html");
      await page.reload();
      assert.ok((await page.getByRole("article").innerText()).includes("git clone https://github.com/qiniu/xhub.git"), "双语安装说明必须来自新仓库");
      await expect(page.locator(".doc-end").getByRole("link").first()).toHaveAttribute("href", `https://github.com/qiniu/xhub/blob/main/docs/${setup}.md`);
    }
    const missing = await page.request.get(base + "404.html");
    assert.equal(missing.status(), 200, "发布的 404 页面必须可读取");
    assert.ok((await missing.text()).includes('href="https://qiniu.github.io/xhub/"'), "404 返回入口必须指向七牛站点");
  });
  // 目的：双语性能说明切换和刷新后仍提供已跟踪的压测指南，报告仅显示本地路径；前置真实构建服务器，无外部请求和业务写入，最终关闭上下文。
  await scenario("性能说明 → 双语切换 → 压测指南与本地报告边界", async () => {
    await page.goto(base + "docs/development/zh-CN/api-performance-design.html");
    for (const [locale, label, boundary] of [["zh-CN", "基准测试指南", "被 Git 忽略的本地产物"], ["en", "benchmark guide", "local artifacts ignored by Git"]]) {
      if (locale === "en") await page.getByRole("link", { name: "Switch to English", exact: true }).click();
      await page.waitForURL(`**/development/${locale}/api-performance-design.html`);
      await page.reload();
      const response = await page.request.get(page.url());
      assert.equal(response.status(), 200, `${locale} 性能文档必须可达`);
      const article = page.getByRole("article");
      await expect(article.getByRole("link", { name: label, exact: true })).toHaveAttribute("href", "https://github.com/qiniu/xhub/blob/main/benchmarks/README.md");
      assert.ok((await article.innerText()).includes(boundary), `${locale} 必须说明报告不随仓库发布`);
      assert.ok((await article.innerText()).includes("benchmarks/reports/staircase/report.md"));
      assert.equal(await article.locator('a[href*="benchmarks/reports/"]').count(), 0, `${locale} 不能提供无效报告链接`);
    }
  });
  // 目的：双语入口可实际导航且共享翻译源不造成重复目录；前置真实构建服务器，只读页面，最终关闭上下文。
  await scenario("整体文档入口 → 开发指南 → 双语目录无重复", async () => {
    for (const [lang, home, label, locale] of [["zh", "", "开发指南", "zh-CN"], ["en", "en/", "Developer guide", "en"]]) {
      await page.goto(base + home);
      assert.equal(await page.locator(".doc-card").count(), 4);
      await page.locator(".documentation-section").getByRole("link", { name: new RegExp(label) }).click();
      await page.waitForURL(`**/development/${locale}/README.html`);
      const links = await page.locator(".docs-sidebar .nav-group a").evaluateAll((items) => items.map((item) => item.href));
      assert.equal(new Set(links).size, links.length, "目录不能出现重复目标地址");
      assert.ok(!links.some((value) => value.includes(`/center/${lang}/`)), "开发侧栏不能混入产品指南");
      assert.ok(!links.some((value) => value.includes("api.generated.html")), "开发侧栏不能混入生成字段表");
      await expect(page.getByRole("navigation").getByRole("link", { name: lang === "zh" ? "API 参考" : "API reference", exact: true })).toHaveAttribute("href", `../../../reference/${lang}/index.html`);
      await page.screenshot({ path: `${reportDirectory}/docs-navigation-${lang}.png` });
    }
  });
  // 目的：新增对账路由通过真实网站双语索引可发现，说明和接口契约可读取；前置当前构建与HTTP服务，刷新后检查，无业务写入，最终关闭浏览器。
  await scenario("处理器索引 → 双语人工对账指南 → 刷新", async () => {
    for (const [locale, label] of [["zh-CN", "未知费用的人工对账"], ["en", "Manual reconciliation of uncertain charges"]]) {
      await page.goto(base + "docs/development/" + locale + "/handler-index.html");
      if (locale === "zh-CN") {
        const text = await page.getByRole("article").innerText();
        assert.ok(text.includes("GET /budget/reservations") && text.includes("BudgetReconcile"), "源码索引必须含新增对账接口和处理器");
      }
      await page.getByRole("article").getByRole("link", { name: label, exact: true }).click();
      await page.waitForURL("**/development/" + locale + "/budget-reconciliation.html");
      await page.reload();
      const text = await page.getByRole("article").innerText();
      for (const value of ["GET /budget/reservations", "POST /budget/reservations/{reservation_id}/reconcile", "verified_cost_usd", "expected_fingerprint", "attempt"]) {
        assert.ok(text.includes(value), locale + " 缺少可读核对契约：" + value);
      }
    }
  });
  // 目的：修正文案必须在双语真实页面可见；前置当前构建和服务器，检查模型匿名参数、默认价格源与系统 CA，再刷新；只读，最终关闭浏览器。
  await scenario("当前源码契约 → 双语模型参数、安装默认值与 CA", async () => {
    for (const [locale, home, setup, ignored] of [["zh-CN", "", "getting-started.zh-CN", "匿名发现忽略此参数"], ["en", "en/", "getting-started", "Ignored for anonymous discovery"]]) {
      await page.goto(base + home);
      assert.match(await page.locator("body").innerText(), /XHUB_CA_CERT_FILE/);
      await page.goto(base + "docs/" + setup + ".html");
      assert.ok((await page.getByRole("article").innerText()).includes("https://api.modelink.ai/v1/market/models"));
      assert.match(await page.getByRole("article").innerText(), /make e2e-offline/);
      await page.goto(base + "docs/api/" + locale + "/models.html");
      const text = await page.getByRole("article").innerText();
      assert.ok(text.includes(ignored), "匿名请求参数边界必须可见");
      assert.ok(text.includes("1677610602") && text.includes("openai"), "标准元信息必须与代码一致");
      await page.reload();
      assert.ok((await page.getByRole("article").innerText()).includes(ignored));
    }
  });
  // 前置为实际构建产物，验证旧地址、锚点、中英同主题切换与英文导航；只读网站，浏览器上下文最终清理。
  await scenario("开发文档语言目录 → 地址重定向 → 同主题切换 → API", async () => {
    await page.goto(base + "docs/development/routing.html#main");
    await page.waitForURL("**/development/zh-CN/routing.html#main");
    await page.getByRole("link", { name: "Switch to English", exact: true }).click();
    await page.waitForURL("**/development/en/routing.html");
    assert.equal(await page.locator("html").getAttribute("lang"), "en");
    assert.doesNotMatch(await page.locator(".docs-sidebar").innerText(), /[\u4e00-\u9fff]/);
    await page.screenshot({ path: reportDirectory + "/development-english.png", fullPage: true });
    await page.getByRole("navigation").getByRole("link", { name: "API reference", exact: true }).click();
    await page.getByRole("heading", { name: "API reference", exact: true }).waitFor();
    await page.getByRole("link", { name: "简体中文", exact: true }).click();
    await page.getByRole("heading", { name: "API 参考", exact: true }).waitFor();
  });
  await scenario("构建回归：文档、资源、搜索页面和锚点均可达", async () => { assert.deepEqual(regression.errors, []); assert.ok(regression.indexedDocuments >= 100); });
  // 目的：从真实帮助进入供应商操作及代码指南，验证双语、英文示例、源码链接和刷新；前置实际构建与 HTTP 索引，无上游调用，浏览器最终清理。
  await scenario("供应商帮助 → NewAPI/Suno → 双语接入指南 → 源码", async () => {
    await page.goto(base + "docs/center/zh/help/models.html");
    await page.getByRole("heading", { name: "供应商与模型部署", exact: true }).waitFor();
    await expect(page.getByRole("img", { name: "中文模型供应商连接表单" })).toBeVisible();
    assert.ok(await page.getByRole("img", { name: "中文模型供应商连接表单" }).evaluate((img) => img.complete && img.naturalWidth > 0), "中文供应商截图必须加载");
    assert.ok(await page.getByRole("img", { name: "中文模型部署表单" }).evaluate((img) => img.complete && img.naturalWidth > 0), "中文部署截图必须加载");
    assert.match(await page.getByRole("article").innerText(), /NewAPI[\s\S]*Suno/);
    await page.getByRole("link", { name: "Switch to English", exact: true }).click();
    assert.match(await page.getByRole("article").innerText(), /no Suno executor/);
    await expect(page.getByRole("img", { name: "English provider connection form" })).toBeVisible();
    assert.ok(await page.getByRole("img", { name: "English provider connection form" }).evaluate((img) => img.complete && img.naturalWidth > 0), "英文供应商截图必须加载");
    assert.ok(await page.getByRole("img", { name: "English model deployment form" }).evaluate((img) => img.complete && img.naturalWidth > 0), "英文部署截图必须加载");
    await page.goto(base + "docs/development/zh-CN/provider-catalog.html");
    await page.getByRole("heading", { name: "添加模型供应商与开发新协议", exact: true }).waitFor();
    await page.getByRole("heading", { name: "自己添加一个 provider", exact: true }).waitFor();
    assert.equal(await page.getByRole("heading", { name: "config.yaml 只负责启动，不用手填 model_list", exact: true }).count(), 0);
    assert.match(await page.getByRole("article").innerText(), /internal\/provider\/all\/all.go/);
    assert.doesNotMatch(await page.getByRole("article").innerText(), /config\.yaml|model_list: \[\]/);
    assert.match(await page.getByRole("article").innerText(), /registry\.RegisterTransport/);
    assert.match(await page.getByRole("article").innerText(), /模型供应商/);
    const examples = await page.locator("article pre:not(:has(code.language-go))").allTextContents();
    assert.ok(await page.locator("article code.language-go .hljs-keyword").count() > 0, "Go 示例必须真正语法高亮");
    assert.ok((await page.locator("article code.language-go").allTextContents()).some((value) => value.includes("registry.RegisterTransport(contract.Transport{")), "必须显示完整注册示例");
    assert.ok(examples.some((value) => value.includes("Reply with the word ready.")));
    assert.ok(examples.every((value) => !/[\u4e00-\u9fff]/.test(value)), "请求和配置示例应保持英文");
    assert.ok(examples.every((value) => !value.includes("model_list:") && !value.includes("os.environ/NEWAPI_KEY")), "接入指南不得引导手写部署与内联认证");
    await page.getByRole("link", { name: "Switch to English", exact: true }).click();
    await page.getByRole("heading", { name: "Add model providers and implement new protocols", exact: true }).waitFor();
    await page.getByRole("heading", { name: "Add your own provider", exact: true }).waitFor();
    assert.equal(await page.getByRole("heading", { name: "config.yaml is for startup, not manual model_list editing", exact: true }).count(), 0);
    await page.reload();
    await page.getByRole("link", { name: "provider source guide", exact: true }).click();
    assert.match(await page.getByRole("article").innerText(), /No new provider package/);
    await page.screenshot({ path: reportDirectory + "/provider-source-guide.png", fullPage: true });
  });
  // 目的：侧栏不重复全局分类入口；前置真实网站与索引，从帮助经主导航进入 API，切换双语并刷新，目录、正文及搜索仍可用；只读页面，最终关闭上下文。
  await scenario("同步文档中心 → 帮助 → 主导航 API → 双语精简侧栏", async () => {
    await page.goto(base + "docs/");
    await page.getByRole("heading", {name: "欢迎使用 XHub", exact: true}).waitFor();
    await page.getByRole("link", {name: "常见问题与故障排查", exact: true}).click();
    assert.match(await page.locator("article").innerText(), /401/);
    await page.getByRole("navigation").getByRole("link", {name: "API 参考", exact: true}).click();
    await page.getByRole("link", {name: "协议使用指南", exact: true}).click();
    await page.getByRole("heading", {name: "模型列表", exact: true}).waitFor();
    assert.equal(await page.locator(".docs-sidebar").getByRole("link", {name: "快速开始", exact: true}).count(), 0);
    assert.match(await page.locator("article").innerText(), /data\[\]\.id/);
    await page.getByRole("link", {name: "Switch to English", exact: true}).click();
    await page.getByRole("heading", {name: "List models", exact: true}).waitFor();
    for (const [lang, categories, heading] of [
      ["en", ["Product docs", "Integrations", "Billing", "API reference"], "List models"],
      ["zh", ["产品文档", "工具接入", "计费说明", "API 文档"], "模型列表"],
    ]) {
      if (lang === "zh") await page.getByRole("link", {name: "切换到简体中文", exact: true}).click();
      await page.reload();
      await page.getByRole("heading", {name: heading, exact: true}).waitFor();
      const sidebar = page.getByRole("complementary", {name: lang === "zh" ? "文档首页" : "Documentation", exact: true});
      for (const name of categories) await expect(sidebar.getByRole("link", {name, exact: true})).toHaveCount(0);
      await expect(sidebar.getByRole("link", {name: heading, exact: true})).toHaveAttribute("aria-current", "page");
      await sidebar.getByRole("button").click();
      await page.getByRole("searchbox").fill("data[]");
      await expect(page.locator(".search-results").getByRole("link").first()).toBeVisible();
      await page.keyboard.press("Escape");
      await page.screenshot({path: `${reportDirectory}/api-sidebar-${lang}.png`});
    }
  });
  // 前置为独立静态服务器，验证首页入口到安装页、命令和刷新；不写业务数据，结束关闭上下文。
  await scenario("首页 → Docker 安装文档 → 刷新保留正文", async () => {
    await page.goto(base);
    await page.getByRole("heading", { name: "让企业 AI， 有序生长。" }).waitFor();
    assert.equal(await page.locator(".capability-card").count(), 8);
    await page.getByRole("link", { name: "开始使用", exact: true }).first().click();
    await page.getByRole("heading", { name: "XHub 安装与运行指南", exact: true }).waitFor();
    assert.match(await page.locator("article").innerText(), /bash deploy\/build.sh/);
    assert.match(await page.locator("article").innerText(), /admin@xhub.local/);
    await page.reload();
    await page.getByRole("heading", { name: "Docker Compose 部署", exact: true }).waitFor();
  });
  // 前置为真实本地索引，验证搜索跳到护栏正文并读到脚本；未 mock 请求，无业务数据清理。
  await scenario("全文搜索 → XGo 护栏 → 脚本与目录", async () => {
    await page.goto(base);
    await page.getByRole("button", { name: "搜索文档 /", exact: true }).click();
    await page.getByRole("searchbox").fill("XGo");
    await page.locator(".search-results").getByRole("link", { name: /XGo 护栏二开/ }).click();
    await page.getByRole("heading", { name: "XGo 护栏二开", exact: true }).waitFor();
    assert.match(await page.locator("article").innerText(), /ApplyGuardrail/);
    assert.ok(await page.locator("article code.language-go .hljs-keyword").count() > 0, "护栏示例必须高亮");
    await page.getByRole("link", { name: "Switch to English", exact: true }).click();
    assert.match(await page.getByRole("article").innerText(), /ApplyGuardrail/);
  });
  // 前置为首页代码块，验证复制实际写入剪贴板；测试凭据均为占位符，无需清理业务数据。
  await scenario("复制 Docker 命令到剪贴板", async () => {
    await page.goto(base);
    await page.locator(".code-panel").first().getByRole("button", { name: "复制代码" }).click();
    assert.match(await page.evaluate(() => navigator.clipboard.readText()), /docker compose up -d --no-build/);
    assert.equal(await page.getByRole("status").innerText(), "已复制");
  });
  // 前置为双语页面，验证首页和客户文档切换正确语言并保留对应指南，无业务数据写入。
  await scenario("中英文首页与安装文档切换", async () => {
    await page.goto(base);
    await page.getByRole("link", { name: "Switch to English", exact: true }).click();
    await page.getByRole("heading", { name: "Give enterprise AI room to grow.", exact: true }).waitFor();
    await page.getByRole("link", { name: "Get started", exact: true }).first().click();
    await page.getByRole("heading", { name: "Installing and Running XHub", exact: true }).waitFor();
    await page.getByRole("link", { name: "切换到简体中文", exact: true }).click();
    await page.getByRole("heading", { name: "XHub 安装与运行指南", exact: true }).waitFor();
  });
  // 前置为真实站点和仓库配图，验证双语安装/客户指南的图片完整加载、截图原图可达；无 mock 或业务数据，截图写入专用目录。
  await scenario("双语图文指南 → 图片加载 → 截图原图", async () => {
    for (const [document, count] of [["getting-started.zh-CN", 3], ["getting-started", 3], ["user-guide.zh-CN", 8], ["user-guide", 8]]) {
      await page.goto(`${base}docs/${document}.html`);
      const illustrations = page.getByRole("article").getByRole("img");
      assert.equal(await illustrations.count(), count, `${document} 应显示 ${count} 张配图`);
      for (const illustration of await illustrations.all()) {
        assert.ok(await illustration.getAttribute("alt"), "文档配图必须有可访问说明");
        await illustration.scrollIntoViewIfNeeded();
        await expect.poll(() => illustration.evaluate((element) => element.complete && element.naturalWidth > 0), { message: `${document} 图片应从真实静态服务器加载` }).toBeTruthy();
      }
      if (document.startsWith("user-guide")) {
        const locale = document.endsWith("zh-CN") ? "zh" : "en";
        for (const name of ["playground", "usage"]) {
          const link = page.getByRole("article").locator(`a[href$="workflow-guide/${name}-${locale}.jpg"]`);
          await link.click();
          assert.equal(page.url(), `${base}assets/workflow-guide/${name}-${locale}.jpg`);
          const original = await page.request.get(page.url());
          assert.equal(original.status(), 200);
          assert.match(original.headers()["content-type"], /image\/jpeg/);
          await page.goBack();
        }
      }
    }
    await page.goto(`${base}docs/user-guide.zh-CN.html`);
    const screenshot = page.getByRole("img", { name: "中文模型供应商连接表单", exact: true });
    await screenshot.click();
    assert.equal(page.url(), `${base}assets/provider-guide/provider-form-zh.jpg`);
    const response = await page.request.get(page.url());
    assert.equal(response.status(), 200);
    assert.match(response.headers()["content-type"], /image\/jpeg/);
    await page.goBack();
    await page.getByRole("heading", { name: "XHub 客户使用与管理手册", exact: true }).waitFor();
    await screenshot.scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${reportDirectory}/illustrated-guide-desktop.png` });
    await page.getByRole("img", { name: "工作空间配置流程：模型接入、组织团队与成员、虚拟密钥、调用和用量核对", exact: true }).click();
    assert.equal(page.url(), `${base}assets/guide-setup.zh-CN.svg`);
    const vector = await page.request.get(page.url());
    assert.equal(vector.status(), 200);
    assert.match(vector.headers()["content-type"], /image\/svg\+xml/);
  });
  // 前置为真实索引加载，验证空结果与 Escape 退出的客户可见结果；不修改索引，无需清理。
  await scenario("搜索空结果与键盘关闭", async () => {
    await page.goto(base);
    await page.keyboard.press("/");
    await page.getByRole("searchbox").fill("zzzz-unavailable-918273");
    await page.getByText("没有找到相关文档", { exact: true }).waitFor();
    await page.keyboard.press("Escape");
    await page.getByRole("dialog").waitFor({ state: "hidden" });
    assert.equal(await page.getByRole("dialog").isVisible(), false);
  });
  // 前置为网络真实失败，验证静态数据面失败提示；只中止请求而不替换响应，关闭页面后自动清理拦截。
  await scenario("搜索索引请求失败显示可恢复提示", async () => {
    const failurePage = await context.newPage();
    try {
      await failurePage.route("**/search-index.json", (route) => route.abort("failed"));
      await failurePage.goto(base);
      await failurePage.getByRole("button", { name: "搜索文档 /", exact: true }).click();
      await failurePage.getByRole("searchbox").fill("预算");
      await failurePage.getByText("搜索索引加载失败，请使用文档目录或稍后重试。", { exact: true }).waitFor();
      await failurePage.unroute("**/search-index.json");
      await failurePage.getByRole("searchbox").fill("XGo");
      await failurePage.locator(".search-results").getByRole("link", { name: /XGo 护栏二开/ }).waitFor();
    } finally { await failurePage.close(); }
  });
  // 前置为真实架构文档，验证精简请求链及模块入口可见；只读页面，无外部依赖及数据清理。
  await scenario("架构请求链与源码导航实际渲染", async () => {
    await page.goto(`${base}docs/development/architecture.html`);
    assert.match(await page.getByRole("article").innerText(), /Gateway :4000/);
    await expect(page.getByRole("link", { name: "router", exact: true })).toBeVisible();
  });
  // 前置为移动设备尺寸，验证菜单、文档入口与横向布局，截图保存到专用报告目录。
  await scenario("移动端菜单 → 帮助文档，页面无横向溢出", async () => {
    const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
    const mobilePage = await mobile.newPage();
    try {
      await mobilePage.goto(base);
      assert.ok(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
      await mobilePage.screenshot({ path: `${reportDirectory}/homepage-mobile.png`, fullPage: true });
      await mobilePage.getByRole("button", { name: "打开导航" }).click();
      await mobilePage.getByRole("navigation").getByRole("link", { name: "产品文档", exact: true }).click();
      await mobilePage.getByRole("heading", { name: "XHub 文档", exact: true }).waitFor();
      assert.ok(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
      await mobilePage.goto(`${base}docs/user-guide.zh-CN.html`);
      const diagram = mobilePage.getByRole("img", { name: "工作空间配置流程：模型接入、组织团队与成员、虚拟密钥、调用和用量核对", exact: true });
      await diagram.scrollIntoViewIfNeeded();
      await expect.poll(() => diagram.evaluate((element) => element.complete && element.naturalWidth > 0)).toBeTruthy();
      assert.ok(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), "移动端图文指南不能产生横向溢出");
      await mobilePage.screenshot({ path: `${reportDirectory}/illustrated-guide-mobile.png` });
    } finally { await mobile.close(); }
  });
  await page.goto(base);
  await page.screenshot({ path: `${reportDirectory}/homepage-desktop.png`, fullPage: true });
  await page.goto(`${base}docs/development/guardrails.html`);
  await page.screenshot({ path: `${reportDirectory}/documentation-desktop.png`, fullPage: true });
  // 验证真正 Swagger 资源、五域响应、双语选择、旧地址迁移与加载失败恢复；仅故障路径中断网络，不模拟成功响应，最后撤销路由。
  await scenario("Swagger 五域 → 双语 → 旧参考迁移 → 失败恢复", async () => {
    for (const [lang, label, title] of [["zh", "接口领域", "API 参考"], ["en", "API domain", "API reference"]]) {
      await page.goto(base + `reference/${lang}/index.html`);
      await expect(page.getByRole("heading", {name: title, exact: true})).toBeVisible();
      for (const [domain, count] of [["suppliers",5],["users",11],["keys",14],["models",9],["usage",9]]) {
        await page.getByLabel(label, {exact: true}).selectOption(domain);
        await expect(page.locator(".swagger-ui .opblock")).toHaveCount(count);
        assert.equal(await page.getByRole("button", {name: "Try it out", exact: true}).count(), 0);
        await expect(page.getByRole("link", {name: lang === "zh" ? "下载原始 OpenAPI" : "Download original OpenAPI", exact:true})).toHaveAttribute("href", `../specs/${domain}.json`);
      }
      const sessionSummary = page.getByRole("button", {name: /^GET \/spend\/logs\/session\/ui /});
      const summary = await sessionSummary.innerText();
      assert.ok(summary.includes(lang === "en" ? "Empty session_id" : "session_id空时"));
      assert.ok(!summary.includes(lang === "en" ? "权限范围内日志" : "Scoped logs"));
      await page.getByLabel(label, {exact:true}).selectOption("users");
      await page.getByRole("link", {name: lang === "zh" ? "English" : "简体中文", exact:true}).click();
      assert.ok(page.url().includes("domain=users"));
      await expect(page.locator(".swagger-ui .opblock")).toHaveCount(11);
    }
    await page.goto(base + "docs/development/en/user-api.generated.html");
    await page.waitForURL("**/reference/en/index.html?domain=users");
    await expect(page.locator(".swagger-ui .opblock")).toHaveCount(11);
    // 真实跳转验证旧操作书签展开正确操作；未知或损坏片段回到领域，不能误导到另一个操作。
    for (const lang of ["en", "zh"]) {
      await page.goto(base + `docs/development/${lang === "zh" ? "zh-CN" : "en"}/user-api.generated.html#post-usernew`);
      await page.waitForURL(`**/reference/${lang}/index.html?domain=users#/default/createUser`);
      await expect(page.getByRole("button", {name:/^POST \/user\/new /})).toHaveAttribute("aria-expanded", "true");
    }
    for (const hash of ["#not-an-operation", "#%ZZ"]) {
      await page.goto(base + "docs/development/en/user-api.generated.html" + hash);
      await page.waitForURL("**/reference/en/index.html?domain=users");
      await expect(page.locator(".swagger-ui .opblock")).toHaveCount(11);
    }
    await page.route("**/reference/specs/users.json", route => route.abort("failed"));
    try {
      await page.reload();
      await expect(page.getByRole("alert")).toHaveText("Unable to load the contract. Refresh to retry.");
      await expect(page.locator(".swagger-ui .opblock")).toHaveCount(0);
    } finally { await page.unroute("**/reference/specs/users.json"); }
    await page.reload();
    await expect(page.locator(".swagger-ui .opblock")).toHaveCount(11);
    const index = await (await page.request.get(base + "search-index.json")).json();
    assert.ok(!index.some(item => item.url.includes("api.generated.html")), "搜索不能再次混入旧字段表");
    await page.screenshot({path: reportDirectory + "/swagger-reference.png", fullPage: true});
  });
  await scenario("浏览器无未处理脚本错误", async () => { assert.deepEqual(pageErrors, []); });
  await context.close();
} finally {
  await browser?.close();
  await new Promise((resolve) => server.close(resolve));
  await writeFile(`${reportDirectory}/report.json`, JSON.stringify({ regression, results, passed: results.filter((result) => result.passed).length, failed: results.filter((result) => !result.passed).length }, null, 2));
}
if (results.some((result) => !result.passed)) process.exitCode = 1;
