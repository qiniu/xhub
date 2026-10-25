import test from "node:test";
import assert from "node:assert/strict";
import { readFile, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { loadReferences, validateReference, referencePage, legacyOperationLinks, referenceRedirect } from "../src/reference.mjs";
import { localizedSpec, normalizeGateway, swaggerOptions } from "../src/reference-runtime.mjs";
import { documentSpace, referenceDomain } from "../src/document-space.mjs";
import { documentPage, header } from "../src/templates.mjs";
const root = new URL("../../", import.meta.url).pathname;
const entries = await loadReferences(root);
const inventory = JSON.parse(await readFile(root + "contracts/migration-inventory.json", "utf8"));

/** 验证公开集合来自真实五域契约且只包含48操作；前置仓库文件，不调用网络或修改资源。 */
test("Swagger only publishes the five contracted domains", () => {
  assert.deepEqual(entries.map(e => [e.domain, e.count]), [["suppliers",5],["users",11],["keys",14],["models",9],["usage",9]]);
  for (const { raw } of entries) assert.ok(!Object.keys(JSON.parse(raw).paths).some(p => p.startsWith("/bypass/")));
});

/** 缺失、无效、未登记操作不能静默发布；纯对象负例，临时目录在 finally 删除。 */
test("reference validation fails closed", async () => {
  const source = JSON.parse(entries[0].raw);
  for (const mutation of [
    s => { s.paths = {}; },
    s => { s.paths["/invented"] = { get: { operationId: "fake", responses: { 200: {} } } }; },
    s => { delete s.paths["/credentials"].get.responses; },
    s => { s.components.schemas.Bad = { $ref: "https://outside.test/schema.json" }; },
    s => { s.components.schemas.Bad = { $ref: "#/components/schemas/Missing" }; },
  ]) {
    const spec = structuredClone(source); mutation(spec);
    assert.throws(() => validateReference("suppliers", spec, inventory));
  }
  const directory = await mkdtemp(tmpdir() + "/xhub-reference-");
  try { await assert.rejects(loadReferences(directory)); } finally { await rm(directory, { recursive: true, force: true }); }
});

/** 展示翻译只影响元数据；模拟用户输入及路径中的斜线，确保示例/默认值/协议名和原对象不变。 */
test("localized descriptions preserve examples and input", () => {
  const spec = { summary: "创建用户 / Create user", description: "GET /user/info", properties: { description: { type: "string", description: "说明 / Description" } }, example: { description: "用户原文 / unchanged", prompt: "Hello" }, default: { summary: "用户内容 / Original" } };
  const snapshot = structuredClone(spec);
  assert.equal(localizedSpec(spec, "zh").summary, "创建用户");
  assert.equal(localizedSpec(spec, "en").properties.description.description, "Description");
  for (const lang of ["zh", "en"]) {
    const result = localizedSpec(spec, lang);
    assert.deepEqual(result.example, spec.example); assert.deepEqual(result.default, spec.default);
    assert.equal(result.description, spec.description);
  }
  assert.deepEqual(spec, snapshot);
  assert.throws(() => localizedSpec(spec, "invalid"));
});

/** 手工调用地址正常值与非法值校验；不访问地址，不存储凭据，纯函数无需清理。 */
test("gateway targets are explicit HTTP URLs without embedded credentials", () => {
  assert.equal(normalizeGateway(" http://localhost:4000/ "), "http://localhost:4000");
  assert.equal(normalizeGateway("https://example.test/gw/", "https:"), "https://example.test/gw");
  for (const input of ["", "relative", "javascript:alert(1)", "https://user:pass@example.test", "https://example.test?q=x", "https://example.test#x"]) assert.throws(() => normalizeGateway(input));
  assert.throws(() => normalizeGateway("http://localhost:4000", "https:"));
});

/** 验证只读默认及显式启用，配置不持久化授权或访问外部验证器；源契约保持原样。 */
test("Swagger defaults to read only and does not persist auth", () => {
  const spec = JSON.parse(entries[0].raw), before = structuredClone(spec);
  const readOnly = swaggerOptions(spec, "");
  assert.deepEqual(readOnly.supportedSubmitMethods, []);
  assert.equal(readOnly.validatorUrl, null); assert.equal(readOnly.persistAuthorization, false);
  assert.equal(readOnly.queryConfigEnabled, false);
  const enabled = swaggerOptions(spec, "http://localhost:4000");
  assert.ok(enabled.supportedSubmitMethods.includes("post"));
  assert.deepEqual(enabled.spec.servers, [{ url: "http://localhost:4000" }]);
  assert.deepEqual(spec, before);
});

/** 双语入口显示准确范围和本地资源，不嵌入生产地址；非法语言明确失败，无资源清理。 */
test("reference pages expose scope and local assets in both languages", () => {
  for (const lang of ["zh", "en"]) {
    const html = referencePage(lang, entries);
    assert.match(html, /48/); assert.match(html, /Bypass/); assert.match(html, /SSO\/SCIM/);
    assert.match(html, /\.\.\/assets\/swagger-ui-bundle.js/);
    assert.doesNotMatch(html, /https:\/\/xapi|cdn\./);
    assert.equal((html.match(/<option /g) || []).length, 5);
    assert.ok(header(lang, "./", "./").includes(`reference/${lang}/index.html`));
  }
  assert.throws(() => referencePage("invalid", entries));
});

/** 文档分区和旧生成页领域映射；正常、边界及相似文件名必须正确，不需外部服务。 */
test("product developer and API reference spaces remain separate", () => {
  assert.equal(documentSpace("api/zh-CN/bypass.md"), "product");
  assert.equal(documentSpace("development/en/testing.md"), "developer");
  assert.equal(documentSpace("modules/internal/foo.md"), "developer");
  assert.equal(documentSpace("development/en/user-api.generated.md"), "reference");
  assert.equal(referenceDomain("development/en/user-api.generated.md"), "users");
  assert.equal(referenceDomain("development/en/unknown-api.generated.md"), null);
  const docs = ["api/zh-CN/bypass.md", "development/zh-CN/testing.md", "development/zh-CN/user-api.generated.md"].map(source => ({ source, title: source, lang: "zh" }));
  for (const doc of docs.slice(0,2)) {
    const html = documentPage({ ...doc, html: "", headings: [], documents: docs, alternate: doc.source });
    const sidebar = html.match(/<aside class="docs-sidebar"[\s\S]*?<\/aside>/)[0];
    assert.ok(sidebar.includes(doc.source.replace(".md", ".html")));
    for (const other of docs.filter(x => documentSpace(x.source) !== documentSpace(doc.source))) assert.ok(!sidebar.includes(other.source.replace(".md", ".html")));
  }
});

/** 验证实际日志契约的多段中英元数据完整切换；原始JSON和示例不变，单语及含斜线边界保持，不需清理。 */
test("multi-clause metadata uses the selected language without losing behavior", () => {
  const raw = entries.find(e => e.domain === "usage").raw;
  const spec = JSON.parse(raw);
  for (const field of ["summary", "description"]) {
    const en = localizedSpec(spec, "en").paths["/spend/logs/session/ui"].get[field];
    const zh = localizedSpec(spec, "zh").paths["/spend/logs/session/ui"].get[field];
    assert.doesNotMatch(en, /[\u4e00-\u9fff]/);
    assert.match(en, /Scoped logs.*Empty session_id/);
    assert.match(zh, /权限范围内日志.*session_id空时/);
    assert.doesNotMatch(zh, /Scoped logs|Empty session_id/);
  }
  assert.equal(JSON.stringify(spec), JSON.stringify(JSON.parse(raw)));
  for (const text of ["只有中文", "Only English / Path", "字段 / mixed中文"]) {
    assert.equal(localizedSpec({description:text}, "en").description, text);
  }
});

/** 验证书签映射使用契约operationId且HTML不执行嵌入数据；正常/未知锚点由浏览器测试补充，无外部资源。 */
test("legacy operation bookmarks map to Swagger deep links safely", () => {
  const links = legacyOperationLinks(JSON.parse(entries.find(e => e.domain === "users").raw));
  assert.equal(links["post-usernew"], "#/default/createUser");
  assert.equal(links["get-userinfo"], "#/default/getUser");
  assert.equal(links["unknown"], undefined);
  assert.deepEqual(legacyOperationLinks({paths:{}}), {});
  const tagged = legacyOperationLinks({paths:{"/demo":{get:{operationId:"get/demo",tags:["custom tag"]}}}});
  assert.equal(tagged["get-demo"], "#/custom%20tag/get%2Fdemo");
  const html = referenceRedirect("en", "../reference/en/index.html?domain=users", {...links, x:"</script>"}, ["post-usernew"]);
  assert.equal((html.match(/<\/script>/g)||[]).length,1);
  assert.match(html, /location.replace/);
  assert.match(html, /post-usernew/);
  assert.throws(() => referenceRedirect("bad", "", {}, []));
});
