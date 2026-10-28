/** 质量工作流回归：读取真实 YAML，验证 PR 门禁、覆盖率输入和失败传播；只读配置，无需清理。 */
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync, mkdtempSync, mkdirSync, writeFileSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { spawnSync } = require('node:child_process');
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
  const checkout = job.steps.find((item) => item.uses === 'actions/checkout@v4');
  assert.ok(checkout, 'Go 作业必须检出源码');
  assert.equal(checkout.with['fetch-depth'], 0, '格式检查需要完整历史来读取 PR 基线');
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
  assert.match(lint, /set \+e[\s\S]*lint_status=\$\?[\s\S]*set -e/);
  assert.match(lint, /lint_status > 1/);
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

/** 质量工作流必须准备真实 Go 工具链并运行空跑/清理门禁自测；只读配置，无需清理。 */
test('quality runs the real Go selection and cleanup gate checks', () => {
  const steps = workflow.jobs['workflow-regression'].steps;
  assert.ok(steps.some(item => item.uses === 'actions/setup-go@v5'));
  const commands = steps.map(item => item.run || '').join('\n');
  for (const name of ['scripts.test_regression_results', 'scripts.test_test_gates', 'scripts.test_e2e_summary', 'scripts.test_e2e_model_endpoints', 'frontend/e2e/checklist-reporter.test.cjs', 'frontend/e2e/guard-resource-cleanup.test.cjs', 'frontend/e2e/guard-resource-fixture.test.cjs']) {
    assert.ok(commands.includes(name), `缺少门禁测试 ${name}`);
  }
});

/** 前置真实质量工作流；契约和供应商轮换的真实数据库回归必须独立阻断，不能 continue-on-error，产物与清理必须保留；只读无清理。 */
test('supplier pilot requires generation, schema validation and real HTTP regression', () => {
  const job = workflow.jobs['supplier-contract'];
  assert.ok(job, '必须注册供应商契约作业');
  assert.equal(step('supplier-contract', 'Check generated contract and TypeScript types').run,
    'npm --prefix frontend run check:api:suppliers');
  assert.equal(step('supplier-contract', 'Test generator drift rejection').run,
    'node --test frontend/scripts/gen-supplier-contract.test.mjs');
  assert.equal(step('supplier-contract', 'Validate OpenAPI and Go types').run, 'go test -race ./contracts/suppliers');
  assert.match(step('supplier-contract', 'Prepare contract database').run, /docker compose up -d --wait postgres redis/);
  assert.equal(step('supplier-contract', 'Run real supplier contract regression').run,
    "bash scripts/regression.sh '^TestSupplier(Contract|Service)'");
  assert.ok(job.steps.every(item => !item['continue-on-error']), '门禁不得忽略失败');
  const cleanup = job.steps.find(item => item.run === 'docker compose down -v');
  assert.equal(cleanup.if, 'always()');
  const artifact = job.steps.find(item => item.uses === 'actions/upload-artifact@v4');
  assert.equal(artifact.if, 'always()');
  assert.equal(artifact.with['if-no-files-found'], 'error');
});

/** 前置真实质量工作流；契约漂移和真实数据库回归必须独立阻断，不能 continue-on-error，产物与清理必须保留；只读无清理。 */
test('user contract requires generation, permissions and real budget regression', () => {
  const job = workflow.jobs['user-contract'];
  assert.ok(job, '必须注册用户契约作业');
  assert.equal(step('user-contract', 'Check generated contract and TypeScript types').run,
    'npm --prefix frontend run check:api:users');
  assert.equal(step('user-contract', 'Test generator drift rejection').run,
    'node --test frontend/scripts/gen-user-contract.test.mjs');
  assert.equal(step('user-contract', 'Validate OpenAPI and Go types').run, 'go test -race ./contracts/users');
  assert.match(step('user-contract', 'Prepare contract database').run, /docker compose up -d --wait postgres redis/);
  assert.equal(step('user-contract', 'Run real user contract regression').run,
    "bash scripts/regression.sh '^Test(UserContract|UserBulkBudget|UserMonthlyBudget|UserEnterpriseIdentity|SoftBudgetAllocationHTTP)'");
  assert.ok(job.steps.every(item => !item['continue-on-error']), '门禁不得忽略失败');
  const cleanup = job.steps.find(item => item.run === 'docker compose down -v');
  assert.equal(cleanup.if, 'always()');
  const artifact = job.steps.find(item => item.uses === 'actions/upload-artifact@v4');
  assert.equal(artifact.if, 'always()');
  assert.equal(artifact.with['if-no-files-found'], 'error');
});

/** 前置真实质量工作流；契约漂移和真实数据库回归必须独立阻断，不能 continue-on-error，产物与清理必须保留；只读无清理。 */
test('key contract requires generation, permissions and real budget regression', () => {
  const job = workflow.jobs['key-contract'];
  assert.ok(job, '必须注册密钥契约作业');
  assert.equal(step('key-contract', 'Check generated contract and TypeScript types').run,
    'npm --prefix frontend run check:api:keys');
  assert.equal(step('key-contract', 'Test generator drift rejection').run,
    'node --test frontend/scripts/gen-key-contract.test.mjs');
  assert.equal(step('key-contract', 'Validate OpenAPI and Go types').run, 'go test -race ./contracts/keys');
  assert.match(step('key-contract', 'Prepare contract database').run, /docker compose up -d --wait postgres redis/);
  assert.equal(step('key-contract', 'Run real key contract regression').run,
    "bash scripts/regression.sh '^Test(KeyContract|SharedPersonalKeyBudgetHTTP|PersonalKeysIsolation|ModelLimitChain|TeamlessPersonalKeys)'");
  assert.ok(job.steps.every(item => !item['continue-on-error']), '门禁不得忽略失败');
  const cleanup = job.steps.find(item => item.run === 'docker compose down -v');
  assert.equal(cleanup.if, 'always()');
  const artifact = job.steps.find(item => item.uses === 'actions/upload-artifact@v4');
  assert.equal(artifact.if, 'always()');
  assert.equal(artifact.with['if-no-files-found'], 'error');
});

/** 五个真实 HTTP/PG 契约作业必须产出各自覆盖率并上传 backend，避免处理器执行证据遗漏。
 * 前置真实 YAML；检查插桩、报告校验、精确上传与诊断归档，失败不允许跳过，纯配置测试无数据需清理。 */
test('real contract regression coverage is validated archived and uploaded', () => {
  for (const area of ['supplier', 'user', 'key', 'model', 'usage']) {
    const job = `${area}-contract`;
    const profile = `coverage/${area}-contract.out`;
    const run = step(job, `Run real ${area} contract regression`);
    assert.equal(run.env?.XHUB_REGRESSION_COVERPROFILE, profile, `${job} 未采集真实回归覆盖率`);
    const check = step(job, 'Validate regression coverage');
    assert.match(check.run, /set -euo pipefail/);
    assert.ok(check.run.includes(`test -s ${profile}`), '报告缺失必须失败');
    assert.ok(check.run.includes(`go tool cover -func=${profile}`), '报告必须可以被 Go 解析');
    const upload = step(job, 'Upload regression coverage');
    assert.equal(upload.uses, 'codecov/codecov-action@v5');
    assert.equal(upload.with.files, area === 'supplier' ? profile + ',coverage/search-config.out' : profile);
    assert.equal(upload.with.flags, 'backend');
    assert.equal(upload.with.name, job);
    assert.equal(upload.with.disable_search, true);
    assert.equal(upload.with.fail_ci_if_error, true);
    assert.equal(upload.with.use_oidc, true);
    assert.ok(!upload.if || upload.if === 'success()', '不上传失败或取消用例的覆盖率充当成功证据');
    const artifact = workflow.jobs[job].steps.find(item => item.uses === 'actions/upload-artifact@v4');
    assert.equal(artifact.if, 'always()');
    assert.ok(artifact.with.path.includes(profile));
    assert.ok(artifact.with.path.includes('.e2e/runs/'));
    assert.ok(workflow.jobs[job].steps.indexOf(run) < workflow.jobs[job].steps.indexOf(check));
    assert.ok(workflow.jobs[job].steps.indexOf(check) < workflow.jobs[job].steps.indexOf(upload));
  }
  assert.equal(codecov.coverage.status.patch.default.target, '70%');
  assert.equal(codecov.coverage.status.patch.default.threshold, '1%');
});

/** 执行工作流中的真实 shell 校验，确认缺失、空文件和损坏的覆盖率报告均失败。
 * 前置本机 Go/bash；每个作业在临时目录注入报告故障，验证非零退出；finally 删除目录，无仓库或数据库副作用。 */
test('regression coverage validation rejects missing empty and malformed reports', () => {
  for (const area of ['supplier', 'user', 'key', 'model', 'usage']) {
    const directory = mkdtempSync(join(tmpdir(), 'xhub-coverage-gate-'));
    try {
      mkdirSync(join(directory, 'coverage'));
      for (const content of [null, '', 'not a Go coverage profile\n']) {
        if (content !== null) writeFileSync(join(directory, `coverage/${area}-contract.out`), content);
        const result = spawnSync('bash', ['-c', step(`${area}-contract`, 'Validate regression coverage').run], {
          cwd: directory, encoding: 'utf8', timeout: 30000,
        });
        assert.equal(result.error, undefined, '校验命令必须实际执行');
        assert.equal(result.signal, null, '不能把超时当作报告校验失败');
        assert.notEqual(result.status, 0, `${area} 错误报告被当成成功: ${content}`);
      }
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  }
});

/** 模型契约作业必须执行真实协议/权限回归，生成器和校验失败阻断；只读工作流，无线上副作用。 */
test('model contract requires generated types and real deployment regression', () => {
  const job = workflow.jobs['model-contract'];
  assert.ok(job);
  assert.equal(step('model-contract', 'Check generated contract and TypeScript types').run, 'npm --prefix frontend run check:api:models');
  assert.equal(step('model-contract', 'Test generator drift rejection').run, 'node --test frontend/scripts/gen-model-contract.test.mjs');
  assert.equal(step('model-contract', 'Validate OpenAPI and Go types').run, 'go test -race ./contracts/models');
  assert.equal(step('model-contract', 'Run real model contract regression').run, "bash scripts/regression.sh '^Test(ModelContract|OfficialCredentialSupportsNativeModel)'");
  assert.ok(job.steps.every(item => !item['continue-on-error']));
  assert.equal(job.steps.find(item => item.run === 'docker compose down -v').if, 'always()');
});

/** 日志契约作业必须实际执行HTTP/PG验证及清单漂移检查；只读YAML，不接触线上。 */
test('usage contract includes real traffic and inventory gates', () => {
 assert.equal(step('usage-contract', 'Run real usage contract regression').run, "bash scripts/regression.sh '^TestUsageContractRealTraffic$'");
 assert.match(step('usage-contract', 'Check core migration inventory').run, /contract-inventory.mjs --check/);
});

/** 兼容门禁必须读取真实历史、固定工具、运行失败反例并阻断WARN；只读，不降低原覆盖率门槛。 */
test('compatibility gate compares historical contracts with real pinned checker', () => {
  const job = workflow.jobs['contract-compatibility'];
  assert.equal(job.steps.find(s => s.uses === 'actions/checkout@v4').with['fetch-depth'], 0);
  assert.equal(step('contract-compatibility', 'Install pinned compatibility checker').run,
    'go install github.com/oasdiff/oasdiff@v1.11.7');
  assert.match(step('contract-compatibility', 'Test compatibility failure gates').run, /node --test .*compatibility.test.mjs/);
  const comparison = step('contract-compatibility', 'Compare contracts with baseline');
  assert.match(comparison.env.CONTRACT_BASE, /pull_request.base.sha/);
  assert.match(comparison.run, /set -euo pipefail/);
  assert.match(comparison.run, /check-contract-compatibility.mjs "\$base"/);
  assert.equal(codecov.coverage.status.patch.default.target, '70%');
});

/** 搜索配置服务复用真实数据库作业；验证回归与覆盖率上传不可遗漏，不降低门禁，只读无需清理。 */
test('search configuration service requires real regression coverage', () => {
  const run = step('supplier-contract', 'Run real search configuration regression');
  assert.equal(run.run, "bash scripts/regression.sh '^Test(SearchConfigService|SearchProbe|CatalogIntegrity)'");
  assert.equal(run.env.XHUB_REGRESSION_COVERPROFILE, 'coverage/search-config.out');
  assert.match(step('supplier-contract', 'Validate regression coverage').run, /test -s coverage\/search-config.out/);
  const upload = step('supplier-contract', 'Upload regression coverage');
  assert.ok(upload.with.files.split(',').includes('coverage/search-config.out'));
  assert.equal(upload.with.fail_ci_if_error, true);
  const artifact = workflow.jobs['supplier-contract'].steps.find(item => item.uses === 'actions/upload-artifact@v4');
  assert.ok(artifact.with.path.includes('coverage/search-config.out'));
});

/** 提示词服务真实数据库回归必须产出并上传有效覆盖率，失败不得忽略；配置只读，无清理。 */
test('prompt configuration has real regression coverage and cleanup', () => {
  const job = workflow.jobs['supplier-contract'];
  const run = step('supplier-contract', 'Run real prompt configuration regression');
  assert.equal(run.run, "bash scripts/regression.sh '^Test(PromptConfigService|PromptProbe)'");
  assert.equal(run.env.XHUB_REGRESSION_COVERPROFILE, 'coverage/prompt-config.out');
  const check = step('supplier-contract', 'Validate prompt regression coverage');
  assert.match(check.run, /set -euo pipefail/);
  assert.match(check.run, /test -s coverage\/prompt-config.out/);
  assert.match(check.run, /go tool cover -func=coverage\/prompt-config.out/);
  const upload = step('supplier-contract', 'Upload prompt regression coverage');
  assert.equal(upload.with.files, 'coverage/prompt-config.out');
  assert.equal(upload.with.fail_ci_if_error, true);
  assert.equal(upload.with.disable_search, true);
  assert.equal(upload.with.flags, 'backend');
  assert.ok(job.steps.indexOf(run) < job.steps.indexOf(check));
  assert.ok(job.steps.indexOf(check) < job.steps.indexOf(upload));
  assert.ok(!upload.if || upload.if === 'success()');
  const artifact = step('supplier-contract', 'Archive prompt regression evidence');
  assert.equal(artifact.if, 'always()');
  assert.match(artifact.with.path, /coverage\/prompt-config.out/);
  assert.equal(artifact.with['if-no-files-found'], 'error');
  assert.ok(job.steps.every(item => !item['continue-on-error']));
  assert.equal(job.steps.find(item => item.run === 'docker compose down -v').if, 'always()');
});

/** 护栏服务真实数据库回归必须产出并上传有效覆盖率，失败不得忽略；配置只读，无清理。 */
test('guard management has real regression coverage and cleanup', () => {
  const job = workflow.jobs['supplier-contract'];
  const run = step('supplier-contract', 'Run real guard management regression');
  assert.equal(run.run, "bash scripts/regression.sh '^Test(GuardManagementService|Guardrail|XGoGuardrail)'");
  assert.equal(run.env.XHUB_REGRESSION_COVERPROFILE, 'coverage/guard-management.out');
  const check = step('supplier-contract', 'Validate guard regression coverage');
  assert.match(check.run, /set -euo pipefail/);
  assert.match(check.run, /test -s coverage\/guard-management.out/);
  assert.match(check.run, /go tool cover -func=coverage\/guard-management.out/);
  const upload = step('supplier-contract', 'Upload guard regression coverage');
  assert.equal(upload.with.files, 'coverage/guard-management.out');
  assert.equal(upload.with.fail_ci_if_error, true);
  assert.equal(upload.with.disable_search, true);
  assert.equal(upload.with.flags, 'backend');
  assert.ok(job.steps.indexOf(run) < job.steps.indexOf(check));
  assert.ok(job.steps.indexOf(check) < job.steps.indexOf(upload));
  assert.ok(!upload.if || upload.if === 'success()');
  const artifact = step('supplier-contract', 'Archive guard regression evidence');
  assert.equal(artifact.if, 'always()');
  assert.match(artifact.with.path, /coverage\/guard-management.out/);
  assert.equal(artifact.with['if-no-files-found'], 'error');
  assert.ok(job.steps.every(item => !item['continue-on-error']));
  assert.equal(job.steps.find(item => item.run === 'docker compose down -v').if, 'always()');
});

/** 策略服务真实数据库回归必须产出并上传有效覆盖率，失败不得忽略；配置只读，无清理。 */
test('policy configuration has real regression coverage and cleanup', () => {
  const job = workflow.jobs['supplier-contract'];
  const run = step('supplier-contract', 'Run real policy configuration regression');
  assert.equal(run.run, "bash scripts/regression.sh '^TestPolicyConfigService'");
  assert.equal(run.env.XHUB_REGRESSION_COVERPROFILE, 'coverage/policy-config.out');
  const check = step('supplier-contract', 'Validate policy regression coverage');
  assert.match(check.run, /set -euo pipefail/);
  assert.match(check.run, /test -s coverage\/policy-config.out/);
  assert.match(check.run, /go tool cover -func=coverage\/policy-config.out/);
  const upload = step('supplier-contract', 'Upload policy regression coverage');
  assert.equal(upload.with.files, 'coverage/policy-config.out');
  assert.equal(upload.with.fail_ci_if_error, true);
  assert.equal(upload.with.disable_search, true);
  assert.equal(upload.with.flags, 'backend');
  assert.ok(job.steps.indexOf(run) < job.steps.indexOf(check));
  assert.ok(job.steps.indexOf(check) < job.steps.indexOf(upload));
  assert.ok(!upload.if || upload.if === 'success()');
  const artifact = step('supplier-contract', 'Archive policy regression evidence');
  assert.equal(artifact.if, 'always()');
  assert.match(artifact.with.path, /coverage\/policy-config.out/);
  assert.equal(artifact.with['if-no-files-found'], 'error');
  assert.ok(job.steps.every(item => !item['continue-on-error']));
  assert.equal(job.steps.find(item => item.run === 'docker compose down -v').if, 'always()');
});

/** 访问组服务真实数据库回归必须产出并上传有效覆盖率，失败不得忽略；配置只读，无清理。 */
test('access group configuration has real regression coverage and cleanup', () => {
  const job = workflow.jobs['supplier-contract'];
  const run = step('supplier-contract', 'Run real access group configuration regression');
  assert.equal(run.run, "bash scripts/regression.sh '^TestAccessGroupConfigService'");
  assert.equal(run.env.XHUB_REGRESSION_COVERPROFILE, 'coverage/access-group-config.out');
  const check = step('supplier-contract', 'Validate access group regression coverage');
  assert.match(check.run, /set -euo pipefail/);
  assert.match(check.run, /test -s coverage\/access-group-config.out/);
  assert.match(check.run, /go tool cover -func=coverage\/access-group-config.out/);
  const upload = step('supplier-contract', 'Upload access group regression coverage');
  assert.equal(upload.with.files, 'coverage/access-group-config.out');
  assert.equal(upload.with.fail_ci_if_error, true);
  assert.equal(upload.with.disable_search, true);
  assert.equal(upload.with.flags, 'backend');
  assert.ok(job.steps.indexOf(run) < job.steps.indexOf(check));
  assert.ok(job.steps.indexOf(check) < job.steps.indexOf(upload));
  assert.ok(!upload.if || upload.if === 'success()');
  const artifact = step('supplier-contract', 'Archive access group regression evidence');
  assert.equal(artifact.if, 'always()');
  assert.match(artifact.with.path, /coverage\/access-group-config.out/);
  assert.equal(artifact.with['if-no-files-found'], 'error');
  assert.ok(job.steps.every(item => !item['continue-on-error']));
  assert.equal(job.steps.find(item => item.run === 'docker compose down -v').if, 'always()');
});

/** 向量库配置服务真实数据库回归必须产出并上传有效覆盖率，失败不得忽略；配置只读，无清理。 */
test('vector configuration has real regression coverage and cleanup', () => {
  const job = workflow.jobs['supplier-contract'];
  const run = step('supplier-contract', 'Run real vector configuration regression');
  assert.equal(run.run, "bash scripts/regression.sh '^TestVectorConfigService'");
  assert.equal(run.env.XHUB_REGRESSION_COVERPROFILE, 'coverage/vector-config.out');
  const check = step('supplier-contract', 'Validate vector regression coverage');
  assert.match(check.run, /set -euo pipefail/);
  assert.match(check.run, /test -s coverage\/vector-config.out/);
  assert.match(check.run, /go tool cover -func=coverage\/vector-config.out/);
  const upload = step('supplier-contract', 'Upload vector regression coverage');
  assert.equal(upload.with.files, 'coverage/vector-config.out');
  assert.equal(upload.with.fail_ci_if_error, true);
  assert.equal(upload.with.disable_search, true);
  assert.equal(upload.with.flags, 'backend');
  assert.ok(job.steps.indexOf(run) < job.steps.indexOf(check));
  assert.ok(job.steps.indexOf(check) < job.steps.indexOf(upload));
  assert.ok(!upload.if || upload.if === 'success()');
  const artifact = step('supplier-contract', 'Archive vector regression evidence');
  assert.equal(artifact.if, 'always()');
  assert.match(artifact.with.path, /coverage\/vector-config.out/);
  assert.equal(artifact.with['if-no-files-found'], 'error');
  assert.ok(job.steps.every(item => !item['continue-on-error']));
  assert.equal(job.steps.find(item => item.run === 'docker compose down -v').if, 'always()');
});

/** 标签真实HTTP/数据库回归必须独立采集并上传backend覆盖率，缺失报告或清理失败不得通过；纯工作流结构验证，无外部副作用。 */
test('tag configuration has real regression coverage and cleanup', () => {
  const job = workflow.jobs['supplier-contract'];
  const run = step('supplier-contract', 'Run real tag configuration regression');
  assert.equal(run.run, "bash scripts/regression.sh '^TestTagCatalog'");
  assert.equal(run.env.XHUB_REGRESSION_COVERPROFILE, 'coverage/tag-catalog.out');
  const check = step('supplier-contract', 'Validate tag regression coverage');
  assert.match(check.run, /set -euo pipefail/);
  assert.match(check.run, /test -s coverage\/tag-catalog.out/);
  assert.match(check.run, /go tool cover -func=coverage\/tag-catalog.out/);
  const upload = step('supplier-contract', 'Upload tag regression coverage');
  assert.equal(upload.with.files, 'coverage/tag-catalog.out');
  assert.equal(upload.with.fail_ci_if_error, true);
  assert.equal(upload.with.disable_search, true);
  assert.equal(upload.with.flags, 'backend');
  assert.ok(job.steps.indexOf(run) < job.steps.indexOf(check));
  assert.ok(job.steps.indexOf(check) < job.steps.indexOf(upload));
  assert.ok(!upload.if || upload.if === 'success()');
  const artifact = step('supplier-contract', 'Archive tag regression evidence');
  assert.equal(artifact.if, 'always()');
  assert.match(artifact.with.path, /coverage\/tag-catalog.out/);
  assert.equal(artifact.with['if-no-files-found'], 'error');
  assert.ok(job.steps.every(item => !item['continue-on-error']));
  assert.equal(job.steps.find(item => item.run === 'docker compose down -v').if, 'always()');
});
