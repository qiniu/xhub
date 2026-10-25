import { readFile } from "node:fs/promises";
import { escapeHTML as e, headingSlug } from "./core.mjs";
import { referenceCopy, referenceDomains } from "./reference-copy.mjs";

export const methods = ["get", "post", "put", "patch", "delete", "options", "head", "trace"];

/** 校验五域契约与迁移清单一致；参数是领域、OpenAPI 和清单，返回操作数。
 * 构建和测试调用；缺失结构、外部引用、未登记操作或零操作均抛错，不修改源数据。 */
export function validateReference(domain, spec, inventory) {
  if (!referenceDomains.includes(domain) || spec.openapi !== "3.0.3" || !spec.info?.title || !spec.paths || !inventory?.routes) throw new Error(`Invalid contract: ${domain}`);
  const actual = [];
  for (const [path, item] of Object.entries(spec.paths)) {
    if (!path.startsWith("/")) throw new Error(`Invalid path: ${path}`);
    for (const method of methods) if (item[method]) {
      const op = item[method];
      if (!op.operationId || !op.responses || !Object.keys(op.responses).length) throw new Error(`Invalid operation: ${path}`);
      actual.push(`${method.toUpperCase()} ${path}|${op.operationId}`);
    }
  }
  const expected = inventory.routes.filter(r => r.status === "core-contracted" && r.contract?.domain === domain).map(r => `${r.route}|${r.contract.operation}`);
  if (!actual.length || JSON.stringify(actual.sort()) !== JSON.stringify(expected.sort())) throw new Error(`Contract inventory mismatch: ${domain}`);
  /** 递归检查结构引用；节点为契约对象，只接受本契约内可解析引用，失败阻止发布。 */
  function check(node) {
    if (!node || typeof node !== "object") return;
    if (typeof node.$ref === "string") {
      if (!node.$ref.startsWith("#/")) throw new Error(`External reference: ${node.$ref}`);
      const found = node.$ref.slice(2).split("/").reduce((value, key) => value?.[key.replaceAll("~1", "/").replaceAll("~0", "~")], spec);
      if (!found) throw new Error(`Unresolved reference: ${node.$ref}`);
    }
    Object.values(node).forEach(check);
  }
  check(spec);
  return actual.length;
}

/** 读取版本库五域契约；root 为仓库目录，返回原始 JSON 与统计，缺文件或非法内容直接失败。 */
export async function loadReferences(root) {
  const inventory = JSON.parse(await readFile(`${root}/contracts/migration-inventory.json`, "utf8"));
  return Promise.all(referenceDomains.map(async domain => {
    const raw = await readFile(`${root}/contracts/${domain}/openapi.json`, "utf8");
    const spec = JSON.parse(raw);
    return { domain, raw, count: validateReference(domain, spec, inventory) };
  }));
}

/** 生成独立 API 页面；lang 为 zh/en，entries 为已验证领域统计，返回 HTML。
 * 资源均为同站相对路径，浏览器初始只读，不内置网关地址或凭据。 */
export function referencePage(lang, entries) {
  const t = referenceCopy[lang];
  if (!t) throw new Error("Invalid reference language");
  return `<!doctype html><html lang="${lang === "zh" ? "zh-CN" : "en"}"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${e(t.title)} · XHub</title><link rel="stylesheet" href="../assets/swagger-ui.css"><link rel="stylesheet" href="../assets/reference.css"><body data-locale="${lang}">
<header><a href="../../docs/center/${lang}/index.html">${e(t.product)}</a><a href="../../docs/development/${lang === "zh" ? "zh-CN" : "en"}/README.html">${e(t.developer)}</a><a href="../../docs/center/${lang}/api/models.html">${e(t.guides)}</a><a id="language" href="../${lang === "zh" ? "en" : "zh"}/index.html">${e(t.language)}</a></header>
<main><h1>${e(t.title)}</h1><p>${e(t.scope.replace("{count}", entries.reduce((n, x) => n + x.count, 0)))}</p><p class="boundary">${e(t.boundary)}</p>
<label for="domain">${e(t.domain)}</label><select id="domain">${entries.map(x => `<option value="${x.domain}">${e(t.domains[x.domain])} (${x.count})</option>`).join("")}</select> <a id="download" href="../specs/suppliers.json" download>${e(t.download)}</a>
<form id="target-form"><label for="gateway">${e(t.gateway)}</label><input id="gateway" type="text" placeholder="http://localhost:4000" autocomplete="off"><button type="submit">${e(t.connect)}</button><button id="disconnect" type="button">${e(t.disconnect)}</button><p>${e(t.targetHelp)}</p></form>
<p id="status" role="status">${e(t.readOnly)}</p><p id="error" role="alert" hidden></p><p>${e(t.vendor)}</p><div id="swagger-ui"></div></main>
<script src="../assets/swagger-ui-bundle.js"></script><script type="module" src="../assets/reference-app.js"></script></body></html>`;
}

/** 为旧生成文档构造操作锚点到 Swagger 深链接的映射；spec 为已验证契约，返回纯对象。
 * 构建器用于迁移书签；沿用 Markdown 锚点算法及 Swagger 首标签/default 规则，不访问网络。 */
export function legacyOperationLinks(spec) {
  return Object.fromEntries(Object.entries(spec.paths).flatMap(([path, item]) =>
    methods.filter(method => item[method]).map(method => {
      const op = item[method];
      return [headingSlug(`${method.toUpperCase()} ${path}`), `#/${encodeURIComponent(op.tags?.[0] || "default")}/${encodeURIComponent(op.operationId)}`];
    })));
}

/** 构造旧字段参考的安全跳转 HTML；target 为站内目标，links 为操作映射，anchors 为旧标题。
 * 浏览器将已知操作定位至真实 Swagger 操作；未知书签回退领域首页，无脚本输入拼接或凭据传递。 */
export function referenceRedirect(lang, target, links, anchors) {
  const title = referenceCopy[lang];
  if (!title) throw new Error("Invalid reference language");
  const data = JSON.stringify({ target, links }).replaceAll("<", "\\u003c");
  return `<!doctype html><html lang="${lang === "zh" ? "zh-CN" : "en"}"><meta charset="utf-8"><title>${e(title.title)} · XHub</title>${anchors.map(id => `<span id="${e(id)}"></span>`).join("")}<a href="${e(target)}">${e(title.open)}</a><script>const migration=${data};let anchor="";try{anchor=decodeURIComponent(location.hash.slice(1))}catch{}location.replace(migration.target+(Object.hasOwn(migration.links,anchor)?migration.links[anchor]:""))</script></html>`;
}
