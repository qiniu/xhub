const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const yaml = require('../frontend/node_modules/js-yaml');

/** 校验维护策略；参数为解析后的配置，返回无值，缺少目录、调度、安全分组或重新启用普通升级时抛错；配置回归使用，纯读取无需清理。 */
function assertPolicy(config) {
  assert.equal(config.version, 2);
  assert.deepEqual(config.updates.map(item => `${item['package-ecosystem']}:${item.directory}`).sort(),
    ['gomod:/', 'npm:/frontend', 'npm:/website']);
  for (const entry of config.updates) {
    assert.deepEqual(entry.schedule, { interval: 'weekly', day: 'monday', time: '09:00', timezone: 'Asia/Shanghai' });
    assert.equal(entry['open-pull-requests-limit'], 0, '普通版本升级必须保持暂停');
    assert.equal(entry['target-branch'], undefined, '安全更新必须针对默认分支');
    assert.equal(entry.ignore, undefined, '不能通过忽略规则屏蔽安全修复');
    const groups = Object.values(entry.groups);
    assert.equal(groups.length, 1, '只保留安全修复分组');
    const group = groups[0];
    assert.equal(group['applies-to'], 'security-updates', '安全分组必须显式限定为安全更新');
    assert.deepEqual(group.patterns, ['*']);
    assert.deepEqual(group['update-types'], ['minor', 'patch'], '主版本安全修复必须独立审核');
  }
}

const config = yaml.load(readFileSync(join(__dirname, '../.github/dependabot.yml'), 'utf8'));

/** 验证真实配置覆盖三处依赖、暂停普通更新并保留即时安全组；前置仓库 YAML，结果满足维护策略，无外部请求和清理。 */
test('Dependabot pauses version updates and retains security groups', () => assertPolicy(config));

/** 验证缺失目录、重新启用普通升级、错误调度、主版本混入和屏蔽安全修复均被拒绝；输入为独立副本，断言抛错，无持久副作用。 */
test('Dependabot policy rejects missing coverage and unsafe grouping', () => {
  const mutations = [
    value => value.updates.pop(),
    value => { value.updates[0]['open-pull-requests-limit'] = 3; },
    value => { delete value.updates[0]['open-pull-requests-limit']; },
    value => { value.updates[0]['target-branch'] = 'dev'; },
    value => { value.updates[0].schedule.interval = 'daily'; },
    value => { Object.values(value.updates[0].groups)[0]['update-types'].push('major'); },
    value => { value.updates[0].ignore = [{ 'dependency-name': '*' }]; },
    value => { delete value.updates[0].groups['frontend-security']; },
    value => { delete value.updates[0].groups['frontend-security']['applies-to']; },
  ];
  for (const mutate of mutations) {
    const candidate = structuredClone(config);
    mutate(candidate);
    assert.throws(() => assertPolicy(candidate));
  }
});
