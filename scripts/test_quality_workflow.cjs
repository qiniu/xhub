/** 质量工作流回归：读取真实 YAML，验证 PR 门禁、覆盖率输入和失败传播；只读配置，无需清理。 */
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const yaml = require('../frontend/node_modules/js-yaml');

const root = join(__dirname, '..');
const workflow = yaml.load(readFileSync(join(root, '.github/workflows/quality.yml'), 'utf8'));
const codecov = yaml.load(readFileSync(join(root, 'codecov.yml'), 'utf8'));

/** 返回指定作业中匹配名称的步骤；参数为作业和步骤名，缺失时失败；供配置断言调用，无副作用。 */
function step(job, name) {
  const result = workflow.jobs[job].steps.find((item) => item.name === name);
  assert.ok(result, '缺少 ' + job + '/' + name);
  return result;
}

/** 前置真实工作流；验证 push、PR 和手动触发均存在，并确保同一 ref 取消过期检查；只读无清理。 */
test('quality workflow runs for pull requests and maintained branches', () => {
  assert.ok(workflow.on.pull_request !== undefined, 'PR 必须触发质量检查');
  assert.deepEqual(workflow.on.push.branches, ['main', 'dev']);
  assert.ok(workflow.on.workflow_dispatch !== undefined, '必须支持手动重跑');
  assert.equal(workflow.permissions.contents, 'read');
  assert.equal(workflow.permissions['id-token'], 'write');
  assert.equal(workflow.concurrency['cancel-in-progress'], true);
});

/** 前置真实 Go 作业；验证格式、race、原子覆盖率和独立 backend 上传，任一步失败均由 Actions 阻断；只读无清理。 */
test('Go gate produces atomic backend coverage and runs race checks', () => {
  const job = workflow.jobs.go;
  assert.equal(job['timeout-minutes'], 45);
  const formatting = step('go', 'Check Go formatting').run;
  assert.match(formatting, /git diff --name-only --diff-filter=ACMR/);
  assert.match(formatting, /gofmt -l/);
  assert.equal(step('go', 'Run Go vet').run, 'go vet ./...');
  assert.match(step('go', 'Prepare Go test dependencies').run, /docker compose up -d --wait postgres redis/);
  const tests = step('go', 'Run Go tests with coverage');
  assert.equal(tests.env.XHUB_REGRESSION_STRICT, '1');
  assert.equal(tests.env.XHUB_TEST_DATABASE_URL,
    'postgres://xhub:xhub_dev_password@127.0.0.1:5433/xhub?sslmode=disable');
  assert.ok(tests.run.includes("go list ./... | grep -vE"), "必须从 go list 结果筛选质量门禁包");
  assert.match(tests.run, /cmd\/regression/);
  assert.match(tests.run, /frontend\/node_modules/);
  assert.match(tests.run,
    /go test -race -timeout=25m -covermode=atomic -coverprofile=coverage\/go\.out/);
  assert.match(tests.run, /"\$\{packages\[@\]\}"/);
  const upload = step('go', 'Upload Go coverage');
  assert.equal(upload.with.files, 'coverage/go.out');
  assert.equal(upload.with.flags, 'backend');
  assert.equal(upload.with.fail_ci_if_error, true);
  assert.equal(upload.with.use_oidc, true);
});

/** 前置真实前端作业；验证锁文件安装、静态错误预算、已知失败隔离后的全量覆盖率和独立上传；只读无清理。 */
test('frontend gate ratchets existing debt and uploads Vitest coverage', () => {
  const steps = workflow.jobs.frontend.steps;
  assert.ok(steps.some((item) => item.run === 'npm ci'));
  const lint = step('frontend', 'Check ESLint error budget').run;
  assert.match(lint, /eslint . --quiet --format json/);
  assert.match(lint, /--pass-on-unpruned-suppressions/);
  assert.match(lint, /check-eslint-error-budget.mjs eslint-report.json 289/);
  const lintArtifact = step('frontend', 'Upload ESLint report');
  assert.equal(lintArtifact.with.name, 'frontend-eslint-report');
  assert.equal(lintArtifact.with.path, 'frontend/eslint-report.json');
  assert.equal(step('frontend', 'Run frontend tests with coverage').run, 'npm run test:quality');
  const upload = step('frontend', 'Upload frontend coverage');
  assert.equal(upload.with.files, 'frontend/coverage/lcov.info');
  assert.equal(upload.with.flags, 'frontend');
  assert.equal(upload.with.fail_ci_if_error, true);
  assert.equal(upload.with.use_oidc, true);
});

/** 前置真实回归作业；验证质量工作流自身和 ESLint 预算工具均纳入 CI；只读配置，无需清理。 */
test('workflow regression runs quality configuration and lint budget tests', () => {
  const commands = workflow.jobs['workflow-regression'].steps
    .map((item) => item.run)
    .filter(Boolean)
    .join('\n');
  assert.match(commands, /scripts\/test_quality_workflow\.cjs/);
  assert.match(commands, /frontend\/tests\/check-eslint-error-budget\.test\.mjs/);
});

/** 前置真实 Codecov 配置；验证总量允许小幅波动、补丁至少 70% 且后端与前端报告分旗标评估；只读无清理。 */
test('Codecov evaluates project and patch quality by code area', () => {
  assert.equal(codecov.codecov.require_ci_to_pass, true);
  assert.equal(codecov.coverage.status.project.default.target, 'auto');
  assert.equal(codecov.coverage.status.project.default.threshold, '1%');
  assert.equal(codecov.coverage.status.patch.default.target, '70%');
  assert.deepEqual(Object.keys(codecov.flags).sort(), ['backend', 'frontend']);
  assert.equal(codecov.flags.backend.carryforward, true);
  assert.equal(codecov.flags.frontend.carryforward, true);
  assert.equal(codecov.comment.require_changes, true);
});
