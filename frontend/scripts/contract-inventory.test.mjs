import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { inventory, syncInventory } from "./contract-inventory.mjs";
/** 真实仓库清单必须有全部契约操作且不把目录等同运行支持；只读，无清理。 */
test("core inventory exposes contracted and peripheral routes", () => {
  const data = inventory();
  assert.ok(data.routes.some((r) => r.route === "POST /key/regenerate" && r.contract?.domain === "keys"));
  assert.ok(data.routes.some((r) => r.route === "GET /spend/logs/ui" && r.contract?.domain === "usage"));
  assert.ok(data.routes.some((r) => r.status === "peripheral-unmigrated"));
  assert.ok(data.dynamicBoundaries.length > 0);
  syncInventory(undefined, true);
});
/** 临时最小仓库验证缺源/缺产物/新路由漂移/重复契约均失败；finally删除全部夹具。 */
test("inventory rejects missing sources and stale classification", () => {
  const root = mkdtempSync(join(tmpdir(), "xhub-inventory-"));
  try {
    assert.throws(() => inventory(root), /ENOENT/);
    mkdirSync(join(root, "internal/catalog"), { recursive: true });
    mkdirSync(join(root, "internal/gateway"), { recursive: true });
    writeFileSync(join(root, "internal/catalog/routes.json"), '[{"m":"GET","p":"/example"}]');
    for (const domain of ["suppliers", "users", "keys", "models", "usage"]) {
      mkdirSync(join(root, "contracts", domain), { recursive: true });
      writeFileSync(join(root, "contracts", domain, "openapi.json"), '{"paths":{}}');
    }
    assert.throws(() => syncInventory(root, true), /ENOENT/);
    syncInventory(root);
    syncInventory(root, true);
    writeFileSync(join(root, "internal/gateway/mount.go"), 'reg.Handle("GET /new", handler)');
    assert.throws(() => syncInventory(root, true), /drift/);
    writeFileSync(join(root, "contracts/users/openapi.json"), '{"paths":{"/missing":{"get":{"operationId":"bad"}}}}');
    assert.throws(() => inventory(root), /without route/);
    for (const domain of ["users", "keys"])
      writeFileSync(
        join(root, "contracts", domain, "openapi.json"),
        '{"paths":{"/example":{"get":{"operationId":"duplicate"}}}}',
      );
    assert.throws(() => inventory(root), /Duplicate/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
