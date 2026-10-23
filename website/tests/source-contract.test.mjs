import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = fileURLToPath(new URL("../../", import.meta.url));

/** 目的：性能文档仅描述已接线的队列与查询行为；前置真实源码，验证双语数值及边界，无写入无需清理。 */
test("双语性能说明匹配刷写批次与查询路径", () => {
  assert.ok(source("internal/dataplane/runtime/live.go").includes("PeekLogs(500)"));
  assert.ok(source("internal/config/config.go").includes("86400"));
  assert.ok(source("internal/iam/usage_read.go").includes("ListUsageSessions"));
  for (const locale of ["en", "zh-CN"]) {
    const document = source("docs/development/" + locale + "/api-performance-design.md");
    for (const value of ["500", "86400", "request_id", "QuotaAndRates", "QuotaPath", "limit / offset"]) {
      assert.ok(document.includes(value), "性能说明缺少当前执行边界：" + value);
    }
    assert.doesNotMatch(document, /设计提案|后续实现|新旧|供兼容|design proposal|backward compatibility/);
  }
});

/** 用途：读取仓库源码契约；参数为仓库相对路径，返回 UTF-8 正文；测试调用，缺失文件直接失败，无写入或清理。 */
function source(file) { return readFileSync(path.join(root, file), "utf8"); }

/** 目的：路由新增、删除或处理器变化必须同步索引；前置真实注册源码，逐项验证文档集合完全相等，纯读取无需清理。 */
test("直接注册的 HTTP 入口与处理器索引完整一致", () => {
  const expected = [];
  const directories = ["internal/gateway", ...readdirSync(path.join(root, "internal/gateway"), { withFileTypes: true }).filter((entry) => entry.isDirectory()).map((entry) => "internal/gateway/" + entry.name)];
  for (const directory of directories) {
    for (const file of readdirSync(path.join(root, directory)).filter((name) => name.endsWith(".go") && !name.endsWith("_test.go"))) {
      for (const line of source(directory + "/" + file).split("\n")) {
        const route = line.match(/reg\.Handle\("([A-Z]+ \/[^"]*)",(.*)/);
        if (!route) continue;
        const handler = route[2].match(/\{\s*(\w+)\(h, w, r\)/) || route[2].match(/s\.(\w+)/) || route[2].trim().match(/^(\w+)\s*\)/);
        assert.ok(handler, "无法识别注册处理器：" + line);
        expected.push([route[1], handler[1], directory + "/" + file].join("|"));
      }
    }
  }
  const actual = [...source("docs/development/zh-CN/handler-index.md").matchAll(/\| `([A-Z]+ \/[^`]+)` \| `(\w+)` \| \[[^\]]+\]\(\.\.\/\.\.\/\.\.\/([^\)]+)\) \|/g)].map((match) => match.slice(1).join("|"));
  assert.ok(expected.length > 150, "应覆盖全部直接注册源码");
  assert.deepEqual(actual.sort(), expected.sort(), "索引缺少、增加或错误描述了真实注册入口");
});

/** 目的：模型列表和价格源常量变化时提醒同步双语文档；前置当前源码及翻译，验证数值、匿名参数边界与默认源，无副作用无需清理。 */
test("双语模型发现和价格源说明使用当前代码契约", () => {
  const created = source("internal/gateway/models/list.go").match(/defaultModelCreatedAt int64 = (\d+)/)[1];
  const feed = source("internal/catalog/feed.go").match(/const MarketURL = "([^"]+)"/)[1];
  for (const locale of ["en", "zh-CN"]) {
    const models = source("docs/api/" + locale + "/models.md");
    assert.ok(models.includes(created));
    assert.ok(models.includes("openai"));
    assert.match(models, locale === "en" ? /Ignored for anonymous discovery/ : /匿名发现忽略此参数/);
    const setup = source(locale === "en" ? "docs/getting-started.md" : "docs/getting-started.zh-CN.md");
    assert.ok(setup.includes(feed), "安装指南缺少实际默认价格源");
    assert.ok(setup.includes("make e2e-offline"));
    const guide = source(locale === "en" ? "docs/user-guide.md" : "docs/user-guide.zh-CN.md");
    assert.ok(guide.includes(feed), "用户指南必须说明实际默认价格源");
    assert.doesNotMatch(guide, /Price refresh requires XHUB_PRICE_FEED_URL|价格刷新需设置 XHUB_PRICE_FEED_URL/, "不能把可选覆盖配置写成刷新前提");
    assert.doesNotMatch(guide, /membership revokes personal keys|Personal keys for that team are revoked|个人密钥会被吊销/, "移除团队关系不能描述成自动吊销个人凭证");
  }
});

/** 目的：首页入口文档不得否定已实现的企业身份能力；前置当前独立配置源码，验证双语说明及凭据保留边界，无写入无需清理。 */
test("双语项目入口反映企业身份与成员凭据当前边界", () => {
  const implementation = source("internal/iam/enterprise_admin.go");
  assert.ok(implementation.includes("CreateEnterpriseOIDC"));
  assert.ok(implementation.includes("SCIMConfigured: false"));
  for (const file of ["README.md", "README.zh-CN.md"]) {
    const document = source(file);
    assert.ok(document.includes("OIDC Code + PKCE"));
    assert.ok(document.includes("SCIM"));
    assert.doesNotMatch(document, /exclude SSO\/SCIM|不包含 SSO\/SCIM|Removing a member invalidates their personal keys|绑定原团队的个人密钥失效/);
  }
});

/** 目的：统一续接与原厂透传文档区分实际执行链；前置执行器和会话源码，验证灰度参数接线、双语历史、护栏及协议字段边界，纯读取无需清理。 */
test("Responses 与 Bypass 说明匹配各自执行链", () => {
  const execution = source("internal/dataplane/execution/serve.go");
  assert.match(execution, /ResponsesRequestBody\(body,\s*plan\.History,\s*nativeEligible\)/, "标准 Responses 必须同时传递网关历史与原生灰度判定");
  assert.ok(execution.includes('nativeEligible := op == "responses" && providerdeployment.NativeResponsesConfigured(cfg.ModelList) && h.NativeResponsesAllowed(p.UserID)'), "原生解析必须受入口、部署与可信用户灰度共同约束");
  for (const locale of ["en", "zh-CN"]) {
    const responses = source("docs/api/" + locale + "/responses.md");
    assert.match(responses, locale === "en" ? /Uses gateway history/ : /使用网关历史/);
    assert.doesNotMatch(responses, /Requires upstream state support|需要上游状态支持/);
    for (const file of ["native-chat", "native-responses", "native-anthropic", "native-gemini", "native-vertex", "native-images", "native-image-edits"]) {
      const document = source("docs/api/" + locale + "/" + file + ".md");
      assert.match(document, locale === "en" ? /Bypass does not run the standard pre-call text guardrail chain/ : /Bypass 不执行标准入口的调用前文本护栏链/);
    }
    assert.doesNotMatch(source("docs/api/" + locale + "/native-gemini.md"), /Vertex project|Vertex 项目/);
  }
});

/** 目的：新增预算核对路由可从双语索引查到实际操作边界；前置真实文档与路由源码，检查入口、仅管理员及attempt费用语义，纯读取无需清理。 */
test("双语处理器索引提供人工对账指南及实际接口", () => {
  for (const locale of ["en", "zh-CN"]) {
    assert.ok(source("docs/development/" + locale + "/handler-index.md").includes("(budget-reconciliation.md)"), "缺少同语言人工对账指南入口");
    const document = source("docs/development/" + locale + "/budget-reconciliation.md");
    for (const value of ["GET /budget/reservations", "POST /budget/reservations/{reservation_id}/reconcile", "verified_cost_usd", "expected_fingerprint", "attempt"]) {
      assert.ok(document.includes(value), "人工对账文档缺少实际契约：" + value);
    }
    assert.match(document, locale === "en" ? /[Pp]latform administrator/ : /平台管理员/);
  }
});
