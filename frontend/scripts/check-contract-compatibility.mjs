import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

/** 执行无shell子进程，返回UTF-8输出；非零退出/工具缺失直接失败，供基线读取和真实oasdiff检查调用。 */
function run(command, args, cwd) {
  return execFileSync(command, args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
}

/** 枚举工作树契约，返回排序后的相对路径；目录缺失必须失败，防止把整个契约目录删除当成无变更。 */
function currentSpecs(root) {
  return readdirSync(join(root, 'contracts'), { withFileTypes: true })
    .filter(e => e.isDirectory() && readdirSync(join(root, 'contracts', e.name)).includes('openapi.json'))
    .map(e => `contracts/${e.name}/openapi.json`).sort();
}

/** 检查本仓单文件OpenAPI来源；只接受本地片段引用，防止CI读取远程/本机文件；返回操作数，缺源/零操作/非法JSON即失败。 */
function validateSource(text, path) {
  const spec = JSON.parse(text);
  if (spec.openapi !== '3.0.3' || !spec.paths) throw new Error(`Invalid contract: ${path}`);
  const refs = JSON.stringify(spec).matchAll(/"\$ref":"([^"]+)"/g);
  for (const [, ref] of refs) if (!ref.startsWith('#/')) throw new Error(`Non-local reference: ${path}`);
  const count = Object.values(spec.paths).reduce((n, methods) => n + Object.keys(methods).filter(m => /^(get|post|put|patch|delete|head|options|trace)$/.test(m)).length, 0);
  if (!count) throw new Error(`Empty contract: ${path}`);
  return count;
}

/** 对显式Git基线和当前工作树逐域执行固定工具的兼容检查；参数root/base/binary来自CLI或测试。
 * 返回每域操作数和工具报告；基线不存在、零匹配、契约删除、工具异常、WARN/ERR均失败。
 * 新领域没有旧客户端契约，执行自身比较验证工具可解析；临时文件在所有路径严格删除，不改变生产或Git状态。
 */
export function checkCompatibility(root, base, binary = 'oasdiff') {
  if (!base || !/^[a-zA-Z0-9_./-]+$/.test(base) || base.startsWith('-')) throw new Error('Explicit valid baseline required');
  const sha = run('git', ['rev-parse', '--verify', `${base}^{commit}`], root).trim();
  const previous = run('git', ['ls-tree', '-r', '--name-only', sha, 'contracts'], root).trim().split('\n').filter(p => /^contracts\/[^/]+\/openapi\.json$/.test(p));
  if (!previous.length) throw new Error('Baseline has zero contracts');
  const current = currentSpecs(root);
  for (const path of previous) if (!current.includes(path)) throw new Error(`Deleted contract: ${path}`);
  const temporary = mkdtempSync(join(tmpdir(), 'xhub-contract-compat-'));
  try {
    return current.map((path, index) => {
      const now = readFileSync(join(root, path), 'utf8');
      const old = previous.includes(path) ? run('git', ['show', `${sha}:${path}`], root) : now;
      const operations = validateSource(now, path);
      validateSource(old, path);
      const left = join(temporary, `${index}-base.json`), right = join(temporary, `${index}-current.json`);
      writeFileSync(left, old); writeFileSync(right, now);
      try {
        const output = run(binary, ['breaking', left, right, '--fail-on', 'WARN', '--format', 'json'], root);
        return { path, baseline: sha, added: !previous.includes(path), operations, output: JSON.parse(output) };
      } catch (error) {
        throw new Error(`Compatibility check failed: ${path}\n${error.stderr || ''}\n${error.stdout || ''}\n${error.message}`);
      }
    });
  } finally { rmSync(temporary, { recursive: true }); }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv.length !== 3) throw new Error('Usage: node check-contract-compatibility.mjs BASE_REF');
  console.log(JSON.stringify(checkCompatibility(process.cwd(), process.argv[2], process.env.OASDIFF_BIN || 'oasdiff'), null, 2));
}
