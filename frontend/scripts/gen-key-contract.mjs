import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { generate as generateDomain, renderReference as renderDomain, syncArtifacts } from "./gen-management-contract.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
export { syncArtifacts };
/** 为此领域生成双语离线参考；参数为源契约与语言，返回文档，非法语言失败，无文件写入。 */
export const renderReference = (spec, locale) => renderDomain(spec, locale, "keys");
/** 从指定仓库根目录生成该领域 Go/TS/文档；默认当前仓库，返回文件映射，缺失或非法源立即失败。 */
export const generate = (targetRoot = root) => generateDomain(targetRoot, "keys");

// 仅直接执行才写入或检查；测试导入无副作用，未知参数不得意外写入。
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.length > 1 || (args.length === 1 && args[0] !== "--check")) throw new Error("Usage: node gen-key-contract.mjs [--check]");
  syncArtifacts(root, await generate(), args.includes("--check"));
}
