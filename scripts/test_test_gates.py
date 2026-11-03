"""真实 Go 子进程验证回归入口，隔离 shell 故障验证退出门禁；不调用线上或供应商。"""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import unittest
import zipfile

SCRIPTS = Path(__file__).resolve().parent


class RegressionSelectionTests(unittest.TestCase):
    """用真实 go test 匹配顶层/子用例；临时模块和日志自动删除。"""

    def run_selection(self, pattern, cold_cache=False):
        """参数为 Go 筛选表达式及是否使用空依赖缓存；返回脚本结果与日志，临时 Go 模块和本地依赖代理自动清理，数据库预检仅为替身。"""
        with tempfile.TemporaryDirectory(prefix="xhub-gate-") as directory:
            root = Path(directory)
            (root / "scripts").mkdir()
            for name in ["regression.sh", "check-regression-results.py"]:
                if (SCRIPTS / name).exists():
                    shutil.copyfile(SCRIPTS / name, root / "scripts" / name)
            (root / "bin").mkdir()
            psql = root / "bin/psql"
            psql.write_text("#!/bin/sh\nexit 0\n")
            psql.chmod(0o755)
            (root / "go.mod").write_text("module gate-fixture\n\ngo 1.24\n")
            (root / "cmd/regression").mkdir(parents=True)
            (root / "cmd/regression/gate_test.go").write_text('''package regression
import "testing"
// 临时真实测试树供选择门禁验证；无外部状态。
func TestParent(t *testing.T) {
 t.Run("valid", func(t *testing.T) { if 1+1 != 2 { t.Fatal("arithmetic") } })
 t.Run("optional", func(t *testing.T) { t.Skip("explicit optional dependency") })
}
func TestTop(t *testing.T) { t.Log("executed leaf") }
''')
            env = dict(os.environ, PATH=str(root / "bin") + os.pathsep + os.environ["PATH"], GOWORK="off")
            if cold_cache:
                # 文件代理使真实 Go 首次下载可重复且离线，不能用伪造 JSON 代替下载诊断。
                # 直接使用仓库已准备的工具链；空模块缓存不能触发额外的 Go 工具链下载。
                goroot = subprocess.check_output(["go", "env", "GOROOT"], cwd=SCRIPTS.parent, text=True).strip()
                (root / "bin/go").symlink_to(Path(goroot) / "bin/go")
                module = "example.com/gatefixture"
                proxy = root / "proxy" / module / "@v"
                proxy.mkdir(parents=True)
                mod = f"module {module}\n\ngo 1.24\n"
                (proxy / "v1.0.0.mod").write_text(mod)
                (proxy / "v1.0.0.info").write_text('{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}')
                with zipfile.ZipFile(proxy / "v1.0.0.zip", "w") as archive:
                    archive.writestr(f"{module}@v1.0.0/go.mod", mod)
                    archive.writestr(f"{module}@v1.0.0/value.go", "package gatefixture\nconst Value = 42\n")
                (root / "go.mod").write_text(f"module gate-fixture\n\ngo 1.24\nrequire {module} v1.0.0\n")
                (root / "cmd/regression/download_test.go").write_text('''package regression
import ("testing"; "example.com/gatefixture")
// 首次加载本地模块后检查真实依赖值；临时代理与缓存由 Python 夹具回收。
func TestDownloaded(t *testing.T) { if gatefixture.Value != 42 { t.Fatal("dependency value") } }
''')
                env.update(GOMODCACHE=str(root / "module-cache"), GOPROXY=(root / "proxy").as_uri(),
                           GOSUMDB="off", GOPRIVATE="", GONOPROXY="", GOFLAGS="-mod=mod", GOTOOLCHAIN="local")
            result = subprocess.run(["bash", str(root / "scripts/regression.sh"), pattern],
                                  cwd=root, env=env, text=True, capture_output=True, timeout=45)
            logs = list((root / ".e2e/runs").glob("*/regression.log"))
            if logs:
                result.regression_log = logs[0].read_text()
                result.events = logs[0].with_name("events.jsonl").read_text()
            return result

    def test_real_cold_cache_download_does_not_corrupt_events(self):
        """真实 Go 从临时文件代理下载依赖，下载日志保留而 JSON 事件纯净；无外部网络，代理、缓存和模块自动删除。"""
        result = self.run_selection("^TestDownloaded$", cold_cache=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr + getattr(result, "regression_log", ""))
        self.assertIn("go: downloading example.com/gatefixture v1.0.0", result.regression_log)
        events = [json.loads(line) for line in result.events.splitlines()]
        self.assertTrue(any(event.get("Action") == "pass" and event.get("Test") == "TestDownloaded" for event in events))

    def test_real_selected_leaf_passes(self):
        """真实顶层与子用例执行后允许成功；临时目录自动清理。"""
        for pattern in ["^TestTop$", "^TestParent$/valid$"]:
            with self.subTest(pattern=pattern):
                result = self.run_selection(pattern)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("backend regression passed", result.stdout)

    def test_missing_top_or_child_cannot_pass(self):
        """顶层匹配但子用例拼错也必须失败，不能借父测试 PASS 伪报执行。"""
        for pattern in ["^NotATest$", "^TestParent$/missing$", "^TestParent$/valid$/missing$"]:
            with self.subTest(pattern=pattern):
                result = self.run_selection(pattern)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertNotIn("backend regression passed", result.stdout)

    def test_only_skipped_selection_cannot_pass(self):
        """唯一选中用例被跳过时退出失败；可选项跳过不算业务验证通过。"""
        result = self.run_selection("^TestParent$/optional$")
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("backend regression passed", result.stdout)


class BrowserCleanupTests(unittest.TestCase):
    """执行真实 EXIT 清理函数，注入端口占用和 Docker 失败；无容器或生产数据。"""

    def cleanup_result(self, *, occupied=False, schema="e2e_123_456", docker_status=0, original=0, lock_busy=False):
        """创建临时运行目录并执行实际清理函数；返回退出码/日志，socket 与目录均由上下文回收。"""
        source = (SCRIPTS / "e2e.sh").read_text()
        body = source.split("cleanup() {", 1)[1].split("\ntrap cleanup EXIT", 1)[0]
        with tempfile.TemporaryDirectory(prefix="xhub-cleanup-") as directory, socket.socket() as listener:
            root = Path(directory)
            (root / "run").mkdir()
            (root / "lock").mkdir()
            if lock_busy:
                (root / "lock/unexpected-owner").write_text("held")
            (root / "bin").mkdir()
            (root / "run/schema.txt").write_text(schema + "\n")
            docker = root / "bin/docker"
            docker.write_text("#!/bin/sh\nexit " + str(docker_status) + "\n")
            docker.chmod(0o755)
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
            if occupied:
                listener.listen()
            else:
                listener.close()
            env = dict(os.environ, PATH=str(root / "bin") + os.pathsep + os.environ["PATH"],
                       E2E_RUN_DIR=str(root / "run"), LOCK=str(root / "lock"), E2E_DATABASE="xhub",
                       E2E_GW_PORT=str(port), E2E_UP_PORT=str(port), E2E_BROWSER_REPORT_DIR="")
            script = "set -euo pipefail\ncleanup() {" + body + "\ntrap cleanup EXIT\nexit " + str(original)
            result = subprocess.run(["bash", "-c", script], env=env, text=True, capture_output=True, timeout=10)
            result.cleanup_status = json.loads((root / "run/cleanup-status.json").read_text())
            return result

    def test_cleanup_normal_and_failures(self):
        """正常清理可通过；占用端口、非法 schema、删除失败必须非零，原测试失败码保留。"""
        for options, expected in [({}, 0), ({"lock_busy": True}, 1), ({"occupied": True}, 1), ({"schema": "unsafe;drop"}, 1),
                                  ({"docker_status": 9}, 1), ({"docker_status": 9, "original": 7}, 7)]:
            with self.subTest(options=options):
                result = self.cleanup_result(**options)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertEqual(result.cleanup_status["cleanup_exit"], int(bool(options)))
                self.assertEqual(result.cleanup_status["test_exit"], options.get("original", 0))


if __name__ == "__main__":
    unittest.main()
