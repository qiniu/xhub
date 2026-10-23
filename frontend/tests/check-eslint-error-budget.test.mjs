/** 目的：验证 ESLint 历史错误基线只允许持平或下降；前置临时 JSON 报告，验证通过、超限和非法参数，临时目录自动清理。 */
import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const script = new URL("../scripts/check-eslint-error-budget.mjs", import.meta.url);

test("ESLint error budget accepts the baseline and rejects regressions", () => {
  const directory = mkdtempSync(join(tmpdir(), "xhub-eslint-budget-"));
  try {
    const report = join(directory, "eslint.json");
    writeFileSync(report, JSON.stringify([{ messages: [{ severity: 2 }, { severity: 1 }] }]));
    assert.match(execFileSync(process.execPath, [script.pathname, report, "1"], { encoding: "utf8" }), /errors: 1/);
    assert.equal(spawnSync(process.execPath, [script.pathname, report, "0"]).status, 1);
    assert.equal(spawnSync(process.execPath, [script.pathname, report, "invalid"]).status, 2);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
