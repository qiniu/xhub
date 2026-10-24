"""执行真实回归脚本，验证控制台输出与完整日志契约。"""
import os
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class RegressionOutputTests(unittest.TestCase):
    """临时命令夹具隔离数据库与供应商，测试结束删除全部数据。"""

    def test_output_modes_and_failure(self):
        """前置临时 Go 与数据库命令；覆盖静默、详细及失败，验证退出码、日志和独立目录，自动清理。"""
        with tempfile.TemporaryDirectory(prefix='xhub-output-') as directory:
            root = Path(directory)
            (root / 'scripts').mkdir()
            shutil.copy(Path(__file__).with_name('regression.sh'), root / 'scripts/regression.sh')
            shutil.copy(Path(__file__).with_name('check-regression-results.py'), root / 'scripts/check-regression-results.py')
            (root / 'bin').mkdir()
            commands = {
                'psql': '#!/bin/bash\nexit 0\n',
                'go': '''#!/usr/bin/env python3
import json, os, sys
pkg = "fixture/cmd/regression"
print("go: downloading fixture/module v1.0.0", file=sys.stderr, flush=True)
if os.environ.get("STDOUT_NOISE"): print("invalid stdout diagnostic", flush=True)
def event(action, name=None, output=None):
    print(json.dumps(dict(Action=action, Package=pkg, **({"Test": name} if name else {}), **({"Output": output} if output else {}))), flush=True)
event("start")
event("run", "TestExample")
event("output", "TestExample", "=== RUN   TestExample\\n")
for _ in range(10000): event("output", "TestExample", "trace gateway diagnostic\\n")
event("output", "TestExample", "stderr diagnostic\\n")
result = os.environ["RESULT"]
event("output", "TestExample", "--- " + result + ": TestExample (0.01s)\\n")
event(result.lower(), "TestExample")
event(result.lower())
sys.exit(int(os.environ["STATUS"]))
''',
            }
            for name, content in commands.items():
                path = root / 'bin' / name
                path.write_text(content)
                path.chmod(0o755)
            for verbose, status in [(False, 0), (True, 0), (False, 7), (True, 7)]:
                with self.subTest(verbose=verbose, status=status):
                    env = {**os.environ, 'PATH': str(root / 'bin') + ':' + os.environ['PATH'], 'STATUS': str(status), 'RESULT': 'FAIL' if status else 'PASS'}
                    result = subprocess.run(['bash', str(root / 'scripts/regression.sh'), *(['-v'] if verbose else [])], env=env, capture_output=True, text=True)
                    output = result.stdout + result.stderr
                    self.assertEqual(result.returncode, status, output)
                    self.assertNotIn('gateway diagnostic', output)
                    self.assertNotIn('stderr diagnostic', output)
                    self.assertEqual('=== RUN' in output, verbose)
                    self.assertLess(len(output), 1000)
                    log = Path(next(line.split(': ', 1)[1] for line in output.splitlines() if line.startswith('full regression log: ')))
                    self.assertTrue(log.with_name("events.jsonl").is_file())
                    self.assertTrue(log.with_name("summary.json").is_file())
                    self.assertEqual(log.read_text().count('trace gateway diagnostic'), 10000)
                    self.assertIn('stderr diagnostic', log.read_text())
                    self.assertIn('go: downloading fixture/module', log.read_text())
                    self.assertNotIn('go: downloading', log.with_name('events.jsonl').read_text())
                    self.assertIn('go: downloading', log.with_name('go-stderr.log').read_text())
                    self.assertIn('backend regression failed' if status else 'backend regression passed', output)
            self.assertEqual(len(list((root / '.e2e/runs').iterdir())), 4)
            # stdout 仍必须是严格事件流，不能为了容忍下载诊断而忽略真实证据损坏。
            env = {**os.environ, 'PATH': str(root / 'bin') + ':' + os.environ['PATH'], 'STATUS': '0', 'RESULT': 'PASS', 'STDOUT_NOISE': '1'}
            result = subprocess.run(['bash', str(root / 'scripts/regression.sh')], env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            latest = max((root / '.e2e/runs').iterdir(), key=lambda path: path.stat().st_mtime_ns)
            self.assertIn('invalid event stream', json.loads((latest / 'summary.json').read_text())['errors'])


if __name__ == '__main__':
    unittest.main()
