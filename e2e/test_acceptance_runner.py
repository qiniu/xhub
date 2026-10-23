"""真实验收 runner 模块的单元测试；各用例独立管理夹具与清理。"""
from contextlib import redirect_stdout
import io
import json
import os
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest import mock
from acceptance import Dataset, load_manifest
from acceptance import runner_runtime as RUNNER
from acceptance import runner_browser as BROWSER, runner_shared as SHARED
from acceptance import cli as CLI
from acceptance.category_help import category_help, CATEGORY_DESCRIPTIONS, CATEGORY_SCENARIOS


class AcceptanceTests(unittest.TestCase):
    """验证所属业务模块的正常、边界和失败路径，资源由各测试自行隔离清理。"""

    def test_verify_restores_report_collections_before_recording_actions(self):
        """目的：历史完整、精简和重复编码报告恢复后可记录检查点，错误集合类型在业务写入前拒绝。

        前置真实 Dataset 与离线接口夹具；验证四个集合、旧证据保留及失败无执行；目录自动清理，无外部写入。
        """
        fields = ("checks", "probe_attempts", "calls", "models")
        old_check = {"name": "previous", "passed": True}
        reports = [{"checks": [old_check]}, {"status": "failed", "error": "ConnectionRefusedError"},
                   json.dumps({"status": "failed"})]
        reports += [{field: None} for field in fields]
        for report in reports:
            with self.subTest(report=report), tempfile.TemporaryDirectory() as directory:
                dataset = Dataset("http://localhost:1", directory, load_manifest(), logger=mock.Mock())
                dataset.selected_categories = frozenset({"entities"})
                Path(directory, "access.json").write_text(json.dumps({"admin": "fixture-token"}))
                Path(directory, "report.json").write_text(json.dumps(report))
                args = type("Args", (), {"directory": directory, "category": ["entities"]})()
                invalid = isinstance(report, dict) and any(field in report and not isinstance(report[field], list) for field in fields)

                def verify():
                    """模拟部署成功后的真实检查点记录；无参数/返回，供 runner 回调，不访问供应商。"""
                    dataset.action_ok("fixture-model", "bypass_openai_chat", "添加模型与端点", "fixture")

                with mock.patch.object(SHARED.module, "Dataset", return_value=dataset), \
                        mock.patch.object(SHARED, "http", return_value=(200, {}, {})), \
                        mock.patch.object(SHARED, "local_admin_credentials", return_value={}), \
                        mock.patch.object(dataset, "api", return_value=({}, {})), \
                        mock.patch.object(dataset, "start_checklist"), \
                        mock.patch.object(dataset, "verify", side_effect=verify) as execution, \
                        mock.patch.dict(os.environ, {}, clear=True), redirect_stdout(io.StringIO()):
                    self.assertEqual(SHARED.verify_running_gateway(args, load_manifest()), 1 if invalid else 0)
                saved = json.loads(Path(directory, "report.json").read_text())
                if invalid:
                    execution.assert_not_called()
                    self.assertIn("必须为 JSON 数组", saved["error"])
                else:
                    for field in fields:
                        self.assertIsInstance(saved[field], list)
                    self.assertEqual(saved["checks"][-1]["action"], "添加模型与端点")
                    if isinstance(report, dict) and report.get("checks"):
                        self.assertEqual(saved["checks"][0], old_check)
                    self.assertNotIn("error", saved)

    def test_category_make_and_cli_contract(self):
        """目的：make e2e 分类只发一个选中命令，未知分类在执行前失败；前置无凭据，验证单类、多类、全量、列表及 seed 禁用分类；只运行 dry-run/参数校验，不启动服务，无数据需清理。"""
        root = Path(__file__).parents[1]
        for categories in ([], ["limits"], ["limits", "permissions"], ["regression"], list(RUNNER.E2E_CATEGORIES)):
            with self.subTest(categories=categories):
                result = subprocess.run(["make", "-n", "e2e", *categories], cwd=root, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.count("scripts/e2e.py"), 1)
                for category in categories:
                    self.assertIn("--category " + category, result.stdout)
                self.assertEqual(result.stdout.count("--category "), len(categories))
        invalid = subprocess.run(["make", "-n", "e2e", "limtis"], cwd=root, capture_output=True, text=True)
        self.assertNotEqual(invalid.returncode, 0)
        self.assertNotIn("scripts/e2e.py", invalid.stdout, "拼错分类不能意外运行全套付费用例")
        listed = subprocess.run(["make", "e2e-list"], cwd=root, capture_output=True, text=True)
        self.assertEqual(listed.returncode, 0, listed.stderr)
        direct = subprocess.run(["python3", "scripts/e2e.py", "--list-categories"], cwd=root, capture_output=True, text=True)
        self.assertEqual(direct.returncode, 0, direct.stderr)
        self.assertEqual(listed.stdout, direct.stdout)
        self.assertEqual(listed.stdout, category_help() + "\n")
        # 分类说明也有缩进，只读取恰好缩进两空格的分类标题。
        rows = [line.split()[0] for line in listed.stdout.splitlines() if line.startswith("  ") and not line.startswith("   ")]
        self.assertEqual(tuple(rows), RUNNER.E2E_CATEGORIES)
        for args in (["--category", "limtis"], ["--category", "limits"],
                     ["--shared-target", "--phase", "seed", "--category", "limits"],
                     ["--shared-target", "--phase", "verify", "--category", "regression"]):
            invalid = subprocess.run(["python3", "scripts/e2e.py", *args], cwd=root, capture_output=True, text=True)
            self.assertEqual(invalid.returncode, 2, invalid.stderr)

    def test_category_help_complete_boundaries_and_no_services(self):
        """目的：中文列表覆盖全部分类并解释使用前提；前置为内存参数，验证完整/空/未知分类及 CLI 零副作用；模拟自动恢复，无数据需清理。"""
        self.assertEqual(set(CATEGORY_DESCRIPTIONS), set(RUNNER.E2E_CATEGORIES))
        self.assertEqual(set(CATEGORY_SCENARIOS), set(RUNNER.E2E_CATEGORIES))
        rendered = category_help()
        for category, description in CATEGORY_DESCRIPTIONS.items():
            example, logic = CATEGORY_SCENARIOS[category]
            self.assertIn(example, rendered)
            self.assertIn(logic, rendered)
            self.assertIn(description, rendered, category + " 必须显示业务说明")
            self.assertTrue(any("\u4e00" <= character <= "\u9fff" for character in description))
        for text in ("make e2e limits permissions", "make testdata", "清空", "计费", "模拟供应商", ".e2e/real-acceptance-current"):
            self.assertIn(text, rendered)
        self.assertFalse(any(line.startswith("  ") for line in category_help([]).splitlines()))
        with self.assertRaisesRegex(ValueError, "缺少 E2E 分类中文说明: unknown"):
            category_help(["unknown"])
        with mock.patch("sys.argv", ["e2e.py", "--list-categories"]), \
                mock.patch.object(CLI.module, "load_manifest", side_effect=AssertionError("列表不得读取供应商配置")), \
                mock.patch.object(CLI, "shared_main", side_effect=AssertionError("列表不得启动共享服务")), \
                mock.patch.object(CLI, "run_isolated", side_effect=AssertionError("列表不得启动隔离服务")), \
                redirect_stdout(io.StringIO()) as output:
            self.assertEqual(CLI.main(), 0)
        self.assertEqual(output.getvalue(), rendered + "\n")


    def test_selected_categories_skip_unrelated_calls_and_validate_plan(self):
        """目的：所有分类过滤与模型子类均只执行选中函数；前置内存夹具，验证空/未知拒绝、回调失败及未执行项不进入报告；不访问供应商，临时报告自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), logger=lambda _: None)
            dataset.representative = True
            dataset.state["keys"] = [dict(token_id=str(i), profile=profile, team_index=team, call_model="unit-model")
                for i, (profile, team) in enumerate([("inherit", 0), ("inherit", 1), ("fenno-only", 0), ("qiniu-only", 0)])]
            dataset.start_checklist(True, list(RUNNER.E2E_CATEGORIES))
            self.assertEqual(tuple(row["id"] for row in dataset.checklist.rows), RUNNER.E2E_CATEGORIES)
            for invalid in ([], ["unknown"], ["limits", "unknown"]):
                with self.assertRaisesRegex(ValueError, "E2E 分类"):
                    dataset.start_checklist(True, invalid)
            dataset.start_checklist(False, ["limits"])
            skipped = mock.Mock(side_effect=RuntimeError("must not run"))
            self.assertIsNone(dataset.run_case("permissions", skipped))
            skipped.assert_not_called()
            dataset.verify_limits = mock.Mock()
            dataset.codex_conversation = mock.Mock(side_effect=RuntimeError("unselected GPT"))
            dataset.verify_glm_model = mock.Mock(side_effect=RuntimeError("unselected GLM"))
            dataset.verify_image_model = mock.Mock(side_effect=RuntimeError("unselected image"))
            dataset.verify_video_model = mock.Mock(side_effect=RuntimeError("unselected video"))
            dataset.verify()
            dataset.verify_limits.assert_called_once_with(dataset.state["keys"][0])
            for name in ("codex_conversation", "verify_glm_model", "verify_image_model", "verify_video_model"):
                getattr(dataset, name).assert_not_called()
            self.assertEqual([(row["id"], row["status"]) for row in dataset.checklist.rows], [("limits", "passed")])
            dataset.start_checklist(False, ["limits"])
            dataset.verify_limits.side_effect = RuntimeError("selected failure")
            with self.assertRaisesRegex(RuntimeError, "selected failure"):
                dataset.verify()
            self.assertEqual(dataset.checklist.rows[0]["status"], "failed")


    def test_business_browser_reads_private_report_and_rejects_missing_or_failed_results(self):
        """目的：排队浏览器必须读取锁内保存的专属报告；前置模拟编排返回成功、失败或缺报告，验证路径和错误响应；临时目录清理，不启动外部服务。"""
        for mode in ["passed", "failed", "missing"]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                report_dir = Path(directory)

                def command(command, logger, env):
                    """用途：模拟浏览器退出前保存专属报告；参数为命令、日志器和环境，无返回；供编排测试调用，仅写临时证据。"""
                    path = Path(env["E2E_BROWSER_REPORT_DIR"])
                    self.assertEqual(path, (report_dir / "business-browser").resolve())
                    if mode != "missing":
                        path.mkdir()
                        (path / "results.json").write_text(json.dumps({"stats": {
                            "expected": 73 if mode == "passed" else 72,
                            "unexpected": 0 if mode == "passed" else 1, "flaky": 0, "skipped": 0}}))

                with mock.patch.object(BROWSER, "command_stream", side_effect=command):
                    if mode == "passed":
                        result = BROWSER.run_business_browser(report_dir, mock.Mock())
                        self.assertEqual(result["cases"], 73)
                    else:
                        with self.assertRaisesRegex(AssertionError, "缺少本轮浏览器报告" if mode == "missing" else "未全部执行通过"):
                            BROWSER.run_business_browser(report_dir, mock.Mock())
                self.assertEqual((report_dir / "business-browser-results.json").exists(), mode != "missing")


    def test_business_browser_selection_preserves_split_lifecycles_pages_and_roles(self):
        """目的：迁移后保留原业务范围；前置真实清单，验证生命周期、所有页面、四角色及路径，无外部访问与清理。"""
        catalog = json.loads((BROWSER.ROOT / "frontend/e2e/scenario-catalog.json").read_text())
        selected = BROWSER.business_browser_selection(catalog)
        self.assertEqual(len(selected), 73)
        ids = {row["id"] for row in selected}
        self.assertTrue({"SC-252", "SC-252-rotation", "SC-252-block", "SC-252-unblock", "SC-252-delete",
                         "SC-253", "SC-253-connection", "SC-253-delete",
                         "SC-214", "SC-221", "SC-226", "SC-231"}.issubset(ids))
        self.assertEqual({row["id"] for row in selected if "/pages/" in row["file"]},
                         {row["id"] for row in catalog if "/scenarios/pages/" in row["file"]})
        self.assertTrue(all((BROWSER.ROOT / "frontend" / row["file"]).is_file() for row in selected))

    def test_business_browser_selection_rejects_missing_duplicate_and_invalid_bindings(self):
        """目的：清单漂移提前失败；前置真实清单变体，验证缺失、重复、旧后缀、越界路径和缺标题；仅操作内存，无清理。"""
        catalog = json.loads((BROWSER.ROOT / "frontend/e2e/scenario-catalog.json").read_text())
        row = next(row for row in catalog if row["id"] == "SC-252")
        remaining = [item for item in catalog if item is not row]
        cases = [remaining, [*catalog, row],
                 *[[*remaining, {**row, "file": file}] for file in
                   ("e2e/writes.spec.ts", "e2e/scenarios/../escape.test.ts", "/outside.test.ts")],
                 [*remaining, {**row, "title": ""}],
                 [*remaining, {**row, "file": next(item["file"] for item in catalog if item["id"] == "SC-253")}]]
        for invalid in cases:
            with self.subTest(binding=invalid[-1]), self.assertRaisesRegex(ValueError, "去重浏览器场景"):
                BROWSER.business_browser_selection(invalid)

    def test_business_browser_selection_is_discovered_by_real_playwright(self):
        """目的：真实 Playwright 必须发现全部选中场景且无串选；前置已安装依赖，验证实际标题集合；只列举，不启动服务或调用供应商，临时证据自动清理。"""
        frontend = BROWSER.ROOT / "frontend"
        catalog = json.loads((frontend / "e2e/scenario-catalog.json").read_text())
        selected = BROWSER.business_browser_selection(catalog)
        with tempfile.TemporaryDirectory() as directory:
            env = {**BROWSER.os.environ, "E2E_RUN_DIR": directory}
            files = [r"(^|/)" + BROWSER.re.escape(row["file"]) + "$" for row in selected]
            pattern = "(" + "|".join(BROWSER.re.escape(row["title"]) + "$" for row in selected) + ")"
            result = subprocess.run(["node", "node_modules/@playwright/test/cli.js", "test", *files,
                                     "--grep", pattern, "--list", "--reporter=json"],
                                    cwd=frontend, env=env, capture_output=True, text=True, timeout=60)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual(report["errors"], [])
            discovered = []

            def collect(suite):
                """用途：递归读取发现报告；参数为 suite，无返回；供契约测试收集标题，仅修改本地列表。"""
                discovered.extend(spec["title"] for spec in suite.get("specs", []))
                for child in suite.get("suites", []):
                    collect(child)

            for suite in report["suites"]:
                collect(suite)
            self.assertCountEqual(discovered, [row["title"] for row in selected])


    def test_shared_target_requires_exact_urls(self):
        """目的：破坏性清理只能命中用户指定目标；前置固定白名单，验证精确通过和数据库/Redis偏差失败，不连接或清理外部数据。"""
        RUNNER.validate_shared_target(RUNNER.SHARED_DATABASE_URL, RUNNER.SHARED_REDIS_URL)
        for database_url, redis_url in (
                (RUNNER.SHARED_DATABASE_URL.replace("/xhub?", "/postgres?"), RUNNER.SHARED_REDIS_URL),
                (RUNNER.SHARED_DATABASE_URL, "redis://127.0.0.1:6379/0")):
            with self.subTest(database_url=database_url, redis_url=redis_url), self.assertRaises(ValueError):
                RUNNER.validate_shared_target(database_url, redis_url)


    def test_shared_config_uses_public_database_and_redis_one(self):
        """目的：共享构建配置不带隔离schema且固定Redis DB 1；前置测试密码，验证配置契约，不写文件且无需清理。"""
        config = RUNNER.shared_config("test-password")["general_settings"]
        self.assertEqual(config["database_url"], RUNNER.SHARED_DATABASE_URL)
        self.assertEqual(config["redis_url"], RUNNER.SHARED_REDIS_URL)
        self.assertNotIn("search_path", config["database_url"])
        self.assertEqual(config["admin_password"], "test-password")

    def test_shared_reset_forces_existing_connections_closed(self):
        """目的：已有连接不阻塞共享重建；前置替代数据库命令和Redis，验证强制断连、重建及空缓存检查，不触碰实际数据，无需清理。"""
        with mock.patch.object(RUNNER, "command") as command, \
                mock.patch.object(RUNNER, "redis_command", side_effect=["OK", 0]) as redis:
            RUNNER.reset_shared_target(logger=lambda message: None)
        self.assertEqual(command.call_args_list[0].args[0][-1], "DROP DATABASE IF EXISTS xhub WITH (FORCE)")
        self.assertEqual(command.call_args_list[1].args[0][-1], "CREATE DATABASE xhub OWNER xhub")
        self.assertEqual(redis.call_args_list, [mock.call("FLUSHDB"), mock.call("DBSIZE")])


    def test_provider_validation_happens_without_database_access(self):
        """目的：缺少真实供应商密钥时必须在清库前失败；前置空环境，验证缺失和非法URL边界，本测试不调用重置函数。"""
        data = load_manifest()
        with self.assertRaisesRegex(ValueError, data["providers"][0]["key_env"]):
            RUNNER.validate_providers(data, {})
        environ = {provider["key_env"]: "secret" for provider in data["providers"]}
        environ[data["providers"][0]["base_env"]] = "http://insecure.invalid"
        with self.assertRaisesRegex(ValueError, "HTTPS"):
            RUNNER.validate_providers(data, environ)


    def test_shared_seed_rejects_missing_credentials_before_cleanup(self):
        """目的：缺少实际密钥时共享建数不能清库或删证据；前置已有检查点和空环境，验证先失败且重置、构建、进程均未调用，临时目录清理。"""
        with tempfile.TemporaryDirectory() as directory:
            checkpoint = Path(directory) / "keep.txt"
            checkpoint.write_text("existing")
            args = type("Args", (), {"phase": "seed", "directory": directory})()
            with mock.patch.dict(RUNNER.os.environ, {}, clear=True), \
                    mock.patch.object(SHARED, "reset_shared_target") as reset, \
                    mock.patch.object(SHARED, "command") as command, \
                    mock.patch.object(RUNNER.subprocess, "Popen") as process:
                with self.assertRaisesRegex(SystemExit, "FENNO_AI_API_KEY"):
                    SHARED.shared_main_locked(args, load_manifest())
            reset.assert_not_called()
            command.assert_not_called()
            process.assert_not_called()
            self.assertEqual(checkpoint.read_text(), "existing")


    def test_shared_seed_missing_password_preserves_baseline(self):
        """目的：已有供应商凭据但缺管理员密码时在联网前失败；前置保留文件，验证不清库、不覆盖证据；临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            checkpoint = Path(directory, "keep.txt")
            checkpoint.write_text("existing")
            args = type("Args", (), {"phase": "seed", "directory": directory})()
            with mock.patch.dict(os.environ, {"FENNO_AI_API_KEY": "fixture", "QINIU_API_KEY": "fixture"}, clear=True), \
                    mock.patch.object(SHARED, "local_admin_credentials", return_value={}), \
                    mock.patch.object(SHARED, "http") as health, \
                    mock.patch.object(SHARED, "reset_shared_target") as reset:
                with self.assertRaisesRegex(SystemExit, "E2E_DATASET_PASSWORD"):
                    SHARED.shared_main_locked(args, load_manifest())
                health.assert_not_called()
                reset.assert_not_called()
            self.assertEqual(checkpoint.read_text(), "existing")

    def test_shared_seed_owns_gateway_lifecycle(self):
        """目的：建数不依赖4000且关闭自建网关；前置模拟数据库和进程，验证成功、启动失败、超时清理；临时目录自动删除。"""
        for failure in (None, RuntimeError("启动失败"), AssertionError("建数失败")):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                args = type("Args", (), {"phase": "seed", "directory": directory})()
                dataset = mock.Mock()
                dataset.report = {}
                dataset.state = {name: [] for name in ("organizations", "teams", "users", "projects", "keys")}
                process = mock.Mock()
                if isinstance(failure, AssertionError):
                    dataset.seed.side_effect = failure
                    process.wait.side_effect = [subprocess.TimeoutExpired("gateway", 10), None]
                with mock.patch.object(SHARED, "reset_shared_target") as reset, \
                        mock.patch.object(SHARED, "free_port", return_value=45678), \
                        mock.patch.object(SHARED, "http") as http, \
                        mock.patch.object(SHARED, "command"), \
                        mock.patch.object(SHARED, "ready", side_effect=failure if isinstance(failure, RuntimeError) else None), \
                        mock.patch.object(RUNNER.subprocess, "Popen", return_value=process) as spawn, \
                        mock.patch.object(SHARED.module, "Dataset", return_value=dataset), \
                        mock.patch.object(SHARED.observer_module, "Observer") as observer, \
                        mock.patch.dict(os.environ, {"FENNO_AI_API_KEY": "fixture", "QINIU_API_KEY": "fixture", "E2E_DATASET_PASSWORD": "password", "E2E_GATEWAY": "http://unreachable.invalid"}, clear=True), redirect_stdout(io.StringIO()):
                    self.assertEqual(SHARED.shared_main_locked(args, load_manifest()), 0 if failure is None else 1)
                    reset.assert_called_once()
                    spawn.assert_called_once()
                    self.assertIn("127.0.0.1:45678", spawn.call_args.args[0])
                    http.assert_not_called()
                    observer.assert_not_called()
                    dataset.verify.assert_not_called()
                    process.terminate.assert_called_once()
                    process.wait.assert_called()
                    if isinstance(failure, AssertionError):
                        process.kill.assert_called_once()
                    if failure is None:
                        self.assertEqual(dataset.report["gateway_mode"], "temporary")
                        self.assertEqual(dataset.report["status"], "seeded")

    def test_shared_seed_build_failure_preserves_data(self):
        """目的：编译失败时不能清库；前置旧基线，验证错误返回和未启动，临时目录自动清理。"""
        for failure in (RuntimeError("编译失败"),):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                checkpoint = Path(directory, "access.json")
                checkpoint.write_text("existing")
                args = type("Args", (), {"phase": "seed", "directory": directory})()
                with mock.patch.object(SHARED, "reset_shared_target") as reset, \
                        mock.patch.object(SHARED, "free_port", return_value=45678), \
                        mock.patch.object(SHARED, "command", side_effect=failure), \
                        mock.patch.object(RUNNER.subprocess, "Popen") as spawn, \
                        mock.patch.dict(os.environ, {"FENNO_AI_API_KEY": "fixture", "QINIU_API_KEY": "fixture", "E2E_DATASET_PASSWORD": "password"}, clear=True), redirect_stdout(io.StringIO()):
                    self.assertEqual(SHARED.shared_main_locked(args, load_manifest()), 1)
                reset.assert_not_called()
                spawn.assert_not_called()
                self.assertEqual(checkpoint.read_text(), "existing")

    def test_run_logger_writes_identical_timestamped_terminal_and_file_lines(self):
        """目的：完整过程日志同时显示并持久化；前置临时日志，验证多行均带时间且正文一致，临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "e2e.log"
            output = io.StringIO()
            logger = RUNNER.RunLogger(path)
            with redirect_stdout(output):
                logger.log("第一步\n第二步")
            self.assertEqual(output.getvalue(), path.read_text())
            self.assertRegex(path.read_text(), r"(?m)^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] 第一步$")
            self.assertIn("] 第二步\n", path.read_text())


    def test_shared_run_lock_rejects_concurrent_process(self):
        """目的：共享 xhub 构建与验收不能并发；前置临时锁文件，验证第二个持锁者立即失败，退出上下文后自动释放。"""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "shared.lock"
            with RUNNER.shared_run_lock(path):
                with self.assertRaisesRegex(RuntimeError, "已有 make testdata"):
                    with RUNNER.shared_run_lock(path):
                        self.fail("竞争锁不应成功")
            with RUNNER.shared_run_lock(path):
                self.assertTrue(path.exists())


    def test_restore_supplier_addresses_uses_deployment_provider_ids(self):
        """目的：退出清理恢复仍存在的聊天部署并覆盖旧诊断；前置模拟部署和接口，验证按供应商还原地址、成功日志及未知供应商失败记录，不访问真实服务或数据库。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest())
            dataset.admin = "admin-token"
            dataset.state["deployments"] = [
                {"id": "deployment-a", "provider": "fennoai", "kind": "chat", "observed": True},
                {"id": "deployment-b", "provider": "qiniu", "kind": "chat", "observed": True},
                {"id": "deployment-media", "provider": "qiniu", "kind": "video", "observed": False},
                {"id": "deployment-unknown", "provider": "missing", "kind": "chat", "observed": True},
            ]
            calls = []
            lines = []
            dataset.logger = lines.append
            dataset.report["retention_error"] = "上一轮遗留错误"
            dataset.api = lambda route, body: calls.append((route, body))
            SHARED.restore_supplier_addresses(dataset, load_manifest())
            self.assertEqual([body["model_info"]["id"] for _, body in calls], ["deployment-a", "deployment-b"])
            self.assertEqual(calls[0][1]["litellm_params"]["api_base"], "https://api.fenno.ai")
            self.assertEqual(calls[1][1]["litellm_params"]["api_base"], "https://api.modelink.ai")
            self.assertIn("未知供应商", dataset.report["retention_error"])
            self.assertNotIn("上一轮遗留错误", dataset.report["retention_error"])
            self.assertEqual(sum("✅" in line and "恢复供应商地址" in line for line in lines), 2)


    def test_restore_supplier_addresses_clears_stale_error_after_success(self):
        """目的：上一轮恢复错误不得污染本轮成功报告；前置一个有效聊天部署和旧 retention_error，验证恢复成功后字段被删除并输出打勾日志，不访问真实服务或数据库。"""
        with tempfile.TemporaryDirectory() as directory:
            lines = []
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), logger=lines.append)
            dataset.admin = "admin-token"
            dataset.report["retention_error"] = "旧错误"
            dataset.state["deployments"] = [
                {"id": "deployment-a", "public_name": "gpt-5.6-sol", "provider": "fennoai",
                 "kind": "chat", "observed": True},
            ]
            dataset.api = lambda route, body: ({}, {})
            SHARED.restore_supplier_addresses(dataset, load_manifest())
            self.assertNotIn("retention_error", dataset.report)
            self.assertIn("✅ [gpt-5.6-sol / bypass_openai_chat / 恢复供应商地址]", "\n".join(lines))


    def test_restore_supplier_addresses_ignores_deleted_temporary_deployment(self):
        """目的：异常退出时已删除的生命周期部署不得造成恢复误报；前置一个带 temporary 标记且更新返回404的聊天部署，验证报告无 retention_error 并记录无需恢复，不访问真实服务或数据库。"""
        with tempfile.TemporaryDirectory() as directory:
            lines = []
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), logger=lines.append)
            dataset.admin = "admin-token"
            dataset.state["deployments"] = [
                {"id": "deployment-deleted", "public_name": "qwen/temporary-real-model",
                 "provider": "qiniu", "kind": "chat", "observed": True, "temporary": True},
            ]
            dataset.api = mock.Mock(side_effect=AssertionError("POST /model/update 状态 404，预期 200"))
            SHARED.restore_supplier_addresses(dataset, load_manifest())
            self.assertNotIn("retention_error", dataset.report)
            self.assertIn("临时部署 deployment-deleted 已删除，无需恢复", "\n".join(lines))


    def test_attach_chat_observer_leaves_native_media_on_supplier_addresses(self):
        """目的：验收观察器只接管聊天请求，防止图片和视频被改发聊天端点；前置聊天、图片和视频部署，验证仅 observed 部署更新，无网络或持久化数据需清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest())
            dataset.isolated_upstreams = True
            dataset.state["deployments"] = [
                {"id": "chat-a", "provider": "fennoai", "kind": "chat", "observed": True, "endpoint_types": ["chat"]},
                {"id": "image-a", "provider": "fennoai", "kind": "image", "observed": False},
                {"id": "video-a", "provider": "qiniu", "kind": "video", "observed": False},
            ]
            calls = []
            dataset.api = lambda route, body: calls.append((route, body))
            observer = type("ObserverStub", (), {"base": "http://127.0.0.1:4567"})()
            SHARED.attach_chat_observer(dataset, observer)
            self.assertEqual(len(calls), 1)
            self.assertEqual(calls[0][1]["model_info"]["id"], "chat-a")
            self.assertEqual(calls[0][1]["model_info"], {"id": "chat-a"})
            self.assertEqual(dataset.state["deployments"][0]["endpoint_types"], ["chat"])
            self.assertEqual(calls[0][1]["litellm_params"]["api_base"], "http://127.0.0.1:4567/fennoai")

    def test_observer_rejects_shared_deployments_before_any_write(self):
        """目的：共享目标在接入观察器或创建聊天部署前立即拒绝；前置普通数据集和本地观察器，验证零管理写入，临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            observer = type("ObserverStub", (), {"base": "http://127.0.0.1:4567"})()
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), observer=observer)
            dataset.api = mock.Mock()
            for isolated in (False, None):
                dataset.isolated_upstreams = isolated
                with self.assertRaisesRegex(ValueError, "禁止改写共享部署"):
                    SHARED.attach_chat_observer(dataset, observer)
                definition = next(row for row in dataset.data["models"] if row["kind"] == "chat")
                with self.assertRaisesRegex(ValueError, "禁止改写共享部署"):
                    dataset.deployment(definition, dataset.data["providers"][0])
            dataset.api.assert_not_called()

    def test_running_gateway_reuses_service_without_address_changes(self):
        """目的：真实网关模式仅复用现有服务；前置最小基线及替代业务验证，覆盖正常、不可达、认证失败与不支持分类，验证无启动/编译/地址写入，临时文件自动清理。"""
        for mode in ("success", "legacy-report", "invalid-report", "offline", "unauthorized", "unsupported"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                Path(directory, "access.json").write_text(json.dumps({"admin": "saved-session", "keys": []}))
                Path(directory, "report.json").write_text(json.dumps(json.dumps({"checks": []}) if mode == "legacy-report" else [] if mode == "invalid-report" else {"checks": []}))
                dataset = mock.Mock()
                dataset.report = {}
                dataset.case_enabled.return_value = False
                args = type("Args", (), {"phase": "verify", "directory": directory,
                                           "category": ["regression"] if mode == "unsupported" else ["entities"]})()
                with mock.patch.object(SHARED, "local_admin_credentials", return_value={}), \
                        mock.patch.object(SHARED, "http", return_value=(200, {}, {})) as health, \
                        mock.patch.object(SHARED.module, "Dataset", return_value=dataset), \
                        mock.patch.object(SHARED, "command") as command, \
                        mock.patch.object(SHARED.subprocess, "Popen") as process, \
                        mock.patch.object(SHARED.observer_module, "Observer") as observer, \
                        mock.patch.dict(os.environ, {}, clear=True), redirect_stdout(io.StringIO()):
                    if mode == "offline":
                        health.side_effect = ConnectionRefusedError("offline")
                    if mode == "unauthorized":
                        dataset.api.side_effect = AssertionError("401")
                    self.assertEqual(SHARED.shared_main_locked(args, load_manifest()), 0 if mode in ("success", "legacy-report") else 1)
                    process.assert_not_called()
                    command.assert_not_called()
                    observer.assert_not_called()
                    if mode in ("success", "legacy-report"):
                        dataset.verify.assert_called_once_with()
                        self.assertEqual(dataset.gateway if isinstance(dataset.gateway, str) else dataset.report["gateway"], "http://127.0.0.1:4000")
                    else:
                        dataset.verify.assert_not_called()
                    if mode == "offline":
                        failed_report = json.loads(Path(directory, "report.json").read_text())
                        self.assertEqual(failed_report["status"], "failed")
                        self.assertIn("ConnectionRefusedError", failed_report["error"])
                    self.assertFalse(any(call.args[0] == "/model/update" for call in dataset.api.call_args_list))

    def test_running_gateway_returns_nonzero_for_blocked_external_case(self):
        """目的：外部前置条件缺失不能把共享验收报为通过；前置最小共享报告与 blocked 清单，验证后续编排返回后状态、外部边界和非零退出码；临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "access.json").write_text(json.dumps({"admin": "saved-session", "keys": []}))
            Path(directory, "report.json").write_text(json.dumps({"checks": []}))
            dataset = mock.Mock()
            dataset.report = {}
            dataset.case_enabled.return_value = False
            dataset.checklist.rows = [{"id": "fallback", "status": "blocked", "reason": "需要受控 429"}]
            args = type("Args", (), {"phase": "verify", "directory": directory, "category": ["fallback"]})()
            with mock.patch.object(SHARED, "local_admin_credentials", return_value={}), \
                 mock.patch.object(SHARED, "http", return_value=(200, {}, {})), \
                 mock.patch.object(SHARED.module, "Dataset", return_value=dataset), \
                 mock.patch.dict(os.environ, {}, clear=True), redirect_stdout(io.StringIO()):
                self.assertEqual(SHARED.verify_running_gateway(args, load_manifest()), 1)
            self.assertEqual(dataset.report["status"], "blocked")
            self.assertEqual(dataset.report["external_boundaries"], [
                {"case": "fallback", "reason": "需要受控 429"}])
