"""真实验收 support 模块的单元测试；各用例独立管理夹具与清理。"""
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
from acceptance import Dataset, acceptance_run_id, hierarchy, load_manifest, temporary_retry_delay, write_private, resolve_document
from acceptance import runner_database as DATABASE


class AcceptanceTests(unittest.TestCase):
    """验证所属业务模块的正常、边界和失败路径，资源由各测试自行隔离清理。"""

    def test_hierarchy(self):
        """目的：固定清单展开27个唯一成员和81把钥匙；前置真实清单，断言团队均三人，无外部数据清理。"""
        data = load_manifest()
        rows = hierarchy(data)
        self.assertEqual(len(rows), 27)
        self.assertEqual(len({r["email"] for r in rows}), 27)
        self.assertEqual(len(rows) * len(data["key_profiles"]), 81)
        for org in data["organizations"]:
            for team in data["teams"]:
                self.assertEqual(sum(r["organization"] == org and r["team"] == team for r in rows), 3)


    def test_acceptance_run_id_cannot_be_redacted_as_phone_number(self):
        """目的：运行标记不能被默认手机号护栏改写；前置固定长度，验证仅含小写字母且长度准确，无外部数据需要清理。"""
        for _ in range(100):
            value = acceptance_run_id(18)
            self.assertEqual(len(value), 18)
            self.assertTrue(value.isascii() and value.isalpha() and value.islower())


    def test_temporary_retry_delay_backs_off_and_honors_rate_limit_window(self):
        """目的：真实供应商短时断连不能在数秒内耗尽全部重试；前置普通 502 与上游 429 摘要，验证指数退避、封顶和完整限流冷却，无外部数据需要清理。"""
        self.assertEqual([temporary_retry_delay("upstream 502", 3, 65, attempt)
                          for attempt in range(1, 7)], [3, 6, 12, 24, 48, 65])
        self.assertEqual(temporary_retry_delay("upstream 429 rate limit", 3, 65, 1), 65)


    def test_xgo_modify_checks_debug_output_and_current_real_upstream_body(self):
        """目的：XGo modify 必须同时证明调试响应和当前真实外发正文已脱敏；前置单条真实清单规则和内存观察器，验证原文被替换且旧观察记录不参与断言，临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest())
            definition = next(row for row in dataset.data["guardrails"]
                              if row.get("acceptance", {}).get("action") == "modify")
            dataset.data["guardrails"] = [definition]
            dataset.observer = type("ObserverStub", (), {"rows": [
                {"messages": "旧请求 XGO_PRIVATE", "status": 200}],
                "lock": __import__("threading").Lock()})()
            dataset.api = mock.Mock(side_effect=[
                ({"guardrails": [{"guardrail_name": definition["guardrail_name"],
                                    "guardrail_id": "guard-modify"}]}, {}),
                ({"litellm_params": definition["litellm_params"]}, {}),
                ({"action": "modify", "response_text": definition["acceptance"]["replacement"]}, {}),
            ])

            def real_call(key, marker, extra, expected, guardrails):
                """模拟护栏处理后的当前真实上游正文；参数与 Dataset.call 一致，无返回；只把脱敏结果写入观察器。"""
                dataset.observer.rows.append({
                    "messages": marker + " " + extra.replace(
                        definition["acceptance"]["text"], definition["acceptance"]["replacement"]),
                    "status": 200,
                })

            key = {"call_model": "gpt-5.6-sol"}
            with mock.patch.object(dataset, "call", side_effect=real_call):
                dataset.verify_xgo(key)
            self.assertTrue(dataset.checked(definition["guardrail_name"] + "-persist-debug-real-request"))

            dataset.report["checks"] = []
            dataset.api = mock.Mock(side_effect=[
                ({"guardrails": [{"guardrail_name": definition["guardrail_name"],
                                    "guardrail_id": "guard-modify"}]}, {}),
                ({"litellm_params": definition["litellm_params"]}, {}),
                ({"action": "modify", "response_text": definition["acceptance"]["text"]}, {}),
            ])
            with self.assertRaisesRegex(AssertionError, "调试响应没有返回脱敏后的正文"):
                dataset.verify_xgo(key)


    def test_invalid_manifest(self):
        """目的：拒绝重复身份、错误版本和非法费率；前置清单副本，验证失败输入，不写库，临时文件自动清理。"""
        for kind in ("version", "duplicate", "rate", "profiles"):
            data = copy.deepcopy(load_manifest())
            if kind == "version":
                data["version"] = 2
            elif kind == "duplicate":
                data["organizations"][1]["slug"] = data["organizations"][0]["slug"]
            elif kind == "rate":
                data["billing"]["input_cost_per_token"] = -1
            else:
                data["key_profiles"] = ["inherit"]
            with tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "data.json"
                path.write_text(json.dumps(data))
                with self.subTest(kind=kind), self.assertRaises(ValueError):
                    load_manifest(path)


    def test_manifest_rejects_incomplete_logic_chains(self):
        """目的：真实组合链配置不能弱化缓存、权重或回退断言；前置完整清单副本，逐项破坏重复次数、隔离、失效、零费用、权重、429、同会话不同正文及成功复用要求，验证加载失败，临时文件自动清理。"""
        mutations = {
            "missing": lambda data: data.pop("logic_chains"),
            "replays": lambda data: data["logic_chains"]["cache_consistency"].update(identical_replays=1),
            "isolation": lambda data: data["logic_chains"]["cache_consistency"].update(cross_key_isolation=False),
            "cost": lambda data: data["logic_chains"]["cache_consistency"].update(cache_hit_cost=1),
            "weights": lambda data: data["logic_chains"]["weighted_route_switch"].update(initial_weights=[99, 1]),
            "negative": lambda data: data["logic_chains"]["weighted_route_switch"].update(updated_weights=[-1, 101]),
            "status": lambda data: data["logic_chains"]["fallback_cache"].update(primary_failure_status=500),
            "fallback_replays": lambda data: data["logic_chains"]["fallback_cache"].update(repeat_requests=1),
            "fallback_session": lambda data: data["logic_chains"]["fallback_cache"].update(same_session=False),
            "fallback_bodies": lambda data: data["logic_chains"]["fallback_cache"].update(different_request_bodies=False),
            "fallback_preference": lambda data: data["logic_chains"]["fallback_cache"].update(reuse_successful_target=False),
            "fallback_legacy": lambda data: data["logic_chains"]["fallback_cache"].pop("reuse_successful_target"),
        }
        for name, mutate in mutations.items():
            data = copy.deepcopy(load_manifest())
            mutate(data)
            with tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "data.json"
                path.write_text(json.dumps(data))
                with self.subTest(name=name), self.assertRaises(ValueError):
                    load_manifest(path)


    def test_private_access(self):
        """目的：访问文件每次保持0600；前置临时目录，验证保存后与覆盖后权限，目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "access.json"
            write_private(path, {"key": "private-example"})
            path.chmod(0o644)
            write_private(path, {})
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)


    def test_document_resolution(self):
        """目的：完整路由文档替换键值变量且不修改原件；前置嵌套JSON，验证正常/未声明失败/空边界，无持久化清理。"""
        source = {"$model": [{"id": "$deployment", "weight": 0}]}
        self.assertEqual(resolve_document(source, {"model": "gpt-5.4", "deployment": "deployment-id"}),
                         {"gpt-5.4": [{"id": "deployment-id", "weight": 0}]})
        self.assertIn("$model", source)
        self.assertEqual(resolve_document([], {}), [])
        with self.assertRaisesRegex(ValueError, "未声明模板变量"):
            resolve_document(source, {"model": "gpt-5.4"})


    def test_complete_manifest_rejects_missing_policy(self):
        """目的：缺失角色、源码或变量时在写库前拒绝；前置完整清单副本，验证失败边界，临时目录自动清理。"""
        for kind in ("role", "xgo", "variable"):
            data = copy.deepcopy(load_manifest())
            if kind == "role":
                data["personas"].pop()
            elif kind == "xgo":
                data["guardrails"][-1]["litellm_params"]["custom_code"] = ""
            else:
                data["routing"]["templates"]["fenno-only"]["unknown"] = "$missing"
            with tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "data.json"
                path.write_text(json.dumps(data))
                with self.subTest(kind=kind), self.assertRaises(ValueError):
                    load_manifest(path)


    def test_manifest_declares_exact_real_models_and_protocols(self):
        """目的：锁定真实聊天、图片和视频验收矩阵；前置完整清单，验证模型 ID、协议、入口和创建路径，不产生外部数据。"""
        data = load_manifest()
        models = {row["id"]: row for row in data["models"]}
        self.assertEqual(set(models), {"fenno-chat", "qiniu-chat", "qiniu-glm", "fenno-image",
                         "qiniu-ark-seedance", "qiniu-fal-seedance", "qiniu-fal-kling"})
        expected = {
            "fenno-image": ("gpt-image-2", "bypass_openai_image_generation", "bypass:openai-images", "/bypass/openai/v1/images/generations"),
            "qiniu-ark-seedance": ("bytedance/doubao-seedance-2-0-mini-260615", "qiniu_contents_generation", "bypass:ark-video", "/v3/contents/generations/tasks"),
            "qiniu-fal-seedance": ("bytedance/seedance-2.0/mini/text-to-video", "qiniu_fal_doubao_20", "fal:queue", "/queue/bytedance/seedance-2.0/mini/text-to-video"),
            "qiniu-fal-kling": ("fal-ai/kling-video/v2.5-turbo/pro/text-to-video", "qiniu_fal_kling", "fal:queue", "/queue/fal-ai/kling-video/v2.5-turbo/pro/text-to-video"),
        }
        for ident, (name, transport, endpoint, path) in expected.items():
            with self.subTest(model=ident):
                row = models[ident]
                self.assertEqual(row["public_name"], name)
                self.assertEqual(row["upstream_model"], name)
                self.assertTrue(row["public_name"].isascii())
                self.assertEqual(row["transport"], transport)
                self.assertEqual(row["endpoint_types"], [endpoint])
                self.assertEqual(row["create_path"], path)
        self.assertEqual(models["fenno-chat"]["upstream_model"], "gpt-5.6-sol")
        self.assertEqual(models["qiniu-chat"]["upstream_model"], "openai/gpt-5.6-sol")
        self.assertEqual(models["qiniu-glm"]["upstream_model"], "z-ai/glm-5")
        for ident in ("fenno-chat", "qiniu-chat", "qiniu-glm"):
            self.assertEqual(models[ident]["endpoint_types"], ["chat", "responses"])
        self.assertEqual(data["limit_scenarios"]["model_allowlist"]["disallowed_model"],
                         "xhub/non-allowlisted-model")


    def test_manifest_rejects_business_alias_as_public_model(self):
        """目的：禁止把业务别名冒充真实模型 ID；前置篡改图片公开名，验证加载失败，临时文件自动清理。"""
        data = copy.deepcopy(load_manifest())
        next(row for row in data["models"] if row["id"] == "fenno-image")["public_name"] = "Enterprise Assistant"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "data.json"
            path.write_text(json.dumps(data))
            with self.assertRaisesRegex(ValueError, "公开模型名必须等于真实上游模型 ID"):
                load_manifest(path)


    def test_recover_shared_checkpoints_selects_latest_valid_row_per_key(self):
        """目的：共享数据库恢复为每把当前密钥选择最新合法真实记录；前置一条较新坏响应和一条较旧完整响应，验证跳过坏记录并恢复完整证据，临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest())
            key = {"token_id": "key-1", "profile": "inherit", "call_model": "gpt-5.6-sol",
                   "user_id": "user-1", "team_id": "team-1", "project_id": "project-1",
                   "organization_id": "org-1"}
            dataset.state["keys"] = [key]
            base = {"api_key": "key-1", "user": "user-1", "team_id": "team-1",
                    "project_id": "project-1", "organization_id": "org-1",
                    "model": "gpt-5.6-sol", "prompt_tokens": 11, "completion_tokens": 7,
                    "spend": 0.000025, "metadata": {"cost_breakdown": {"source": "snapshot"}},
                    "messages": [{"content": "dataset-a1b2-3"}]}
            newest_bad = {**base, "request_id": "call-bad",
                          "response": {"choices": [], "usage": {"prompt_tokens": 11, "completion_tokens": 7}}}
            older_good = {**base, "request_id": "call-good",
                           "response": {"choices": [{}], "usage": {"prompt_tokens": 11, "completion_tokens": 7}}}
            output = "\n".join(json.dumps(row) for row in (newest_bad, older_good))
            with mock.patch.object(DATABASE, "sql", return_value=output):
                self.assertEqual(DATABASE.recover_shared_chat_checkpoints(dataset), 1)
            self.assertEqual(dataset.report["calls"][0]["call_id"], "call-good")


    def test_recover_shared_checkpoints_ignores_incomplete_rows(self):
        """目的：共享恢复不能把缺少价格、归属或响应usage的历史行当作通过；前置不完整数据库行，验证恢复数量为零且报告为空，临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest())
            dataset.state["keys"] = [{"token_id": "key-1", "profile": "inherit",
                "call_model": "gpt-5.6-sol", "user_id": "user-1", "team_id": "team-1",
                "project_id": "project-1", "organization_id": "org-1"}]
            row = {"request_id": "call-bad", "api_key": "key-1", "user": "user-1",
                   "team_id": "team-1", "project_id": "project-1", "organization_id": "org-1",
                   "model": "gpt-5.6-sol", "prompt_tokens": 11, "completion_tokens": 7,
                   "spend": 0.000025, "metadata": {}, "messages": [{"content": "dataset-a1b2-3"}],
                   "response": {"choices": [{}], "usage": {"prompt_tokens": 11, "completion_tokens": 7}}}
            with mock.patch.object(DATABASE, "sql", return_value=json.dumps(row)):
                self.assertEqual(DATABASE.recover_shared_chat_checkpoints(dataset), 0)
            self.assertEqual(dataset.report["calls"], [])
