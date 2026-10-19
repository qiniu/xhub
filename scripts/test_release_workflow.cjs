/** 发布配置回归：读取真实 YAML 并执行内嵌 shell；外部发布使用临时夹具。 */
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync, mkdtempSync, mkdirSync, writeFileSync, rmSync, existsSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { spawnSync } = require('node:child_process');
const yaml = require('../frontend/node_modules/js-yaml');
const workflow = yaml.load(readFileSync(join(__dirname, '../.github/workflows/release.yml'), 'utf8'));
const publish = workflow.jobs.publish;

/** 前置真实发布 YAML；验证升级及监控权限双语闭环随发布运行且使用隔离 Redis，防止 CI 遗漏权限回归；只读配置无需清理。 */
test('verification runs bilingual usage upgrade and monitor permission scenarios with isolated Redis', () => {
  const browser = workflow.jobs.verify.steps.find(item => (item.run || '').startsWith('bash scripts/e2e.sh '));
  assert.ok(browser, '发布必须运行真实浏览器场景');
  for (const locale of ['en', 'zh-CN']) {
    assert.ok(browser.run.split(/\s+/).includes(`scenarios/permissions/model-monitor-${locale}.test.ts`),
      `发布前必须验证 ${locale} 普通用户监控权限，不能省略团队参数就泄露目录`);
    assert.ok(browser.run.split(/\s+/).includes(`scenarios/regression/usage-upgrade-${locale}.test.ts`),
      `发布缺少 ${locale} 旧库升级闭环`);
  }
  assert.equal(browser.env.E2E_REDIS, '1', '升级验收必须使用专属 Redis 真实结算链');
  assert.equal(browser.env.E2E_PORTABLE_CONSOLE, '1', '发布必须继续验证 HTTP 控制台代理');
});

/** 按名称返回发布步骤；参数为名称，缺失时抛出断言；供测试调用，无副作用。 */
function step(name) {
  const result = publish.steps.find(item => item.name === name);
  assert.ok(result, name);
  return result;
}

/** 前置干净 runner；验证本机 Redis 在 Go 与后台回归前安装且可执行，覆盖安装和二进制失败，finally 清理命令夹具。 */
test('verification installs isolated Redis before Go and backend regression tests and propagates failures', () => {
  const steps = workflow.jobs.verify.steps;
  const installIndex = steps.findIndex(item => item.name === 'Install regression Redis');
  const goIndex = steps.findIndex(item => /go test/.test(item.run || ''));
  const regressionIndex = steps.findIndex(item => item.name === 'Backend regression');
  assert.ok(installIndex >= 0 && installIndex < goIndex && installIndex < regressionIndex,
    '本机 redis-server 必须在依赖它的测试之前安装');
  const install = steps[installIndex];
  assert.equal(install.shell, 'bash');
  const directory = mkdtempSync(join(tmpdir(), 'xhub-release-redis-'));
  try {
    writeFileSync(join(directory, 'sudo'), '#!/bin/bash\nprintf "%s\\n" "$*" >> "$CALLS"\ncase "$*" in\n  "apt-get update") exit "$UPDATE_STATUS" ;;\n  "apt-get install -y --no-install-recommends redis-server") exit "$INSTALL_STATUS" ;;\n  *) exit 99 ;;\nesac\n', { mode: 0o755 });
    writeFileSync(join(directory, 'redis-server'), '#!/bin/bash\nprintf "redis-server %s\\n" "$*" >> "$CALLS"\n[[ "$*" == --version ]] || exit 99\nexit "$REDIS_STATUS"\n', { mode: 0o755 });
    for (const [updateStatus, installStatus, redisStatus, expectedCalls] of [
      [0, 0, 0, 3], [7, 0, 0, 1], [0, 8, 0, 2], [0, 0, 127, 3],
    ]) {
      const calls = join(directory, 'calls');
      writeFileSync(calls, '');
      const result = spawnSync('bash', ['-c', install.run], {
        cwd: directory, encoding: 'utf8',
        env: { ...process.env, PATH: directory + ':' + process.env.PATH, CALLS: calls,
          UPDATE_STATUS: String(updateStatus), INSTALL_STATUS: String(installStatus), REDIS_STATUS: String(redisStatus) },
      });
      assert.equal(result.status, updateStatus || installStatus || redisStatus,
        'Redis 准备失败必须中止 CI: ' + result.stderr);
      assert.deepEqual(readFileSync(calls, 'utf8').trim().split('\n'), [
        'apt-get update', 'apt-get install -y --no-install-recommends redis-server', 'redis-server --version',
      ].slice(0, expectedCalls), '准备步骤必须安装本机程序并验证可执行性，失败后不得继续');
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

/** 配置准备覆盖干净检出、已有配置和缺失示例；执行真实工作流 shell，验证生成内容、保留原配置及失败退出，finally 删除临时目录。 */
test('verification prepares configs/config.yaml before Go tests without overwriting existing configuration', () => {
  const steps = workflow.jobs.verify.steps;
  const prepareIndex = steps.findIndex(item => item.name === 'Prepare test configuration');
  const goIndex = steps.findIndex(item => /go test/.test(item.run || ''));
  assert.ok(prepareIndex >= 0 && prepareIndex < goIndex, '配置必须在 Go 测试前准备');
  const prepare = steps[prepareIndex];
  assert.equal(prepare.shell, 'bash');
  const example = readFileSync(join(__dirname, '../configs/config.example.yaml'), 'utf8');
  for (const scenario of ['clean checkout', 'existing configuration', 'missing example']) {
    const directory = mkdtempSync(join(tmpdir(), 'xhub-release-config-'));
    try {
      mkdirSync(join(directory, 'configs'));
      if (scenario !== 'missing example') {
        writeFileSync(join(directory, 'configs/config.example.yaml'), example);
      }
      const target = join(directory, 'configs/config.yaml');
      if (scenario === 'existing configuration') writeFileSync(target, 'model_list: []\n# preserved configuration\n');
      const result = spawnSync('bash', ['-c', prepare.run], { cwd: directory, encoding: 'utf8' });
      if (scenario === 'missing example') {
        assert.notEqual(result.status, 0, '缺失示例时必须报告准备失败');
        assert.equal(existsSync(target), false, '失败时不得留下空配置');
      } else {
        assert.equal(result.status, 0, scenario + ': ' + result.stderr);
        assert.equal(readFileSync(target, 'utf8'), scenario === 'clean checkout' ? example : 'model_list: []\n# preserved configuration\n', scenario);
      }
    } finally { rmSync(directory, { recursive: true, force: true }); }
  }
});

/** 正常流程验证发布依赖真实测试且 PR 没有写权限；只读配置，无需清理。 */
test('publication requires regression and browser verification', () => {
  assert.deepEqual(workflow.on.push, { branches: ['main'], tags: ['v*'] });
  assert.equal(publish.needs, 'verify');
  assert.match(publish.if, /github.event_name != 'pull_request'/);
  assert.deepEqual(workflow.permissions, { contents: 'read' });
  assert.deepEqual(publish.permissions, { contents: 'write', packages: 'write' });
  const commands = workflow.jobs.verify.steps.map(item => item.run || '').join('\n');
  assert.match(commands, /go test/);
  assert.match(commands, /bash scripts\/regression.sh -v/);
  assert.match(commands, /scenarios\/keys\/rotation-secret.test.ts/);
  assert.ok(workflow.jobs.verify.steps.some(item => item.run === 'docker compose down -v' && item.if === 'always()'));
});

/** 回归日志覆盖成功、大量失败输出、无换行长行及目录创建失败；执行真实工作流 shell，验证双流落盘、输出上限和原始退出码，finally 清理临时夹具。 */
test('backend regression stores complete logs and bounds console output without hiding failures', () => {
  const regression = workflow.jobs.verify.steps.find(item => item.name === 'Backend regression');
  assert.ok(regression, '后台回归步骤必须存在');
  assert.equal(regression.shell, 'bash');
  const directory = mkdtempSync(join(tmpdir(), 'xhub-release-regression-'));
  try {
    mkdirSync(join(directory, 'scripts'));
    writeFileSync(join(directory, 'scripts/regression.sh'), '#!/bin/bash\n[[ "$1" == -v ]] || exit 99\ncat "$FIXTURE_STDOUT"\ncat "$FIXTURE_STDERR" >&2\nexit "$FIXTURE_STATUS"\n');
    for (const [label, status, output] of [
      ['success', 0, 'trace request\n'.repeat(100000)],
      ['failure', 7, 'trace request\n'.repeat(100000)],
      ['long line', 2, 'x'.repeat(1500000)],
      ['empty failure', 1, ''],
    ]) {
      const stdoutPath = join(directory, 'stdout');
      const stderrPath = join(directory, 'stderr');
      const error = status === 0 ? 'diagnostic stderr\n' : 'specific regression failure\n';
      writeFileSync(stdoutPath, output);
      writeFileSync(stderrPath, error);
      const result = spawnSync('bash', ['-c', regression.run], {
        cwd: directory, encoding: 'utf8',
        env: { ...process.env, FIXTURE_STDOUT: stdoutPath, FIXTURE_STDERR: stderrPath, FIXTURE_STATUS: String(status) },
      });
      assert.equal(result.status, status, label + ': 必须保留回归退出码 ' + result.stderr);
      assert.equal(readFileSync(join(directory, '.e2e/release/regression.log'), 'utf8'), output + error, label + ': 必须完整保留标准输出与标准错误');
      assert.ok(Buffer.byteLength(result.stdout + result.stderr) < 17000, label + ': 控制台日志不得超过尾部字节上限与简短提示');
      assert.match(result.stdout, /release-test-results\/regression.log/, label + ': 必须说明完整日志位置');
      if (status === 0) {
        assert.match(result.stdout, /Backend regression passed/);
        assert.equal(result.stdout.includes('trace request'), false, '成功时不向控制台打印网关日志');
      } else {
        assert.match(result.stdout, /Backend regression failed/);
        assert.ok(result.stdout.endsWith(Buffer.from(output + error).subarray(-16384).toString()), label + ': 失败必须打印日志尾部诊断');
      }
    }
    rmSync(join(directory, '.e2e'), { recursive: true });
    writeFileSync(join(directory, '.e2e'), 'blocked');
    const result = spawnSync('bash', ['-c', regression.run], { cwd: directory, encoding: 'utf8' });
    assert.notEqual(result.status, 0, '无法保存日志时必须失败');
    assert.equal(result.stdout.includes('passed'), false, '日志目录创建失败不得报告通过');
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

/** 附件契约覆盖回归失败和浏览器未执行的场景；检查真实 YAML 中 always 上传及日志路径，纯读取无需清理。 */
test('verification always uploads backend regression logs with browser reports', () => {
  const steps = workflow.jobs.verify.steps;
  const regressionIndex = steps.findIndex(item => item.name === 'Backend regression');
  const uploadIndex = steps.findIndex(item => item.uses === 'actions/upload-artifact@v4');
  assert.ok(uploadIndex > regressionIndex, '日志附件上传必须位于回归之后');
  const upload = steps[uploadIndex];
  assert.equal(upload.if, 'always()', '回归失败后仍必须上传完整日志');
  assert.equal(upload.with.name, 'release-test-results');
  assert.ok(upload.with.path.split('\n').includes('.e2e/release/regression.log'), '附件必须包含完整回归日志');
  assert.ok(upload.with.path.split('\n').includes('.e2e/release/results.json'), '附件必须保留浏览器报告');
});

/** 边界覆盖正式版、预发布、非法及注入 ref；执行真实 shell，finally 清理临时输出。 */
test('version validation accepts valid versions and rejects malformed refs', () => {
  const directory = mkdtempSync(join(tmpdir(), 'xhub-release-version-'));
  try {
    for (const [version, accepted] of [
      ['v1.2.3', true], ['v0.0.0', true], ['v2.0.0-rc.1', true],
      ['v2.0.0-beta.0', true], ['v2.0.0-alpha.1', true],
      ['v01.2.3', false], ['v1.2', false], ['v1.2.3-rc.01', false],
      ['v1.2.3;touch injected', false], ['v1.2.3-unknown.1', false],
    ]) {
      const result = spawnSync('bash', ['-c', step('Validate version').run], {
        cwd: directory, encoding: 'utf8',
        env: { ...process.env, REF_TYPE: 'tag', REF_NAME: version, GITHUB_REPOSITORY: 'owner/xhub', GITHUB_OUTPUT: join(directory, 'output') },
      });
      assert.equal(result.status === 0, accepted, version + ': ' + result.stderr);
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

/** 镜像契约验证两个镜像、预发布 latest 隔离及 standalone 入口；只读无需清理。 */
test('both images preserve tags and standalone layout', () => {
  const builds = publish.steps.filter(item => item.uses === 'docker/build-push-action@v6');
  assert.equal(builds.length, 2);
  assert.ok(builds.every(item => item.with.push && item.with.platforms === 'linux/amd64'));
  const metadata = publish.steps.filter(item => item.uses === 'docker/metadata-action@v5');
  assert.equal(metadata.length, 2);
  assert.ok(metadata.every(item => item.with.flavor.includes("!contains(github.ref_name, '-')")));
  assert.match(step('Prepare runtime artifacts').run, /standalone\/frontend\/server.js/);
  assert.match(readFileSync(join(__dirname, '../deploy/Dockerfile.release'), 'utf8'), /\/app\/frontend\/server.js/);
  assert.equal(step('Publish GitHub Release').if, "github.ref_type == 'tag'");
});

/** 发布 shell 覆盖初次发布、重跑、预发布和上传失败；gh 夹具只记录命令，finally 清理。 */
test('release reruns are repeatable and upload failures propagate', () => {
  const directory = mkdtempSync(join(tmpdir(), 'xhub-release-publish-'));
  try {
    writeFileSync(join(directory, 'gh'), '#!/bin/bash\nprintf "%s\\n" "$*" >> "$CALLS"\ncase "$2" in view) exit "$EXISTS" ;; upload) exit "$UPLOAD_STATUS" ;; esac\n', { mode: 0o755 });
    for (const [version, exists, uploadStatus] of [['v1.2.3', '1', '0'], ['v1.2.3', '0', '0'], ['v2.0.0-rc.1', '1', '0'], ['v1.2.3', '0', '1']]) {
      const calls = join(directory, 'calls');
      writeFileSync(calls, '');
      const result = spawnSync('bash', ['-c', step('Publish GitHub Release').run], {
        cwd: directory, encoding: 'utf8', env: { ...process.env, PATH: directory + ':' + process.env.PATH,
          VERSION: version, REPOSITORY: 'owner/xhub', GATEWAY_DIGEST: 'sha256:gateway', CONSOLE_DIGEST: 'sha256:console',
          CALLS: calls, EXISTS: exists, UPLOAD_STATUS: uploadStatus },
      });
      assert.equal(result.status, Number(uploadStatus), result.stderr);
      const log = readFileSync(calls, 'utf8');
      assert.equal(log.includes('release create'), exists === '1');
      if (version.includes('-')) assert.match(log, /--prerelease --latest=false/);
      assert.match(log, /release upload .* docker-images.txt --clobber/);
      assert.equal(readFileSync(join(directory, 'docker-images.txt'), 'utf8'), 'ghcr.io/owner/xhub-gateway@sha256:gateway\nghcr.io/owner/xhub-console@sha256:console\n');
    }
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

/** 前置发布工作流；验证 CI 使用无构建地址的普通 HTTP 调试流程并运行对应单元测试；只读无需清理。 */
test('portable HTTP compatibility is required before publishing', () => {
  const steps = workflow.jobs.verify.steps;
  const browser = steps.find(item => /bash scripts\/e2e.sh/.test(item.run || ''));
  assert.equal(browser.env.E2E_PORTABLE_CONSOLE, '1');
  assert.match(browser.run, /scenarios\/legacy\/playground-workspace\/sc-109.test.ts/);
  assert.match(browser.run, /web-search-policy\.test\.ts/);
  const unit = steps.find(item => item.name === 'Console HTTP compatibility unit tests');
  assert.ok(unit);
  assert.match(unit.run, /resolveApiBase.test.ts/);
  assert.match(unit.run, /useChatHistory.test.ts/);
});

/** 前置真实发布工作流；验证固定版本的实际 CLI 进入后台回归、原生、图片上传、桥接及跨模型 ID/加密历史浏览器闭环同时进入发布门槛且证据 always 上传。只读配置，无外部依赖或清理。 */
test('agent compatibility uses pinned real clients and a governed browser flow', () => {
  const verify = workflow.jobs.verify.steps;
  const install = verify.findIndex(item => item.name === 'Install agent compatibility clients');
  const backend = verify.findIndex(item => item.name === 'Backend regression');
  assert.ok(install >= 0 && install < backend, '实际 CLI 必须在后台回归之前准备');
  assert.match(verify[install].run, /@openai\/codex@0\.163\.0/);
  assert.match(verify[install].run, /@anthropic-ai\/claude-code@2\.1\.300/);
  for (const [key, name] of [['XHUB_TEST_CODEX_BIN', 'codex'], ['XHUB_TEST_CLAUDE_BIN', 'claude']]) {
    assert.ok(verify[backend].env[key].endsWith('/node_modules/.bin/' + name), 'CI 不得因未配置二进制跳过实际 ' + name);
  }
  const browser = verify.find(item => (item.run || '').startsWith('bash scripts/e2e.sh '));
  assert.ok(browser.run.split(/\s+/).includes('agent-native-protocol.test.ts'), '发布缺少原生 Agent/工具续轮/计费浏览器闭环');
  assert.ok(browser.run.split(/\s+/).includes('messages-reasoning.test.ts'), '发布缺少 Messages 公开推理和工具续轮浏览器闭环');
  assert.ok(browser.run.split(/\s+/).includes('agent-chat-bridge.test.ts'), '发布缺少 Codex namespace/custom Chat 桥接闭环');
  assert.ok(browser.run.split(/\s+/).includes('responses-item-ids.test.ts'), '发布缺少跨模型 Responses item ID 回放闭环');
  assert.ok(browser.run.split(/\s+/).includes('portable-reasoning.test.ts'), '发布缺少跨模型加密历史可移植闭环');
  assert.ok(browser.run.split(/\s+/).includes('vision-input.test.ts'), '发布缺少图片上传与三协议识图闭环');
  assert.ok(browser.run.split(/\s+/).includes('chat-encrypted-reasoning.test.ts'), '发布缺少 Responses 密文经 Chat 扩展续轮的浏览器闭环');
  assert.ok(browser.run.split(/\s+/).includes('agent-proxy-timeout.test.ts'), '发布缺少超过30秒的非流式推理代理闭环');
  const artifact = verify.find(item => item.uses === 'actions/upload-artifact@v4');
  assert.equal(artifact.if, 'always()');
  assert.ok(artifact.with.path.includes('.e2e/release/agent-clients/'), '必须保留实际 CLI 证据');
});
