import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import openapiTS, { astToString } from "openapi-typescript";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");

const labels = {
  en: {title:"Supplier management API — generated reference", notice:"Generated from the pilot OpenAPI source. No production route or client is replaced. See supplier-contract-pilot.md for compatibility and verification boundaries.", operation:"Operation", summary:"Behavior", responses:"HTTP statuses", schemas:"Schemas"},
  "zh-CN": {title:"供应商管理 API — 生成参考", notice:"由试点 OpenAPI 源生成，不替换生产路由或客户端。兼容及验证边界见 supplier-contract-pilot.md。", operation:"操作", summary:"行为", responses:"HTTP 状态", schemas:"数据结构"},
};

/** 为离线参考选择中英文说明；text 是契约中的双语描述，locale 为支持语言，返回文本，无外部调用。 */
function localized(text, locale) {
  const parts = (text || "").split(" / ");
  return locale === "en" ? parts.at(-1) : parts[0];
}

/** 为文档递归选择说明语言；value为契约节点，locale为语言，返回副本。
 * 供操作与Schema展示使用；example/examples/default/enum/const是原始数据，不翻译、不修改输入或协议字段。 */
function localizedView(value, locale) {
  if (Array.isArray(value)) return value.map(item => localizedView(item, locale));
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, child]) => {
    if (["example", "examples", "default", "enum", "const"].includes(key)) return [key, child];
    return [key, key === "description" && typeof child === "string" ? localized(child, locale) : localizedView(child, locale)];
  }));
}

/** 从 spec 生成不依赖 CDN 的离线 Markdown；locale 决定标题和描述，返回完整参考，未知语言显式报错。 */
export function renderReference(spec, locale, domain = "suppliers") {
  const l = labels[locale] && { ...labels[locale] };
  if (l && domain === "users") {
    l.title = locale === "en" ? "User management API — generated reference" : "用户管理 API — 生成参考";
    l.notice = locale === "en" ? "Generated from OpenAPI; no production handler is replaced. See user-contract.md for compatibility and verification boundaries." : "由 OpenAPI 生成，不替换生产 handler。兼容及验证边界见 user-contract.md。";
  }
  if (l && domain === "keys") {
    l.title = locale === "en" ? "Key management API — generated reference" : "密钥管理 API — 生成参考";
    l.notice = locale === "en" ? "Generated from OpenAPI; no production handler is replaced. See key-contract.md for compatibility and verification boundaries." : "由 OpenAPI 生成，不替换生产 handler。兼容及验证边界见 key-contract.md。";
  }
  if (l && domain === "models") {
    l.title = locale === "en" ? "Model and endpoint API — generated reference" : "模型与端点 API — 生成参考";
    l.notice = locale === "en" ? "Generated from OpenAPI; no production handler is replaced. See model-contract.md for verification boundaries." : "由 OpenAPI 生成，不替换生产 handler。验证边界见 model-contract.md。";
  }
  if (l && domain === "usage") {
    l.title = locale === "en" ? "Logs and usage API — generated reference" : "日志与用量 API — 生成参考";
    l.notice = locale === "en" ? "Generated from OpenAPI; existing authorization and billing remain unchanged. See core-contract-completion.md." : "由 OpenAPI 生成，保留既有权限与计费。范围见 core-contract-completion.md。";
  }
  if (!l) throw new Error(`Unsupported locale: ${locale}`);
  const lines = [`# ${l.title}`, "", l.notice, "", `[OpenAPI](../../../contracts/${domain}/openapi.json)`, "", `| ${l.operation} | ${l.summary} | ${l.responses} |`, "| --- | --- | --- |"];
  for (const [path, methods] of Object.entries(spec.paths)) {
    for (const [method, op] of Object.entries(methods)) {
      lines.push(`| \`${method.toUpperCase()} ${path}\` | ${localized(op.summary, locale)} | ${Object.keys(op.responses).join(", ")} |`);
    }
  }
  for (const [path, methods] of Object.entries(spec.paths)) {
    for (const [method, op] of Object.entries(methods)) {
      lines.push("", `### ${method.toUpperCase()} ${path}`, "", localized(op.description || op.summary, locale), "");
      if (op.parameters) lines.push("```json", JSON.stringify(localizedView(op.parameters, locale), null, 2), "```", "");
      if (op.requestBody) lines.push("```json", JSON.stringify(localizedView(op.requestBody, locale), null, 2), "```", "");
    }
  }
  lines.push("", `## ${l.schemas}`, "");
  for (const [name, schema] of Object.entries(spec.components.schemas)) {
    // JSON Schema 原文保留字段与校验语义；描述仅在文档展示时选语言，不改变契约或用户输入。
    const view = localizedView(schema, locale);
    lines.push(`### ${name}`, "", "```json", JSON.stringify(view, null, 2), "```", "");
  }
  return lines.join("\n");
}

/** 将命名产物写入 targetRoot 或只读比对；check=true 时缺失/漂移均抛错，供 CLI 和临时目录单测使用，不修改源文件。 */
export function syncArtifacts(targetRoot, artifacts, check) {
  for (const [path, content] of Object.entries(artifacts)) {
    const target = resolve(targetRoot, path);
    if (check) {
      if (readFileSync(target, "utf8") !== content) throw new Error(`Generated contract drift: ${path}`);
    } else {
      mkdirSync(dirname(target), { recursive:true });
      writeFileSync(target, content);
    }
  }
}

/** 从 targetRoot/domain 的唯一 spec（仅 users/suppliers/keys/models/usage） 调用固定版本生成器；默认仓库根目录，测试可传临时目录，失败原样终止，返回产物映射，不注册运行时 handler。 */
export async function generate(targetRoot = root, domain = "suppliers") {
  if (!["suppliers", "users", "keys", "models", "usage"].includes(domain)) throw new Error(`Unsupported contract: ${domain}`);
  const source = `contracts/${domain}/openapi.json`;
  const prefix = ({ suppliers: "supplier", users: "user", keys: "key", models: "model", usage: "usage" })[domain];
  const spec = JSON.parse(readFileSync(resolve(targetRoot, source), "utf8"));
  const routes = JSON.parse(readFileSync(resolve(targetRoot, "internal/catalog/routes.json"), "utf8"));
  // 目录来自兼容清单，部分已实现的用户/密钥别名只在真实挂载文件中；两者都读取，绝不靠手写白名单放行。
  if (domain === "users" || domain === "keys" || domain === "models" || domain === "usage") {
    const mount = readFileSync(resolve(targetRoot, `internal/gateway/${domain === "users" ? "identity" : domain}/mount.go`), "utf8");
    for (const match of mount.matchAll(/reg\.Handle\("(GET|POST|PUT|PATCH|DELETE) ([^" ]+)"/g)) routes.push({m:match[1], p:match[2]});
  }
  for (const [path, methods] of Object.entries(spec.paths)) {
    for (const method of Object.keys(methods)) {
      if (!routes.some(r => r.m === method.toUpperCase() && r.p === path)) throw new Error(`Undeclared gateway route: ${method} ${path}`);
    }
  }
  const go = execFileSync("go", ["run", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0", "-generate", "types,skip-prune", "-package", domain, source], {cwd:targetRoot, encoding:"utf8", maxBuffer:4*1024*1024});
  const ts = astToString(await openapiTS(spec));
  return {
    [`contracts/${domain}/types.gen.go`]:go,
    [`frontend/src/lib/http/contracts/${domain}.d.ts`]:ts,
    [`docs/development/en/${prefix}-api.generated.md`]:renderReference(spec, "en", domain),
    [`docs/development/zh-CN/${prefix}-api.generated.md`]:renderReference(spec, "zh-CN", domain),
  };
}
