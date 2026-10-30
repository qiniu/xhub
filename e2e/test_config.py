"""隔离网关配置测试，验证可选 Redis，不访问数据库或供应商。"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class GatewayConfigTests(unittest.TestCase):
    """验证浏览器夹具配置的可选异步落库路径。"""

    def test_continuation_cache_price(self):
        """目的：显式开启续接夹具时配置完整缓存价格；未开启时不注入模型，临时目录自动清理。"""
        for enabled in ("0", "1"):
            with self.subTest(enabled=enabled), tempfile.TemporaryDirectory() as directory:
                env = {"PATH": os.environ.get("PATH", ""), "E2E_UP_PORT": "4137",
                       "E2E_MASTER_KEY": "test-only-key", "E2E_SCHEMA": "test_config",
                       "E2E_RUN_DIR": directory, "E2E_RESPONSES_CONTINUATION": enabled}
                subprocess.run([sys.executable, str(Path(__file__).with_name("config.py"))],
                               env=env, check=True, capture_output=True)
                models = json.loads((Path(directory) / "c.yaml").read_text())["model_list"]
                selected = [row for row in models if row["model_name"] in ("e2e-responses-history", "e2e-codex-agent")]
                self.assertEqual(len(selected), 2 if enabled == "1" else 0)
                for model in selected:
                    prices = model["litellm_params"]
                    self.assertEqual(prices["input_cost_per_token"], 0.000001)
                    self.assertEqual(prices["output_cost_per_token"], 0.000002)
                    self.assertEqual(prices["cache_read_input_token_cost"], 0.000001)

    def test_database_target(self):
        """验证默认库、专属库及非法名称；配置不访问数据库，临时文件自动清理。"""
        for database in (None, "xhub_e2e_audit", "../postgres", "xhub;drop", ""):
            with self.subTest(database=database), tempfile.TemporaryDirectory() as directory:
                env = {"PATH": os.environ.get("PATH", ""), "E2E_UP_PORT": "4137",
                       "E2E_MASTER_KEY": "test-only-key", "E2E_SCHEMA": "test_config",
                       "E2E_RUN_DIR": directory}
                if database is not None:
                    env["E2E_DATABASE"] = database
                result = subprocess.run([sys.executable, str(Path(__file__).with_name("config.py"))],
                                        env=env, capture_output=True)
                if database in (None, "xhub_e2e_audit"):
                    self.assertEqual(result.returncode, 0, result.stderr)
                    settings = json.loads((Path(directory) / "c.yaml").read_text())["general_settings"]
                    self.assertIn("/" + (database or "xhub") + "?", settings["database_url"])
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse((Path(directory) / "c.yaml").exists())

    def test_optional_redis(self):
        """目的：隔离环境生成配置，验证未配置、空值及专属 Redis 的原样传递；临时目录自动清理，无外部副作用。"""
        for value in (None, "", "redis://127.0.0.1:6397/1"):
            with self.subTest(redis=value), tempfile.TemporaryDirectory() as directory:
                env = {"PATH": os.environ.get("PATH", ""), "E2E_UP_PORT": "4137",
                       "E2E_MASTER_KEY": "test-only-key", "E2E_SCHEMA": "test_config",
                       "E2E_RUN_DIR": directory}
                if value is not None:
                    env["E2E_REDIS_URL"] = value
                subprocess.run([sys.executable, str(Path(__file__).with_name("config.py"))],
                               env=env, check=True, capture_output=True)
                settings = json.loads((Path(directory) / "c.yaml").read_text())["general_settings"]
                if value:
                    self.assertEqual(settings["redis_url"], value, "专属 Redis 配置必须原样保留")
                else:
                    self.assertNotIn("redis_url", settings, "默认配置不能依赖共享 Redis")
