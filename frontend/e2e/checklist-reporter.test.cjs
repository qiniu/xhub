const {test} = require('node:test');
const assert = require('node:assert/strict');
const Reporter = require('./checklist-reporter.cjs');
/** 目的：组织、用量及日志双语回归必须登记精确文件/标题；前置静态清单，验证六个独立场景，无文件写入或清理。 */
test('组织、用量与日志六个场景全部登记且文件存在', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const catalog = require('./scenario-catalog.json');
  const {evaluateScenarios} = require('./scenario-coverage.cjs');
  const results = [];
  for (const [name,title] of [['organization-management','organization budgets filters and roles '],['usage-error','usage errors fallback partial pages and retry '],['log-cost-reasoning','log costs and reasoning effort ']]) {
    for (const locale of ['en','zh-CN']) {
      const file = `e2e/scenarios/regression/${name}-${locale}.test.ts`;
      assert.ok(fs.existsSync(path.join(__dirname,'..',file)), file);
      results.push({file,title:title+locale,status:'passed'});
    }
  }
  const report=evaluateScenarios(catalog,results);
  assert.deepEqual(report.unregistered,[]);
  assert.equal(report.passed,6);
});
/** 目的：全局启动失败必须可见且不暴露凭据；前置内存错误对象，验证普通/空错误及敏感值，finally 恢复输出，无外部副作用。 */
test('启动错误可见且隐藏连接串和凭据', () => {
  const messages = [];
  const original = console.error;
  console.error = message => messages.push(message);
  try {
    const reporter = new Reporter();
    reporter.onError({message: 'Timed out waiting 180000ms from config.webServer.\nprivate stack'});
    reporter.onError({message: 'Failed postgres://user:password@host/db Bearer session-secret sk-private-key'});
    reporter.onError(undefined);
    assert.match(messages[0], /Timed out waiting 180000ms/);
    assert.doesNotMatch(messages[0], /private stack/);
    assert.doesNotMatch(messages[1], /password|session-secret|sk-private-key/);
    assert.match(messages[2], /Unknown runner error/);
  } finally { console.error = original; }
});
/** 目的：执行前列出完整业务清单且跳过/超时不能显示通过；前置内存测试树，断言编号和全部结果类型，finally 恢复输出，无外部数据。 */
test('逐项显示业务内容并区分通过、失败与未执行', () => {
  const logs = [];
  const original = console.log;
  console.log = message => logs.push(message);
  try {
    const reporter = new Reporter();
    const cases = [{title: '密钥完整生命周期'}, {title: '错误日志详情'}];
    reporter.onBegin({}, {allTests: () => cases});
    reporter.onTestBegin(cases[0]);
    for (const status of ['passed', 'failed', 'timedOut', 'skipped', 'interrupted']) {
      reporter.onTestEnd(cases[0], {status});
    }
    assert.match(logs[0], /待测 001\/002.*密钥完整生命周期/);
    assert.match(logs[1], /待测 002\/002.*错误日志详情/);
    assert.match(logs[2], /开始 001\/002/);
    for (const [index, status] of ['通过', '失败', '超时', '未执行', '中断'].entries()) {
      assert.match(logs[index + 3], new RegExp('浏览器' + status));
    }
    const empty = new Reporter();
    const count = logs.length;
    empty.onBegin({}, {allTests: () => []});
    assert.equal(logs.length, count);
  } finally { console.log = original; }
});

/** 目的：完整运行缺失业务时返回失败并落盘固定分母；前置临时目录/内存测试树，finally 恢复环境并删除证据。 */
test('完整门禁不能用过滤运行缩小业务分母', () => {
  const fs = require('node:fs');
  const os = require('node:os');
  const path = require('node:path');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'xhub-scenario-reporter-'));
  const oldRun = process.env.E2E_RUN_DIR;
  const oldFull = process.env.E2E_FULL_COVERAGE;
  const original = console.log;
  try {
    console.log = () => {};
    process.env.E2E_RUN_DIR = dir;
    process.env.E2E_FULL_COVERAGE = '1';
    const reporter = new Reporter();
    reporter.onBegin({rootDir: __dirname}, {allTests: () => []});
    assert.deepEqual(reporter.onEnd({status:'passed'}), {status:'failed'});
    const report = JSON.parse(fs.readFileSync(path.join(dir,'scenario-coverage.json'),'utf8'));
    assert.equal(report.total, require('./scenario-catalog.json').length);
    assert.equal(report.passed, 0);
    assert.equal(report.unmet.length, report.total);
    assert.ok(report.unmet.every(row => row.state === 'missing'));
  } finally {
    console.log = original;
    if (oldRun === undefined) delete process.env.E2E_RUN_DIR; else process.env.E2E_RUN_DIR = oldRun;
    if (oldFull === undefined) delete process.env.E2E_FULL_COVERAGE; else process.env.E2E_FULL_COVERAGE = oldFull;
    fs.rmSync(dir,{recursive:true,force:true});
  }
});

/** 目的：共享注册函数不能把文件清单绑定到 helper；前置模拟文件 suite，验证精确 .test.ts、标题与一次通过；finally 删除临时报告。 */
test('共享 helper 注册仍绑定独立测试文件', () => {
  const fs = require('node:fs');
  const os = require('node:os');
  const path = require('node:path');
  const {reportCoverage} = require('./scenario-coverage.cjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(),'xhub-file-binding-'));
  const file = path.join(__dirname,'scenarios/example.test.ts');
  const fileSuite = {type:'file',location:{file},parent:null};
  const nested = {type:'describe',parent:fileSuite};
  try {
    const report = reportCoverage([{id:'a',file:'e2e/scenarios/example.test.ts',title:'独立业务'}], [{title:'独立业务',location:{file:path.join(__dirname,'example-helper.ts')},parent:nested,results:[{status:'passed'}]}], path.join(dir,'report.json'));
    assert.equal(report.passed,1);
    assert.deepEqual(report.unregistered,[]);
    const duplicate = reportCoverage([{id:'a',file:'e2e/scenarios/example.test.ts',title:'独立业务'}], [{title:'独立业务',location:{file},parent:fileSuite,results:[{status:'failed'},{status:'passed'}]}],path.join(dir,'retry.json'));
    assert.equal(duplicate.passed,0);
    assert.equal(duplicate.unmet[0].state,'retried');
  } finally {fs.rmSync(dir,{recursive:true,force:true});}
});

/** 目的：部分运行也必须执行至少一个真实通过场景，零匹配/全跳过/未执行不能通过；临时报告与环境在 finally 清理。 */
test('部分运行拒绝零执行和全跳过', () => {
  const fs = require('node:fs');
  const os = require('node:os');
  const path = require('node:path');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'xhub-empty-reporter-'));
  const oldRun = process.env.E2E_RUN_DIR;
  const oldFull = process.env.E2E_FULL_COVERAGE;
  const original = console.log;
  try {
    console.log = () => {};
    process.env.E2E_RUN_DIR = dir;
    process.env.E2E_FULL_COVERAGE = '0';
    const item = require('./scenario-catalog.json')[0];
    for (const statuses of [null, [], ['skipped'], ['passed']]) {
      const reporter = new Reporter();
      const cases = statuses === null ? [] : [{title: item.title, location: {file: path.join(__dirname, '..', item.file)}, results: statuses.map(status => ({status}))}];
      reporter.onBegin({rootDir: __dirname}, {allTests: () => cases});
      const result = reporter.onEnd({status: 'passed'});
      if (statuses?.[0] === 'passed') assert.equal(result, undefined);
      else assert.deepEqual(result, {status: 'failed'}, '缺少真实通过测试不能返回成功');
    }
  } finally {
    console.log = original;
    if (oldRun === undefined) delete process.env.E2E_RUN_DIR; else process.env.E2E_RUN_DIR = oldRun;
    if (oldFull === undefined) delete process.env.E2E_FULL_COVERAGE; else process.env.E2E_FULL_COVERAGE = oldFull;
    fs.rmSync(dir, {recursive: true, force: true});
  }
});

/** 目的：真实 Playwright 进程验证 reporter 返回值确实影响退出码；临时纯断言用例不启动产品服务。
 * 覆盖正常、零匹配（显式允许 no-tests）、全跳过和失败；finally 删除全部脚本与运行证据。 */
test('真实 Playwright 执行拒绝空跑和全跳过', () => {
  const fs = require('node:fs');
  const os = require('node:os');
  const path = require('node:path');
  const {spawnSync} = require('node:child_process');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'xhub-real-playwright-gate-'));
  try {
    fs.writeFileSync(path.join(dir, 'fixture.spec.js'), `const {test, expect} = require(${JSON.stringify(require.resolve('@playwright/test'))});
      test('passed', () => { expect(2 + 2).toBe(4); });
      test.skip('optional', () => {});
      test('failed', () => { expect(2 + 2).toBe(5); });`);
    fs.writeFileSync(path.join(dir, 'playwright.config.cjs'), `module.exports = ${JSON.stringify({
      testDir: dir, testMatch: 'fixture.spec.js', outputDir: path.join(dir, 'artifacts'),
      reporter: [[path.join(__dirname, 'checklist-reporter.cjs')]], workers: 1, retries: 0,
    })};`);
    for (const [name, status] of [['passed', 0], ['missing', 1], ['optional', 1], ['failed', 1]]) {
      const result = spawnSync(process.execPath, [require.resolve('@playwright/test/cli'), 'test',
        '--config', path.join(dir, 'playwright.config.cjs'), '--grep', `(?:^| )${name}$`, '--pass-with-no-tests'], {
        cwd: dir, encoding: 'utf8', timeout: 30000,
        env: {...process.env, E2E_RUN_DIR: path.join(dir, name), E2E_FULL_COVERAGE: '0'},
      });
      assert.ifError(result.error);
      assert.equal(result.status, status, `${name}: ${result.stdout}\n${result.stderr}`);
      assert.match(result.stdout, /业务场景覆盖/, '必须真正执行报告器，不能把加载故障算作成功门禁');
    }
  } finally { fs.rmSync(dir, {recursive: true, force: true}); }
});

/** 目的：Node CommonJS 工具允许原生 require，但未知变量仍必须报错；内存 lint 三组输入，无文件副作用。 */
test('reporter 的 lint 使用真实 Node 环境且保留错误门禁', async () => {
  const {ESLint} = require('eslint');
  const path = require('node:path');
  const eslint = new ESLint({cwd: path.join(__dirname, '..')});
  const filePath = path.join(__dirname, 'runtime-fixture.cjs');
  const [valid] = await eslint.lintText('const fs = require("node:fs"); module.exports = () => fs.existsSync(__dirname); console.error(process.pid);', {filePath});
  assert.equal(valid.errorCount, 0, JSON.stringify(valid.messages));
  const [invalid] = await eslint.lintText('module.exports = () => missingRuntimeVariable;', {filePath});
  assert.ok(invalid.messages.some(item => item.ruleId === 'no-undef'), '不能关闭未声明变量检查');
  const [esm] = await eslint.lintText('const fs = require("node:fs"); export default fs;', {filePath: path.join(__dirname, 'runtime-fixture.ts')});
  assert.ok(esm.messages.some(item => item.ruleId === '@typescript-eslint/no-require-imports'), 'CommonJS 规则不得扩散到 TypeScript');
});

/** 目的：Swagger双语结果必须绑定清单中的独立文件和精确标题；模拟通过/失败，验证登记及失败不可计通过；无外部资源。 */
test('Swagger双语场景登记且失败不能假通过', () => {
  const {evaluateScenarios} = require('./scenario-coverage.cjs');
  const catalog = require('./scenario-catalog.json');
  const results = [['en','en'],['zh-CN','zh']].map(([file,lang]) => ({file:`e2e/scenarios/regression/swagger-reference-${file}.test.ts`, title:`Swagger reference real user lifecycle ${lang}`, status:'passed'}));
  assert.deepEqual(evaluateScenarios(catalog,results).unregistered,[]);
  assert.equal(evaluateScenarios(catalog,results).passed,2);
  results[0].status='failed';
  assert.equal(evaluateScenarios(catalog,results).passed,1);
});

/** 护栏双语闭环必须进入固定全量分母；前置真实目录，验证精确文件/标题与未知项拒绝，无外部数据。 */
test('护栏管理双语场景登记且不漏入全量分母', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const catalog = require('./scenario-catalog.json');
  const {evaluateScenarios} = require('./scenario-coverage.cjs');
  const results = ['en','zh-CN'].map(locale => ({
    file: `e2e/scenarios/regression/guard-management-service-${locale}.test.ts`,
    title: 'guard management service lifecycle ' + locale, status: 'passed',
  }));
  for (const result of results) assert.ok(fs.existsSync(path.join(__dirname,'..',result.file)));
  const report = evaluateScenarios(catalog, results);
  assert.deepEqual(report.unregistered, []);
  assert.equal(report.passed, 2);
  assert.equal(catalog.filter(row => results.some(result => result.file === row.file && result.title === row.title)).length, 2);
  assert.equal(evaluateScenarios(catalog, [{...results[0],title:'unknown guard scenario'}]).unregistered.length, 1);
});

/** 策略双语场景须登记精确标题，防止局部通过而全量漏测；只读清单无待清理资源。 */
test('policy configuration scenarios are registered', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const {evaluateScenarios} = require('./scenario-coverage.cjs');
  const catalog = require('./scenario-catalog.json');
  const results = ['en','zh-CN'].map(locale => {
    const file = `e2e/scenarios/regression/policy-config-service-${locale}.test.ts`;
    assert.ok(fs.existsSync(path.join(__dirname, '..', file)));
    return {file, title: 'policy configuration persistence and isolation ' + locale, status: 'passed'};
  });
  const report = evaluateScenarios(catalog, results);
  assert.deepEqual(report.unregistered, []);
  assert.equal(report.passed, 2);
});

/** 双语标签真实场景必须登记独立文件和真实标题，避免新测试通过却未进入业务清单；纯内存校验无需清理。 */
test('tag catalog scenarios are registered independently', () => {
 const {evaluateScenarios}=require('./scenario-coverage.cjs');
 const catalog=require('./scenario-catalog.json');
 const results=['en','zh-CN'].map(locale=>({file:`e2e/scenarios/regression/tag-catalog-${locale}.test.ts`,title:`tag catalog read after write ${locale}`,status:'passed'}));
 const report=evaluateScenarios(catalog,results);
 assert.deepEqual(report.unregistered,[]);
 assert.equal(report.passed,2);
});
