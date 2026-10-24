import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import openapiTS, { astToString } from "openapi-typescript";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const source = "contracts/suppliers/openapi.json";
const labels = {
  en: {title:"Supplier management API — generated reference", notice:"Generated from the pilot OpenAPI source. No production route or client is replaced. See supplier-contract-pilot.md for compatibility and verification boundaries.", operation:"Operation", summary:"Behavior", responses:"HTTP statuses", schemas:"Schemas"},
  "zh-CN": {title:"供应商管理 API — 生成参考", notice:"由试点 OpenAPI 源生成，不替换生产路由或客户端。兼容及验证边界见 supplier-contract-pilot.md。", operation:"操作", summary:"行为", responses:"HTTP 状态", schemas:"数据结构"},
};

/** 为离线参考选择中英文说明；text 是契约中的双语描述，locale 为支持语言，返回文本，无外部调用。 */
function localized(text, locale) {
  const parts = (text || "").split(" / ");
  return locale === "en" ? parts.at(-1) : parts[0];
}

/** 从 spec 生成不依赖 CDN 的离线 Markdown；locale 决定标题和描述，返回完整参考，未知语言显式报错。 */
export function renderReference(spec, locale) {
  const l = labels[locale];
  if (!l) throw new Error(`Unsupported locale: ${locale}`);
  const lines = [`# ${l.title}`, "", l.notice, "", `[OpenAPI](../../../contracts/suppliers/openapi.json)`, "", `| ${l.operation} | ${l.summary} | ${l.responses} |`, "| --- | --- | --- |"];
  for (const [path, methods] of Object.entries(spec.paths)) {
    for (const [method, op] of Object.entries(methods)) {
      lines.push(`| \`${method.toUpperCase()} ${path}\` | ${localized(op.summary, locale)} | ${Object.keys(op.responses).join(", ")} |`);
    }
  }
  lines.push("", `## ${l.schemas}`, "");
  for (const [name, schema] of Object.entries(spec.components.schemas)) {
    // JSON Schema 原文保留字段与校验语义；描述仅在文档展示时选语言，不改变契约或用户输入。
    const view = JSON.parse(JSON.stringify(schema), (key, value) => key === "description" ? localized(value, locale) : value);
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

/** 从 targetRoot 的唯一 spec 调用固定版本生成器；默认仓库根目录，测试可传临时目录，失败原样终止，返回产物映射，不注册运行时 handler。 */
export async function generate(targetRoot = root) {
  const spec = JSON.parse(readFileSync(resolve(targetRoot, source), "utf8"));
  const routes = JSON.parse(readFileSync(resolve(targetRoot, "internal/catalog/routes.json"), "utf8"));
  for (const [path, methods] of Object.entries(spec.paths)) {
    for (const method of Object.keys(methods)) {
      if (!routes.some(r => r.m === method.toUpperCase() && r.p === path)) throw new Error(`Undeclared gateway route: ${method} ${path}`);
    }
  }
  const go = execFileSync("go", ["run", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0", "-generate", "types,skip-prune", "-package", "suppliers", source], {cwd:targetRoot, encoding:"utf8", maxBuffer:4*1024*1024});
  const ts = astToString(await openapiTS(spec));
  return {
    "contracts/suppliers/types.gen.go":go,
    "frontend/src/lib/http/contracts/suppliers.d.ts":ts,
    "docs/development/en/supplier-api.generated.md":renderReference(spec, "en"),
    "docs/development/zh-CN/supplier-api.generated.md":renderReference(spec, "zh-CN"),
  };
}

// 直接执行才生成；测试导入不触发进程或文件写入。仅支持显式 --check，避免拼错参数意外写入。
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.length > 1 || (args.length === 1 && args[0] !== "--check")) throw new Error("Usage: node gen-supplier-contract.mjs [--check]");
  syncArtifacts(root, await generate(), args.includes("--check"));
}
