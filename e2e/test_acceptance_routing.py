"""真实验收 routing 模块的单元测试；各用例独立管理夹具与清理。"""
import tempfile
import json
import threading
import unittest
from unittest import mock
from checklist import CaseBlocked
from acceptance import Dataset, fallback_acceptance_deployments, fallback_acceptance_route, load_manifest, weighted_acceptance_route


class AcceptanceTests(unittest.TestCase):
    """验证所属业务模块的正常、边界和失败路径，资源由各测试自行隔离清理。"""

    def test_weighted_acceptance_route_switches_only_the_active_deployment(self):
        """目的：确定性权重链只在两个真实部署间切换；前置同一模型和两个部署 ID，验证 100:0、0:100、无回退及单次尝试，纯内存结果无需清理。"""
        first = weighted_acceptance_route("gpt-5.6-sol", "a", "b", [100, 0], 300, ["baseline"])
        second = weighted_acceptance_route("gpt-5.6-sol", "a", "b", [0, 100], 300, ["baseline"])
        self.assertEqual(first["model_routes"][0]["allocations"], [
            {"deployment_id": "a", "weight": 100}, {"deployment_id": "b", "weight": 0}, {"deployment_id": "baseline", "weight": 0}])
        self.assertEqual(second["model_routes"][0]["allocations"], [
            {"deployment_id": "a", "weight": 0}, {"deployment_id": "b", "weight": 100}, {"deployment_id": "baseline", "weight": 0}])
        for category in ("fallbacks", "context_window_fallbacks", "content_policy_fallbacks"):
            self.assertEqual(first[category], [{"gpt-5.6-sol": []}])
        self.assertEqual(first["retry_policy"]["max_attempts"], 1)

    def test_weighted_route_rejects_ambiguous_ids_and_invalid_weights(self):
        """目的：确定性路由拒绝重复部署和非法分配；前置纯内存样本，验证空、重复、负数、非有限及全零权重失败，无资源需要清理。"""
        for first, second, excluded in (("a", "a", []), ("", "b", []), ("a", "b", ["a"]), ("a", "b", ["c", "c"])):
            with self.subTest(ids=(first, second, excluded)), self.assertRaises(ValueError):
                weighted_acceptance_route("model", first, second, [100, 0], 60, excluded)
        for weights in ([], [1], [1, 2, 3], [0, 0], [-1, 1], [float("nan"), 1], [float("inf"), 1], [True, 0]):
            with self.subTest(weights=weights), self.assertRaises(ValueError):
                weighted_acceptance_route("model", "a", "b", weights, 60)


    def test_fallback_acceptance_route_pins_zero_weight_and_real_fallback(self):
        """目的：零权重验收不能被现有组织或团队模板覆盖；前置主模型故障/健康部署及真实备用模型，验证临时密钥模板显式写入100:0、备用部署和回退链，纯内存结果无需清理。"""
        body = fallback_acceptance_route("gpt-5.6-sol", "fault", "healthy", "z-ai/glm-5", "qiniu", 300, ["other-glm"])
        self.assertEqual(body["model_routes"][0]["allocations"], [
            {"deployment_id": "fault", "weight": 100},
            {"deployment_id": "healthy", "weight": 0},
        ])
        self.assertEqual(body["model_routes"][1]["allocations"], [
            {"deployment_id": "qiniu", "weight": 100},
            {"deployment_id": "other-glm", "weight": 0},
        ])
        self.assertEqual(body["fallbacks"], [{"gpt-5.6-sol": ["z-ai/glm-5"]}, {"z-ai/glm-5": []}])
        self.assertEqual(body["retry_policy"]["max_attempts"], 1)


    def test_fallback_acceptance_deployments_excludes_all_shared_model_suppliers(self):
        """目的：同一公开模型跨供应商时全部健康部署必须置零；前置七牛部署排在 Fenno 之前及临时部署，验证选择独立 GLM 目标并排除临时记录，纯内存无需清理。"""
        deployments = [
            {"id": "qiniu-shared", "public_name": "gpt-5.6-sol", "provider": "qiniu"},
            {"id": "fenno-shared", "public_name": "gpt-5.6-sol", "provider": "fennoai"},
            {"id": "other-glm", "public_name": "z-ai/glm-5", "provider": "fennoai"},
            {"id": "qiniu-glm", "public_name": "z-ai/glm-5", "provider": "qiniu"},
            {"id": "old-fault", "public_name": "gpt-5.6-sol", "provider": "fennoai", "temporary_fault": True},
            {"id": "temp-glm", "public_name": "z-ai/glm-5", "provider": "qiniu", "temporary": True},
        ]
        healthy, target = fallback_acceptance_deployments(deployments, "gpt-5.6-sol", "z-ai/glm-5", "qiniu")
        self.assertEqual(healthy, ["qiniu-shared", "fenno-shared"])
        self.assertEqual(target, "qiniu-glm")
        body = fallback_acceptance_route("gpt-5.6-sol", "fault", healthy, "z-ai/glm-5", target, 300)
        self.assertEqual(body["model_routes"][0]["allocations"], [
            {"deployment_id": "fault", "weight": 100},
            {"deployment_id": "qiniu-shared", "weight": 0},
            {"deployment_id": "fenno-shared", "weight": 0},
        ])


    def test_fallback_acceptance_deployments_rejects_invalid_baseline(self):
        """目的：无效基线须在创建临时数据前明确失败；前置同名主备、缺失主备或重复备用，验证抛 ValueError，纯内存无需清理。"""
        primary = {"id": "healthy", "public_name": "primary", "provider": "fennoai"}
        backup = {"id": "backup", "public_name": "backup", "provider": "qiniu"}
        for rows, source, target in (([primary], "primary", "primary"),
                                     ([primary], "primary", "backup"),
                                     ([backup], "primary", "backup"),
                                     ([primary, backup, {**backup, "id": "duplicate"}], "primary", "backup")):
            with self.subTest(rows=rows, source=source, target=target), self.assertRaises(ValueError):
                fallback_acceptance_deployments(rows, source, target, "qiniu")
        with self.assertRaisesRegex(ValueError, "不同的公开模型"):
            fallback_acceptance_route("shared", "fault", ["healthy"], "shared", "backup", 300)


    def test_route_evidence_rejects_missing_duplicate_or_wrong_facts(self):
        """目的：路由证据不能沿用缺失或错误事实；前置内存数据库响应，覆盖正常、空、重复、串供应商、型号、缓存及外发路径错误；临时目录自动清理。"""
        good = dict(request_id="call", supplier="qiniu", upstream_model="actual", cache_hit=False,
                    outbound_endpoint="/v1/chat/completions", status="success", http_status=200)
        cases = [[good], [], [good, good], [{**good, "supplier": "other"}],
                 [{**good, "upstream_model": "wrong"}], [{**good, "cache_hit": True}],
                 [{**good, "outbound_endpoint": ""}]]
        for index, rows in enumerate(cases):
            with self.subTest(index=index), tempfile.TemporaryDirectory() as directory:
                dataset = Dataset("http://localhost:1", directory, load_manifest())
                with mock.patch.object(dataset, "wait_log"), mock.patch("acceptance.routing.sql", return_value=json.dumps(rows)):
                    if index == 0:
                        self.assertEqual(dataset.route_evidence("call", "qiniu", "actual"), good)
                    else:
                        with self.assertRaises(AssertionError):
                            dataset.route_evidence("call", "qiniu", "actual")
                with self.assertRaises(AssertionError):
                    dataset.route_evidence(None, "qiniu")

    def test_profile_route_isolation_uses_current_persisted_receipts(self):
        """目的：两个供应商配置每轮主动调用并核对精确请求；前置两把密钥，验证调用型号、当前证据及串供应商失败；内存模拟，无外部数据，临时目录清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset("http://localhost:1", directory, load_manifest())
            dataset.state["keys"] = [{"profile": "fenno-only", "call_model": "gpt"},
                                     {"profile": "qiniu-only", "call_model": "gpt"}]
            dataset.state["selected"] = {"fennoai": {"model": "gpt"}, "qiniu": {"model": "openai/gpt"}}
            def called(key, marker, model=None):
                """记录本轮独立调用回执；参数为密钥、标记及型号，无返回；只写内存报告供隔离断言读取。"""
                dataset.report["calls"].append({"call_id": key["profile"], "marker": marker})
            with mock.patch.object(dataset, "call", side_effect=called) as call, mock.patch.object(dataset, "route_evidence") as evidence:
                dataset.verify_profile_route_isolation()
                dataset.verify_profile_route_isolation()
                self.assertEqual(call.call_count, 4, "历史成功不能跳过当前供应商请求")
                evidence.assert_any_call("fenno-only", "fennoai", "gpt")
                evidence.assert_any_call("qiniu-only", "qiniu", "openai/gpt")
            with mock.patch.object(dataset, "call", side_effect=called), mock.patch.object(dataset, "route_evidence", side_effect=AssertionError("串供应商")):
                with self.assertRaisesRegex(AssertionError, "串供应商"):
                    dataset.verify_profile_route_isolation()

    def test_real_cache_and_fallback_chains_clean_resources_on_success_and_failure(self):
        """目的：真实路由链不依赖观察器且错误时清理资源；前置管理接口与真实响应契约夹具，覆盖缓存切换、429/5xx 回退成功、错误缓存、主模型无故障及真实 404 不满足回退条件；临时目录自动清理。"""
        for chain in ("cache", "fallback"):
            for invalid in ((False, "eligible-5xx", True, "ineligible-404") if chain == "fallback" else (False, True)):
                with self.subTest(chain=chain, invalid=invalid), tempfile.TemporaryDirectory() as directory:
                    dataset = Dataset("http://localhost:1", directory, load_manifest())
                    dataset.logger = mock.Mock()
                    dataset.state.update(chat_models={"fennoai": "gpt-5.6-sol"}, selected={
                        "fennoai": {"provider": {"id": "fennoai"}}, "qiniu": {"provider": {"id": "qiniu"}}})
                    target = next(r for r in dataset.data["models"] if r["id"] == "qiniu-glm")
                    baseline = [{"id": "healthy", "public_name": "gpt-5.6-sol", "provider": "fennoai"},
                                {"id": "fallback", "public_name": target["public_name"], "provider": "qiniu"}]
                    dataset.state["deployments"] = list(baseline)
                    created, requests = [], []
                    def deployment(*args, **kwargs):
                        """模拟注册本轮部署并保存状态；参数同生产接口，返回唯一 ID；仅修改内存以验证异常清理。"""
                        ident = "temporary-" + str(len(created))
                        created.append(ident)
                        dataset.state["deployments"].append({"id": ident, "public_name": kwargs["public_name"]})
                        return ident
                    def api(route, body):
                        """模拟管理资源创建和清理；参数为路径及正文，返回 ID 或密钥；不访问真实服务。"""
                        requests.append((route, body))
                        if route == "/key/generate":
                            return {"key": str(len(requests))}, {}
                        return {"id": "route"}, {}
                    answer = {"choices": [{}], "usage": {"prompt_tokens": 2, "completion_tokens": 1}}
                    hit = {"x-litellm-cache-hit": "true"}
                    responses = [(answer, {}, 200, "first"), (answer, hit, 200, "second"),
                                 (answer, {}, 200, "isolated"), ({}, {}, 401, "blocked"),
                                 (answer, {}, 200, "switched"), (answer, hit, 200, "repeated")]
                    if chain == "cache" and invalid:
                        responses[1] = (answer, {}, 200, "second")
                    if chain == "fallback":
                        responses = [({"error": "upstream refused"}, {}, 502, "fault")]
                        responses += [(answer, {}, 200, "turn-" + str(i)) for i in range(dataset.data["logic_chains"]["fallback_cache"]["repeat_requests"])]
                        responses += [({"error": "model not in allowed model list"}, {}, 401, "denied")]
                    def bill(call_id):
                        """返回当前主模型及明确会话账单；参数为请求 ID，返回账单，避免历史身份替代当前请求。"""
                        model = "gpt-5.6-sol"
                        return {"model": model, "session_id": "real-fallback-" + dataset.run}
                    fallback_env = {"E2E_FALLBACK_UPSTREAM_MODEL": "fault-model"} if chain == "fallback" else {}
                    with mock.patch.dict("os.environ", fallback_env, clear=True), \
                         mock.patch.object(dataset, "deployment", side_effect=deployment), mock.patch.object(dataset, "api", side_effect=api), \
                         mock.patch.object(dataset, "chat_request", side_effect=responses), mock.patch.object(dataset, "wait_bill", side_effect=bill), \
                         mock.patch.object(dataset, "wait_log", return_value={"metadata": {"upstream_response": {
                             "status_code": 404 if invalid == "ineligible-404" else 503 if invalid == "eligible-5xx" else 429}}}), \
                         mock.patch.object(dataset, "route_evidence", return_value={"status": "success" if invalid is True else "error"}) as evidence, \
                         mock.patch("acceptance.routing.bill_check", return_value=0.1), mock.patch("acceptance.routing.cache_bill_check"):
                        callback = dataset.verify_cache_weight_chain if chain == "cache" else dataset.verify_fallback_policy_chain
                        if invalid is True or invalid == "ineligible-404":
                            with self.assertRaisesRegex(AssertionError, "前置条件未满足" if invalid == "ineligible-404" else "."):
                                callback({"user_id": "u", "team_id": "t", "project_id": "p"})
                        else:
                            callback({"user_id": "u", "team_id": "t", "project_id": "p"})
                            self.assertGreaterEqual(evidence.call_count, 3)
                    self.assertEqual(dataset.state["deployments"], baseline)
                    self.assertEqual(sum(route == "/key/delete" for route, _ in requests), 2)
                    self.assertIn(("/route_template/route/delete", {}), requests)
                    for ident in created:
                        self.assertIn(("/model/delete", {"id": ident}), requests)

    def test_shared_fallback_without_explicit_fault_model_blocks_before_writes(self):
        """目的：共享回退缺少可控 429/5xx 型号时不得制造 404；前置未设置或空白环境变量，验证明确阻塞且部署、管理接口和供应商请求均未调用；临时目录自动清理。"""
        for value in (None, "", "   "):
            with self.subTest(value=value), tempfile.TemporaryDirectory() as directory:
                dataset = Dataset("http://localhost:1", directory, load_manifest())
                dataset.state["chat_models"] = {"fennoai": "gpt-5.6-sol"}
                deployment = mock.Mock(side_effect=AssertionError("不得创建部署"))
                api = mock.Mock(side_effect=AssertionError("不得创建管理资源"))
                request = mock.Mock(side_effect=AssertionError("不得调用供应商"))
                environment = {} if value is None else {"E2E_FALLBACK_UPSTREAM_MODEL": value}
                with mock.patch.dict("os.environ", environment, clear=True), \
                     mock.patch.object(dataset, "deployment", deployment), \
                     mock.patch.object(dataset, "api", api), \
                     mock.patch.object(dataset, "chat_request", request), \
                     self.assertRaisesRegex(CaseBlocked, "E2E_FALLBACK_UPSTREAM_MODEL"):
                    dataset.verify_fallback_policy_chain({"user_id": "u", "team_id": "t", "project_id": "p"})
                deployment.assert_not_called()
                api.assert_not_called()
                request.assert_not_called()

    def test_isolated_fallback_uses_observer_fault_without_environment_model(self):
        """目的：隔离验收继续使用 Observer 的受控 429；前置隔离标记与观察器，验证默认清单型号和 fault 参数传入部署，不依赖环境变量；不创建真实资源。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset = Dataset("http://localhost:1", directory, load_manifest(), observer=object())
            dataset.isolated_upstreams = True
            dataset.state.update(chat_models={"fennoai": "gpt-5.6-sol"}, selected={
                "fennoai": {"provider": {"id": "fennoai"}}, "qiniu": {"provider": {"id": "qiniu"}}})
            target = next(row for row in dataset.data["models"] if row["id"] == "qiniu-glm")
            dataset.state["deployments"] = [
                {"id": "healthy", "public_name": "gpt-5.6-sol", "provider": "fennoai"},
                {"id": "fallback", "public_name": target["public_name"], "provider": "qiniu"},
            ]
            captured = {}
            def deployment(definition, provider, upstream, **kwargs):
                """记录隔离故障部署参数；参数同生产方法，返回临时 ID；只修改内存状态供 finally 清理。"""
                captured.update(upstream=upstream, kwargs=kwargs)
                dataset.state["deployments"].append({"id": "temporary", "public_name": kwargs["public_name"]})
                return "temporary"
            requests = []
            def api(route, body):
                """模拟临时路由、密钥与删除接口；参数为路径和正文，返回契约夹具；记录清理但不访问服务。"""
                requests.append((route, body))
                if route == "/key/generate":
                    return {"key": str(len(requests))}, {}
                return {"id": "route"}, {}
            answer = {"choices": [{}], "usage": {"prompt_tokens": 2, "completion_tokens": 1}}
            responses = [({"error": "rate limited"}, {}, 429, "fault")]
            responses += [(answer, {}, 200, "turn-" + str(index))
                          for index in range(dataset.data["logic_chains"]["fallback_cache"]["repeat_requests"])]
            responses += [({"error": "model not in allowed model list"}, {}, 401, "denied")]
            with mock.patch.dict("os.environ", {}, clear=True), \
                 mock.patch.object(dataset, "deployment", side_effect=deployment), \
                 mock.patch.object(dataset, "api", side_effect=api), \
                 mock.patch.object(dataset, "chat_request", side_effect=responses), \
                 mock.patch.object(dataset, "wait_log", return_value={"metadata": {"upstream_response": {"status_code": 429}}}), \
                 mock.patch.object(dataset, "wait_bill", return_value={"model": "gpt-5.6-sol", "session_id": "real-fallback-" + dataset.run}), \
                 mock.patch.object(dataset, "route_evidence", return_value={"status": "error"}), \
                 mock.patch("acceptance.routing.bill_check", return_value=0.1):
                dataset.verify_fallback_policy_chain({"user_id": "u", "team_id": "t", "project_id": "p"})
            definition = next(row for row in dataset.data["models"] if row["id"] == "fenno-chat")
            self.assertEqual(captured["upstream"], definition["upstream_model"])
            self.assertTrue(captured["kwargs"]["fault"])
            self.assertNotIn("temporary", {row["id"] for row in dataset.state["deployments"]})

    def test_deployment_route_tag_builds_distinct_observer_address(self):
        """目的：同模型临时部署必须生成可观察的独立地址；前置模拟观察器和模型创建接口，验证 weight-b 写入 api_base、状态保存标签及临时资源标记，不创建真实部署。"""
        with tempfile.TemporaryDirectory() as directory:
            observer = type("ObserverStub", (), {"base": "http://127.0.0.1:4567"})()
            dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), observer=observer)
            dataset.isolated_upstreams = True
            definition = next(row for row in dataset.data["models"] if row["id"] == "fenno-chat")
            provider = dataset.data["providers"][0]
            captured = []

            def api(route, body):
                """记录部署请求并返回固定 ID；参数为路由和正文，返回模拟管理响应；仅用于内存断言。"""
                captured.append((route, body))
                return {"model_info": {"id": "deployment-tagged"}}, {}

            dataset.api = api
            ident = dataset.deployment(definition, provider, "gpt-5.6-sol", route_tag="weight-b")
            self.assertEqual(ident, "deployment-tagged")
            self.assertEqual(captured[0][1]["litellm_params"]["api_base"],
                             "http://127.0.0.1:4567/fennoai/weight-b")
            self.assertEqual(dataset.state["deployments"][0]["route_tag"], "weight-b")
            self.assertTrue(dataset.state["deployments"][0]["temporary"])
