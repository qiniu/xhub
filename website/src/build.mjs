import { referenceCopy } from "./reference-copy.mjs";
import { loadReferences, referencePage } from "./reference.mjs";
import { documentSpace, referenceDomain } from "./document-space.mjs";
import { publicMarkdownSources } from "./document-links.mjs";
import { consoleDocuments } from "./console-docs.mjs";
import { documentLanguage, localizedSource } from "./localization.mjs";
import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build as bundle } from "esbuild";
import { documentPath } from "./core.mjs";
import { renderMarkdown } from "./markdown.mjs";
import { documentPage, homepage } from "./templates.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../..");
const output = path.resolve(here, "../dist");
// 构建前校验所有公开契约；不允许缺领域时发布不完整或虚假的支持清单。
const references = await loadReferences(root);

/** 将生成内容写入输出目录；参数为相对路径和内容，自动创建父目录；构建流程使用，错误向调用方传播。 */
async function emit(relative, content) {
  const target = path.join(output, relative);
  await mkdir(path.dirname(target), { recursive: true });
  await writeFile(target, content);
}

// 仅清理网站专用输出目录，避免触及网关或控制台构建产物。
await rm(output, { recursive: true, force: true });
await mkdir(output, { recursive: true });
// 仅发布纳入版本管理的正式文档，不收集其他并行任务产生的临时文件。
const sources = publicMarkdownSources(root).filter((source) => source !== "README.md" && source !== "README.zh-CN.md");
const mappings = new Map(sources.map((source) => [source, source.startsWith("docs/") ? source.slice(5) : `modules/${source}`]));
const documents = [];
for (const source of sources) {
  const websiteSource = mappings.get(source);
  const markdown = await readFile(path.join(root, source), "utf8");
  const rendered = renderMarkdown(markdown, source, websiteSource, mappings);
  const lang = documentLanguage(source);
  documents.push({ source: websiteSource, repositorySource: source, lang, ...rendered });
}
const synced = await consoleDocuments(root);
for (const document of synced.documents) {
  const rendered = renderMarkdown(document.markdown, document.repositorySource, document.source, mappings);
  documents.push({ ...document, ...rendered });
}
for (const document of documents) {
  const counterpart = localizedSource(document.repositorySource, document.lang === "zh" ? "en" : "zh", new Set(documents.map((item) => item.repositorySource)));
  const alternate = mappings.get(counterpart) || (document.lang === "zh" ? "README.md" : "README.zh-CN.md");
  if (documentSpace(document.source) === "reference") {
    const prefix = "../".repeat(documentPath(document.source).split("/").length - 1);
    const target = `${prefix}reference/${document.lang}/index.html?domain=${referenceDomain(document.source)}`;
    // 保留旧地址及锚点以兼容已有链接，字段参考只在独立入口展示，不复制一份可漂移正文。
    const anchors = document.headings.map(h => `<span id="${h.id}"></span>`).join("");
    await emit(documentPath(document.source), `<!doctype html><html lang="${document.lang === "zh" ? "zh-CN" : "en"}"><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=${target}"><title>${referenceCopy[document.lang].title} · XHub</title>${anchors}<a href="${target}">${referenceCopy[document.lang].open}</a></html>`);
    continue;
  }
  await emit(documentPath(document.source), documentPage({ ...document, documents, alternate: document.source.startsWith("center/") ? document.source.replace("/" + document.lang + "/", document.lang === "zh" ? "/en/" : "/zh/") : alternate, centerGroups: synced.groups }));
}
await emit("docs/index.html", '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=./center/zh/index.html"><title>XHub 文档中心</title><a href="./center/zh/index.html">XHub 文档中心 / Documentation</a></html>');
await emit("index.html", homepage("zh"));
// 保留已公开开发文档地址；浏览器重定向保留原查询串和标题锚点，目标正文只发布在语言目录。
for (const document of documents.filter((item) => /^development\/(en|zh-CN)\//.test(item.source))) {
  const english = document.lang === "en";
  const legacy = document.source.replace(/\/(en|zh-CN)\//, "/").replace(/\.md$/, english ? ".en.md" : ".md");
  const target = "./" + (english ? "en/" : "zh-CN/") + path.posix.basename(documentPath(document.source));
  await emit(documentPath(legacy), '<!doctype html><html lang="' + (english ? 'en' : 'zh-CN') + '"><meta charset="utf-8"><title>XHub</title><link rel="canonical" href="' + target + '"><a href="' + target + '">' + (english ? 'Open documentation' : '打开文档') + '</a><script>location.replace(' + JSON.stringify(target) + '+location.search+location.hash)</script></html>');
}
await emit("en/index.html", homepage("en"));
await emit("search-index.json", JSON.stringify(documents.filter(d => documentSpace(d.source) !== "reference").map(({ source, title, text, lang }) => ({ url: documentPath(source), title, text, lang }))));
await emit("404.html", `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>页面未找到 · XHub</title><body><h1>页面未找到 / Page not found</h1><p>请从站点首页重新打开文档。 / Open documentation from the site home.</p><a href="https://qiniu.github.io/xhub/">XHub 官网 / Website</a></body></html>`);
await emit(".nojekyll", "");
await emit("assets/favicon.svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><rect width="48" height="48" rx="13" fill="#146b50"/><path d="M14 13h6l4 7 4-7h6L27 24l7 11h-6l-4-7-4 7h-6l7-11z" fill="#fff"/></svg>`);
await cp(path.join(root, "docs/assets"), path.join(output, "assets"), { recursive: true });
await cp(path.join(here, "style.css"), path.join(output, "assets/style.css"));
await bundle({ entryPoints: [path.join(here, "app.mjs")], outdir: path.join(output, "assets"), entryNames: "app", chunkNames: "chunks/[name]-[hash]", bundle: true, splitting: true, format: "esm", minify: true, target: ["es2022"], logLevel: "warning" });
console.log(`Built ${documents.length} documentation pages, 2 homepages, and a local search index in website/dist.`);

// Swagger 资源与契约随版本离线打包，不访问 CDN 或在线验证服务。
await mkdir(path.join(output, "reference/assets"), { recursive: true });
for (const name of ["swagger-ui.css", "swagger-ui-bundle.js", "LICENSE", "NOTICE"]) {
  await cp(path.join(here, "../node_modules/swagger-ui-dist", name), path.join(output, "reference/assets", name));
}
await cp(path.join(here, "reference.css"), path.join(output, "reference/assets/reference.css"));
for (const entry of references) await emit(`reference/specs/${entry.domain}.json`, entry.raw);
for (const lang of ["zh", "en"]) await emit(`reference/${lang}/index.html`, referencePage(lang, references));
await emit("reference/index.html", '<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=zh/index.html"><a href="zh/index.html">API 参考</a><a href="en/index.html">API reference</a>');
await bundle({ entryPoints: [path.join(here, "reference-app.mjs")], outfile: path.join(output, "reference/assets/reference-app.js"), bundle: true, format: "esm", minify: true, target: ["es2022"] });
