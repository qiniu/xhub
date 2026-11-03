import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, rmSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { generate, renderReference, syncArtifacts } from "./gen-supplier-contract.mjs";

/** 前置真实契约及固定生成器；验证 Go/TS/双语离线参考都来自同一源，临时产物漂移和缺失不能通过，finally 删除全部文件。 */
test("real generators produce deterministic artifacts and reject drift", async () => {
  const artifacts = await generate();
  assert.equal(Object.keys(artifacts).length, 4);
  assert.match(artifacts["contracts/suppliers/types.gen.go"], /type Credential = map\[string\]json.RawMessage/);
  assert.match(artifacts["frontend/src/lib/http/contracts/suppliers.d.ts"], /createCredential/);
  assert.match(artifacts["docs/development/en/supplier-api.generated.md"], /Save supplier/);
  assert.match(artifacts["docs/development/zh-CN/supplier-api.generated.md"], /保存供应商/);
  const root = mkdtempSync(join(tmpdir(), "xhub-contract-"));
  try {
    assert.throws(() => syncArtifacts(root, artifacts, true), /ENOENT/);
    syncArtifacts(root, artifacts, false);
    syncArtifacts(root, artifacts, true);
    for (const name of Object.keys(artifacts)) {
      writeFileSync(join(root, name), artifacts[name]+"drift");
      assert.throws(() => syncArtifacts(root, artifacts, true), /drift/);
      assert.equal(readFileSync(join(root,name),"utf8"), artifacts[name]+"drift", "检查不得修复或覆盖漂移");
      writeFileSync(join(root,name),artifacts[name]);
    }
  } finally { rmSync(root, {recursive:true,force:true}); }
});

/** 无外部依赖；未知文档语言和无效契约输入必须报错，不生成误导参考，不产生文件。 */
test("reference rejects unsupported inputs", () => {
  assert.throws(() => renderReference({}, "fr"), /Unsupported locale/);
  assert.throws(() => renderReference({}, "en"), TypeError);
});

/** 前置临时契约目录；源文件缺失、非法JSON及未注册路由都必须失败，不能产出类型；finally 清理整个临时树。 */
test("generation rejects missing, malformed and unregistered sources", async () => {
  const root = mkdtempSync(join(tmpdir(), "xhub-invalid-contract-"));
  try {
    await assert.rejects(generate(root), /ENOENT/);
    mkdirSync(join(root, "contracts/suppliers"), { recursive:true });
    const source = join(root, "contracts/suppliers/openapi.json");
    writeFileSync(source,"{");
    await assert.rejects(generate(root), SyntaxError);
    mkdirSync(join(root, "internal/catalog"), { recursive:true });
    writeFileSync(join(root, "internal/catalog/routes.json"), "[]");
    writeFileSync(source, JSON.stringify({ paths:{ "/missing":{ get:{} } } }));
    await assert.rejects(generate(root), /Undeclared gateway route/);
  } finally { rmSync(root, {recursive:true,force:true}); }
});
