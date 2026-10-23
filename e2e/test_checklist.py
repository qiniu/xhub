"""验收清单、代表选择与浏览器编排测试；无外部调用，临时目录自动清理。"""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock
from checklist import CaseBlocked, Checklist
from acceptance import Dataset, load_manifest

ROOT = Path(__file__).resolve().parents[1]
from acceptance import runner_browser as runner


class ChecklistTests(unittest.TestCase):
    """验证成功、失败、待执行和去重的可观察结果。"""

    def test_scenario_description_visible_in_execution_and_reports(self):
        """目的：执行代码必须输出测什么和判断依据；前置临时清单，验证正常、空说明和失败报告，临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            logs = []
            checklist = Checklist(directory, logs.append, [("demo", "示例"), ("empty", "空说明")],
                                  scenarios={"demo": ("发送相同正文两次", "第二次缓存命中且费用为零")})
            with self.assertRaises(RuntimeError):
                with checklist.case("demo"):
                    raise RuntimeError("失败夹具")
            self.assertIn("    [测什么、怎么测] 发送相同正文两次", logs)
            self.assertIn("    [判断依据] 第二次缓存命中且费用为零", logs)
            report = json.loads((Path(directory) / "cases.json").read_text())
            self.assertEqual(report["cases"][0]["example"], "发送相同正文两次")
            self.assertEqual(report["cases"][0]["status"], "failed")
            self.assertNotIn("example", report["cases"][1])
            self.assertIn("判断依据：第二次缓存命中且费用为零", (Path(directory) / "cases.md").read_text())

    def test_probe_http_warning_and_case_terminal_results(self):
        """目的：真实本地 HTTP 探测与清单结果区分警告和失败；前置首候选502、后候选有效回答，验证重试/切换/通过、全部失败及清理异常不误报通过；服务器、环境及临时报告自动清理，不调用付费供应商。"""
        requests = []

        class Supplier(BaseHTTPRequestHandler):
            """用途：提供隔离供应商 HTTP 边界；只处理测试模型，不访问外网。"""

            def log_message(self, *args):
                """用途：屏蔽服务器访问输出；参数为默认日志参数，返回无，避免干扰断言。"""
                pass

            def do_POST(self):
                """用途：返回失败或有效回答；读取HTTP请求，无返回；记录模型序列供重试断言，响应不含真实凭据。"""
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                requests.append(body['model'])
                good = body['model'] == 'good'
                reply = dict(choices=[dict(message=dict(content='OK'))],
                             usage=dict(prompt_tokens=8, completion_tokens=1)) if good else dict(error=dict(message='unavailable'))
                self.send_response(200 if good else 502)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(json.dumps(reply).encode())

        server = ThreadingHTTPServer(('127.0.0.1', 0), Supplier)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as directory:
                lines = []
                dataset = Dataset('http://127.0.0.1:1', directory, load_manifest(), logger=lines.append)
                provider = dataset.data['providers'][1]
                dataset.checklist = Checklist(directory, lines.append,
                    [('success', '候选恢复'), ('exhausted', '全部不可用'), ('cleanup', '清理失败')])
                env = {provider['base_env']: f'http://127.0.0.1:{server.server_port}/v1',
                       provider['key_env']: 'sk-local-test', 'NO_PROXY': '127.0.0.1', 'no_proxy': '127.0.0.1'}
                with mock.patch.dict(os.environ, env):
                    selected = dataset.run_case('success', dataset.select_lifecycle_model, provider, ['bad', 'good'], 2, 0)
                    self.assertEqual(selected, 'good')
                    self.assertEqual(requests, ['bad', 'bad', 'good'])
                    with self.assertRaisesRegex(RuntimeError, '均未通过'):
                        dataset.run_case('exhausted', dataset.select_lifecycle_model, provider, ['bad'], 1, 0)
                    with self.assertRaisesRegex(RuntimeError, 'cleanup secret'):
                        with dataset.case('cleanup'):
                            try:
                                pass
                            finally:
                                raise RuntimeError('cleanup secret')
                output = '\n'.join(lines)
                self.assertIn('候选探测未成功：HTTP=502', output)
                self.assertIn('将重试当前候选', output)
                self.assertIn('将继续下一候选', output)
                self.assertIn('✅ 用例通过：[结果 001/003 success', output)
                self.assertIn('❌ 用例失败：[失败 002/003 exhausted', output)
                self.assertIn('❌ 用例失败：[失败 003/003 cleanup', output)
                self.assertNotIn('✅ 用例通过：[结果 003/003', output)
                self.assertNotIn('cleanup secret', output)
                self.assertFalse(dataset.report['checks'])
                result = json.loads((Path(directory) / 'cases.json').read_text())
                self.assertEqual(result['counts']['passed'], 1)
                self.assertEqual(result['counts']['failed'], 2)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_runner_import_without_pythonpath(self):
        """目的：用户原始入口无需 PYTHONPATH 即可加载；前置干净环境，验证帮助成功且无导入错误，无外部数据清理。"""
        env = dict(os.environ)
        env.pop('PYTHONPATH', None)
        result = subprocess.run([sys.executable, str(ROOT / 'scripts/e2e.py'), '--help'],
                                env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('--shared-target', result.stdout)

    def test_results_failure_blocked_pending_and_duplicate(self):
        """目的：通过、复验、异常、外部前置缺失和未执行分别报告；前置五项清单，验证编号、耗时、阻塞原因、失败不泄漏正文及重复拒绝，目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            lines = []
            checklist = Checklist(directory, lines.append,
                                  [('a', '普通'), ('b', '复验'), ('c', '失败'), ('d', '外部前置'), ('e', '未执行')])
            with checklist.case('a'):
                pass
            with checklist.case('b') as row:
                row['status'] = 'revalidated'
            with self.assertRaisesRegex(RuntimeError, 'secret'):
                with checklist.case('c'):
                    raise RuntimeError('secret')
            with checklist.case('d'):
                raise CaseBlocked('需要受控供应商错误')
            checklist.summary()
            result = json.loads((Path(directory) / 'cases.json').read_text())
            self.assertEqual([row['status'] for row in result['cases']],
                             ['passed', 'revalidated', 'failed', 'blocked', 'pending'])
            self.assertEqual(result['counts']['passed'], 1)
            self.assertEqual(result['counts']['blocked'], 1)
            self.assertEqual(result['cases'][3]['reason'], '需要受控供应商错误')
            self.assertNotIn('secret', (Path(directory) / 'cases.json').read_text())
            self.assertIn('[开始 001/005', '\n'.join(lines))
            self.assertIn('✅ 用例通过：[结果 001/005 a 普通] 通过', '\n'.join(lines))
            self.assertIn('ℹ️ 用例结果：[结果 002/005 b 复验] 检查点已复验', '\n'.join(lines))
            self.assertIn('❌ 用例失败：[失败 003/005 c 失败] 异常类型=RuntimeError', '\n'.join(lines))
            self.assertIn('ℹ️ 用例结果：[阻塞 004/005 d 外部前置] 外部前置条件未满足：需要受控供应商错误', '\n'.join(lines))
            self.assertNotIn('secret', '\n'.join(lines))
            markdown = (Path(directory) / 'cases.md').read_text()
            self.assertIn('外部前置条件未满足=1', markdown)
            self.assertIn('- d：需要受控供应商错误', markdown)
            with self.assertRaises(ValueError):
                with checklist.case('a'):
                    pass
            with self.assertRaises(ValueError):
                Checklist(directory, lines.append, [('same', '一'), ('same', '二')])

    def test_representatives_and_missing_category(self):
        """目的：81钥匙只选4种路由各一把且不改基线；前置重复的内存身份，验证稳定选择、全量兼容及缺分类拒绝，目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset('http://127.0.0.1:1', directory, load_manifest(), logger=lambda _: None)
            dataset.state['keys'] = [dict(token_id=str(i), profile=profile, team_index=team)
                for i, (profile, team) in enumerate([('inherit', 0), ('fenno-only', 0), ('qiniu-only', 0),
                                                     ('inherit', 1), ('inherit', 2), ('fenno-only', 1)] * 14)][:81]
            baseline = copy.deepcopy(dataset.state)
            self.assertEqual(len(dataset.acceptance_keys()), 81)
            dataset.representative = True
            self.assertEqual([key['token_id'] for key in dataset.acceptance_keys()], ['0', '1', '2', '3'])
            self.assertEqual(dataset.state, baseline)
            dataset.start_checklist(False)
            self.assertEqual(sum(row['id'].startswith('gpt-') for row in dataset.checklist.rows), 4)
            self.assertFalse(any(row['id'] == 'regression' for row in dataset.checklist.rows))
            dataset.report['checks'] = [dict(name='old', passed=True)]
            self.assertFalse(dataset.checked('old'))
            dataset.state['keys'] = []
            with self.assertRaisesRegex(AssertionError, '代表密钥'):
                dataset.acceptance_keys()

    def test_business_browser_selection_and_failure(self):
        """目的：去重流程必须完整执行且不能沿用旧报告；前置模拟报告，验证73项精确筛选包含当前继承预览标题、禁用live、跳过失败和异常传播，临时目录及补丁自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            result = root / '.e2e/current/results.json'
            result.parent.mkdir(parents=True)
            report = root / 'report'
            report.mkdir()
            stats = dict(expected=73, unexpected=0, flaky=0, skipped=0)
            def execute(args, logger, **kwargs):
                """用途：模拟本轮浏览器产物；参数为命令及环境，返回无；只写临时报告，供完整性断言使用。"""
                self.assertIn('--grep', args)
                self.assertEqual(args[-1].count('$'), 73)
                self.assertIn(runner.re.escape('team session inheritance and explicitly bound key preview match inference without consuming rpm') + '$', args[-1])
                self.assertNotIn(runner.re.escape('inherited session and key preview matches inference without consuming rpm') + '$', args[-1])
                self.assertNotIn('E2E_LIVE', kwargs['env'])
                current = Path(kwargs['env']['E2E_BROWSER_REPORT_DIR']) / 'results.json'
                current.parent.mkdir(parents=True, exist_ok=True)
                current.write_text(json.dumps(dict(stats=stats)))
            with mock.patch.object(runner, 'command_stream', side_effect=execute):
                self.assertEqual(runner.run_business_browser(report, mock.Mock())['cases'], 73)
                stats['skipped'] = 1
                with self.assertRaisesRegex(AssertionError, '未全部执行'):
                    runner.run_business_browser(report, mock.Mock())
            with mock.patch.object(runner, 'command_stream'):
                with self.assertRaisesRegex(AssertionError, '缺少本轮'):
                    runner.run_business_browser(report, mock.Mock())
            with mock.patch.object(runner, 'command_stream', side_effect=RuntimeError('browser failed')):
                with self.assertRaisesRegex(RuntimeError, 'browser failed'):
                    runner.run_business_browser(report, mock.Mock())

    def test_gpt_unique_requests_and_revalidation(self):
        """目的：每类只执行一个三轮会话，复验与新执行分别计数；前置4把代表和内存回执，验证12个独立请求及重复ID拒绝，目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset('http://127.0.0.1:1', directory, load_manifest(), logger=lambda _: None)
            dataset.representative = True
            dataset.state['keys'] = [dict(token_id=str(i), profile=profile, team_index=team)
                for i, (profile, team) in enumerate([('inherit', 0), ('inherit', 1), ('fenno-only', 0), ('qiniu-only', 0)])]
            dataset.start_checklist(False)
            def conversation(key, revalidate=False):
                """用途：返回代表会话证据；参数为身份和复验开关，返回三轮回执；第一把复验，其他新执行，无外部副作用。"""
                if revalidate and key['token_id'] != '0':
                    return None
                return dict(turns=[dict(call_id=key['token_id'] + '-' + str(i)) for i in range(3)])
            dataset.codex_conversation = mock.Mock(side_effect=conversation)
            dataset.verify_codex_agents()
            check = dataset.report['checks'][-1]
            self.assertEqual((check['sessions'], check['requests'], check['executed_this_run']), (4, 12, 3))
            self.assertEqual(dataset.checklist.rows[1]['status'], 'revalidated')
            dataset.checklist = None
            dataset.codex_conversation.return_value = dict(turns=[dict(call_id='same')] * 3)
            dataset.codex_conversation.side_effect = None
            with self.assertRaisesRegex(AssertionError, '复用了请求 ID'):
                dataset.verify_codex_agents()

    def test_verify_uses_only_representative_chat_and_routing(self):
        """目的：完整编排的Chat和继承只检查4个代表且保留81基线；前置内存接口/全部旧账单，验证每类型一次、复验状态和错误层级拒绝，目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset('http://127.0.0.1:1', directory, load_manifest(), logger=lambda _: None)
            dataset.representative = True
            categories = [('inherit', 0), ('inherit', 1), ('fenno-only', 0), ('qiniu-only', 0)]
            dataset.state['keys'] = [dict(token_id=str(i), profile=categories[i % 4][0],
                                          team_index=categories[i % 4][1]) for i in range(81)]
            for name, count in [('organizations', 3), ('teams', 9), ('users', 27), ('projects', 9)]:
                dataset.state[name] = [dict(id=str(i)) for i in range(count)]
            dataset.report['calls'] = [dict(key_id=str(i)) for i in range(81)]
            def api(route, *args, **kwargs):
                """用途：模拟成员和有效路由接口；参数为路径，返回业务响应；仅验证编排调用，无网络副作用。"""
                if route.startswith('/team/member_list'):
                    return dict(members=[{}, {}, {}]), {}
                index = int(route.rsplit('=', 1)[-1])
                return dict(effective=dict(scope_type=['organization', 'team', 'key', 'key'][index % 4])), {}
            dataset.api = mock.Mock(side_effect=api)
            dataset.call_checkpoint = mock.Mock(return_value=dict(call_id='saved'))
            dataset.call = mock.Mock(return_value=dict(error='guardrail_failed'))
            helpers = ['verify_acceptance_models', 'verify_permissions', 'verify_xgo', 'verify_limits',
                       'verify_model_lifecycle', 'verify_guardrail_lifecycle',
                       'verify_profile_route_isolation', 'verify_cache_weight_chain',
                       'verify_affinity_chain', 'verify_fallback_policy_chain']
            for name in helpers:
                setattr(dataset, name, mock.Mock())
            dataset.start_checklist(False)
            dataset.verify()
            self.assertEqual(dataset.call_checkpoint.call_count, 4)
            self.assertEqual(sum(call.args[0].startswith('/route_template') for call in dataset.api.call_args_list), 4)
            self.assertEqual(len(dataset.state['keys']), 81)
            self.assertEqual(len(dataset.report['calls']), 81)
            check = next(row for row in dataset.report['checks'] if row['name'] == 'representative-keys-real-calls-and-billing')
            self.assertEqual((check['keys'], check['executed_this_run'], check['resumed']), (4, 0, 4))
            for name in helpers:
                getattr(dataset, name).assert_called_once()
            dataset.checklist = None
            dataset.api = mock.Mock(side_effect=[(dict(members=[{}, {}, {}]), {})] * 9 +
                                    [(dict(effective=dict(scope_type='wrong')), {})])
            with self.assertRaisesRegex(AssertionError, '路由继承层级错误'):
                dataset.verify()
