import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { generate, renderReference, syncArtifacts } from "./gen-key-contract.mjs";
import { generate as generateDomain } from "./gen-management-contract.mjs";

/** 使用真实固定生成器验证八个操作、双语说明和四份产物；缺失或漂移必须失败，临时文件 finally 清理。 */
test("key contract generators cover real routes and reject all artifact drift", async () => {
  const artifacts = await generate();
  assert.equal(Object.keys(artifacts).length, 4);
  assert.match(artifacts["contracts/keys/types.gen.go"], /type KeyInfoRecord struct/);
  const ts = artifacts["frontend/src/lib/http/contracts/keys.d.ts"];
  for (const op of ["createKey", "listKeys", "getKey", "getKeyV2", "updateKey", "blockKey", "unblockKey", "deleteKeys"]) assert.ok(ts.includes(op), op);
  assert.match(artifacts["docs/development/en/key-api.generated.md"], /not a batch transaction/);
  assert.match(artifacts["docs/development/zh-CN/key-api.generated.md"], /不是整批事务/);
  const root = mkdtempSync(join(tmpdir(), "xhub-keys-"));
  try {
    assert.throws(() => syncArtifacts(root, artifacts, true), /ENOENT/);
    syncArtifacts(root, artifacts, false);
    syncArtifacts(root, artifacts, true);
    for (const name of Object.keys(artifacts)) {
      writeFileSync(join(root, name), artifacts[name] + "drift");
      assert.throws(() => syncArtifacts(root, artifacts, true), /drift/);
      writeFileSync(join(root, name), artifacts[name]);
    }
  } finally { rmSync(root, {recursive:true, force:true}); }
});

/** 生成失败必须暴露而非产生空契约；覆盖未知领域/语言、缺源、坏JSON及不存在路由，临时目录最终删除。 */
test("key generation rejects invalid source and domain", async () => {
  assert.throws(() => renderReference({}, "fr"), /Unsupported locale/);
  await assert.rejects(generateDomain(undefined, "typo"), /Unsupported contract/);
  const root = mkdtempSync(join(tmpdir(), "xhub-keys-invalid-"));
  try {
    await assert.rejects(generate(root), /ENOENT/);
    mkdirSync(join(root, "contracts/keys"), {recursive:true});
    const source = join(root, "contracts/keys/openapi.json");
    writeFileSync(source, "{");
    await assert.rejects(generate(root), SyntaxError);
    mkdirSync(join(root, "internal/catalog"), {recursive:true});
    writeFileSync(join(root, "internal/catalog/routes.json"), "[]");
    mkdirSync(join(root, "internal/gateway/keys"), {recursive:true});
    writeFileSync(join(root, "internal/gateway/keys/mount.go"), "");
    writeFileSync(source, JSON.stringify({paths:{"/missing":{get:{}}}}));
    await assert.rejects(generate(root), /Undeclared gateway route/);
  } finally { rmSync(root, {recursive:true, force:true}); }
});
