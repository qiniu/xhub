import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

import { inspectContractSources, resolveGeneratorPaths, runGeneratorCheck } from "./gen-api-types.mjs";

/**
 * 创建与真实仓库层级一致的临时前端脚本路径。
 * 参数无；返回临时根目录和脚本 URL，供路径与来源测试调用。调用方负责删除 root；
 * 夹具不读取真实仓库，也不生成 schema。
 */
function makeFixture() {
  const root = mkdtempSync(join(tmpdir(), "xhub-api-generator-"));
  const script = join(root, "frontend", "scripts", "gen-api-types.mjs");
  mkdirSync(join(root, "frontend", "scripts"), { recursive: true });
  writeFileSync(script, "// fixture\n");
  return { root, scriptUrl: pathToFileURL(script).href };
}

test("仓库根目录只从 frontend 向上一层计算", () => {
  /** 正常路径应落在夹具根目录，防止生成器再次越过实际仓库。 */
  const fixture = makeFixture();
  try {
    const paths = resolveGeneratorPaths(fixture.scriptUrl);
    assert.equal(paths.repoRoot, fixture.root);
    assert.equal(paths.outputPath, join(fixture.root, "frontend", "src", "lib", "http", "schema.d.ts"));
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("路由目录存在时仍不冒充 OpenAPI schema", () => {
  /** 边界场景只有 method/path 目录；检查结果必须拒绝把它视为完整合同。 */
  const fixture = makeFixture();
  try {
    const paths = resolveGeneratorPaths(fixture.scriptUrl);
    mkdirSync(join(fixture.root, "internal", "catalog"), { recursive: true });
    writeFileSync(paths.routeCatalogPath, "[]\n");
    assert.deepEqual(inspectContractSources(paths), { routeCatalogExists: true, hasSchemaSource: false });
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("缺少 Go OpenAPI 来源时失败且保留已有生成文件", () => {
  /** 失败路径应给出迁移原因，并保证已有 schema 内容没有任何副作用。 */
  const fixture = makeFixture();
  try {
    const paths = resolveGeneratorPaths(fixture.scriptUrl);
    mkdirSync(join(fixture.root, "internal", "catalog"), { recursive: true });
    mkdirSync(join(fixture.root, "frontend", "src", "lib", "http"), { recursive: true });
    writeFileSync(paths.routeCatalogPath, "[]\n");
    writeFileSync(paths.outputPath, "existing schema\n");
    assert.throws(() => runGeneratorCheck(fixture.scriptUrl), /只包含 method\/path/);
    assert.throws(() => runGeneratorCheck(fixture.scriptUrl), /gen:api:suppliers/);
    assert.equal(readFileSync(paths.outputPath, "utf8"), "existing schema\n");
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});
