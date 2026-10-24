import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const domains = ["suppliers", "users", "keys", "models", "usage"];
/** 递归读取Go直接注册声明；输入目录，返回绝对路径文件，无执行或生产副作用。 */
function goFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory()
      ? goFiles(resolve(dir, e.name))
      : e.name.endsWith(".go") && !e.name.endsWith("_test.go")
        ? [resolve(dir, e.name)]
        : [],
  );
}
/** 汇总目录和直接注册声明及契约覆盖；只声明静态覆盖，动态SCIM/Bypass单列边界，不能把目录当真实可用证明。 */
export function inventory(base = root) {
  const records = new Map();
  for (const r of JSON.parse(readFileSync(resolve(base, "internal/catalog/routes.json"), "utf8")))
    records.set(`${r.m} ${r.p}`, { sources: ["catalog"], contract: null, status: "peripheral-unmigrated" });
  for (const file of goFiles(resolve(base, "internal/gateway"))) {
    for (const m of readFileSync(file, "utf8").matchAll(/reg\.Handle\("(GET|POST|PUT|PATCH|DELETE) ([^" ]+)"/g)) {
      const key = `${m[1]} ${m[2]}`;
      const row = records.get(key) ?? { sources: [], contract: null, status: "peripheral-unmigrated" };
      row.sources.push(file.slice(base.length + 1));
      records.set(key, row);
    }
  }
  for (const domain of domains) {
    const spec = JSON.parse(readFileSync(resolve(base, `contracts/${domain}/openapi.json`), "utf8"));
    for (const [path, methods] of Object.entries(spec.paths))
      for (const [method, operation] of Object.entries(methods)) {
        const key = `${method.toUpperCase()} ${path}`;
        const row = records.get(key);
        if (!row) throw new Error(`Contract without route: ${key}`);
        if (row.contract) throw new Error(`Duplicate contract: ${key}`);
        row.contract = { domain, operation: operation.operationId };
        row.status = "core-contracted";
      }
  }
  return {
    scope: "Static declarations only; not a runtime support or test coverage claim",
    dynamicBoundaries: [
      "SCIM concatenated routes in directory.go: enterprise provisioning, outside core CRUD batch",
      "Bypass transport registry: data-plane protocols, stage 5",
      "Frontend callers and real test evidence: docs/development/en/core-contract-completion.md",
    ],
    routes: [...records].sort(([a], [b]) => a.localeCompare(b, "en")).map(([route, data]) => ({ route, ...data })),
  };
}
/** 写入或检查唯一迁移清单；缺文件、新声明或分类漂移必须失败，供CI调用，不修改运行服务。 */
export function syncInventory(base = root, check = false) {
  const path = resolve(base, "contracts/migration-inventory.json");
  const text = JSON.stringify(inventory(base), null, 2) + "\n";
  if (check) {
    if (readFileSync(path, "utf8") !== text) throw new Error("Contract migration inventory drift");
  } else writeFileSync(path, text);
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.length > 1 || (args.length === 1 && args[0] !== "--check"))
    throw new Error("Usage: contract-inventory.mjs [--check]");
  syncInventory(root, args.includes("--check"));
}
