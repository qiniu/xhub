import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

/** 验证契约更新触发站点验证且发布回归构建真实资源后运行双语Swagger用例；仅读取工作流，无外部请求或清理。 */
test("CI builds current Swagger resources and executes both real gateway scenarios", () => {
  const website = readFileSync(new URL("../../.github/workflows/website.yml", import.meta.url), "utf8");
  const release = readFileSync(new URL("../../.github/workflows/release.yml", import.meta.url), "utf8");
  assert.equal((website.match(/"contracts\/\*\*"/g) || []).length, 2, "push 和 PR 都需监视契约来源");
  const runner = readFileSync(new URL("../../scripts/e2e.sh", import.meta.url), "utf8");
  const build = runner.indexOf('npm run build --prefix "$ROOT/website"');
  const e2e = runner.indexOf('npx playwright test "$@"');
  const install = runner.indexOf('npm ci --prefix "$ROOT/website"');
  assert.ok(install > 0 && install < build);
  assert.ok(build > 0 && build < e2e, "真实浏览器回归前必须构建当前契约和 Swagger");
  for (const lang of ["en", "zh-CN"]) assert.ok(release.includes(`scenarios/regression/swagger-reference-${lang}.test.ts`));
  assert.match(website, /if: github.event_name != 'pull_request' && github.ref == 'refs\/heads\/main'/);
});
