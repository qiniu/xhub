"""真实验收 limits_permissions 模块的单元测试；各用例独立管理夹具与清理。"""
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
from acceptance import Dataset, load_manifest


class AcceptanceTests(unittest.TestCase):
    """验证所属业务模块的正常、边界和失败路径，资源由各测试自行隔离清理。"""

    def test_temporary_limits_isolate_routing_identity_and_cleanup_failures(self):
        """目的：临时归属链复用实际路由且绝不混入基线 ID；前置内存 API，覆盖默认/继承模板、各创建失败、验证失败和删除失败；验证全部已创建项逆序清理，临时报告自动删除。"""
        creates = ("/organization/new", "/user/new", "/team/new", "/project/new", "/key/generate")
        deletes = ("/organization/delete", "/user/delete", "/team/delete", "/project/delete", "/key/delete")
        for mode in ("builtin", "template", "invalid-binding", "body-failure", "cleanup-failure", *creates):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), logger=lambda _: None)
                source = {"token_id": "baseline-key", "call_model": "unit-model",
                          "organization_id": "original-org", "team_id": "original-team",
                          "user_id": "original-user", "project_id": "original-project"}
                calls = []

                def api(route, body=None, **kwargs):
                    """用途：记录临时归属链请求并注入创建/删除错误；参数兼容管理 API，返回临时 ID 和空响应头；单测调用，仅写内存，不连接后台。"""
                    calls.append((route, body, kwargs))
                    if route.startswith("/route_template/binding?"):
                        return {"effective": (None if mode == "invalid-binding" else
                            {"scope_type": "builtin"} if mode == "builtin" else
                            {"scope_type": "organization", "template_id": "effective-route"})}, {}
                    if route == mode or mode == "cleanup-failure" and route == "/key/delete":
                        raise RuntimeError("injected failure")
                    return {"organization_id": "temp-org", "team_id": "temp-team",
                            "user_id": "temp-user", "project_id": "temp-project",
                            "token_id": "temp-key", "key": "temporary-secret"}, {}

                dataset.api = api

                def exercise():
                    """用途：消费上下文并核对路由与所有临时归属；无参数/返回，供成功及异常单测调用；可注入业务失败，资源始终由上下文清理。"""
                    with dataset.temporary_limit_subject(source, "unit") as subject:
                        for field in ("organization_id", "team_id", "user_id", "project_id", "token_id"):
                            self.assertTrue(subject[field].startswith("temp-"), field + " 不得复用基线归属")
                        self.assertEqual(subject["profile"], "limits-temporary")
                        generated = next(body for route, body, _ in calls if route == "/key/generate")
                        self.assertEqual(generated["models"], ["unit-model"])
                        self.assertEqual(generated["route_template_id"], None if mode == "builtin" else "effective-route")
                        self.assertIsNone(generated["max_budget"])
                        team_body = next(body for route, body, _ in calls if route == "/team/new")
                        self.assertEqual(team_body["admin_user_id"], "temp-user", "团队不得隐式加入基线管理员")
                        if mode == "body-failure":
                            raise RuntimeError("injected failure")

                if mode == "invalid-binding":
                    with self.assertRaisesRegex(AssertionError, "生效路由"):
                        exercise()
                elif mode in creates or mode in ("body-failure", "cleanup-failure"):
                    with self.assertRaisesRegex(RuntimeError, "injected failure"):
                        exercise()
                else:
                    exercise()
                created_count = creates.index(mode) if mode in creates else 0 if mode == "invalid-binding" else 5
                self.assertEqual([route for route, _, _ in calls if route in deletes],
                                 list(reversed(deletes[:created_count])), "所有已创建资源必须逆序删除，即使某次删除失败")
                for route, body, _ in calls:
                    if route == "/user/delete":
                        self.assertEqual(body, {"user_id": "temp-user"}, "删除单个用户必须使用 user_id 契约")
                self.assertEqual(source["token_id"], "baseline-key")


    def test_allocated_limits_cover_all_scopes_and_record_only_complete_checks(self):
        """目的：五预算以 403、四层 RPM/TPM 以 429 拒绝，且每项恢复调用和清理完成才记通过；前置模拟临时归属链，覆盖成功、恢复失败及已有检查点跳过；临时报告自动清理，不写数据库。"""
        for fail in (False, True):
            with self.subTest(fail=fail), tempfile.TemporaryDirectory() as directory:
                dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), logger=lambda _: None)
                subject = {"organization_id": "temporary-org", "team_id": "temporary-team",
                           "user_id": "temporary-user", "project_id": "temporary-project",
                           "token_id": "temporary-key", "call_model": "unit-model"}
                context = mock.MagicMock()
                context.__enter__.return_value = subject
                dataset.temporary_limit_subject = mock.Mock(return_value=context)
                dataset.api = mock.Mock(return_value=({}, {}))
                dataset.refuse = mock.Mock()
                dataset.call = mock.Mock(side_effect=RuntimeError("recovery failed") if fail else None)
                if fail:
                    with self.assertRaisesRegex(RuntimeError, "recovery failed"):
                        dataset.verify_allocated_limits({"call_model": "unit-model"})
                    self.assertEqual(dataset.report["checks"], [])
                    self.assertEqual(context.__exit__.call_count, 1)
                    continue
                dataset.verify_allocated_limits({"call_model": "unit-model"})
                self.assertEqual(dataset.call.call_count, 13)
                self.assertEqual(dataset.refuse.call_count, 13)
                for refusal in dataset.refuse.call_args_list:
                    reason = refusal.args[3]
                    expected = 403 if reason == "budget" else 429
                    self.assertEqual(refusal.args[2], expected,
                                     f"{reason} 拒绝状态应为 {expected}，不能混用预算和限流状态")
                self.assertEqual(context.__exit__.call_count, 13)
                writes = [call.args[1] for call in dataset.api.call_args_list]
                for index, scenario in enumerate(dataset.data["limit_scenarios"]["budgets"]):
                    self.assertEqual(writes[index * 2]["max_budget"], 0)
                    self.assertEqual(writes[index * 2 + 1]["max_budget"], dataset.data["budgets"][scenario["scope"]])
                self.assertEqual(len(dataset.report["checks"]), 13)
                dataset.verify_allocated_limits({"call_model": "unit-model"})
                self.assertEqual(dataset.call.call_count, 13, "明确通过的检查点应跳过")


    def test_permissions_separate_limits_from_models_and_status(self):
        """目的：防止组织管理员分配额度再次被旧验收拒绝；前置四角色及原值含 null/零的团队，核对每项请求的状态码和原值，并验证意外放行立即失败；不连接后台，临时报告自动清理。"""
        for unexpected_allow in (False, True):
            with self.subTest(unexpected_allow=unexpected_allow), tempfile.TemporaryDirectory() as directory:
                dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), logger=lambda _: None)
                dataset.admin = "platform-session"
                dataset.state["member_password"] = "unit-password"
                for persona in dataset.data["personas"]:
                    if persona["role"] == "platform_admin":
                        continue
                    dataset.state["users"].append({
                        "email": persona["role"], "organization_slug": persona["organization"],
                        "team_slug": persona["team"], "member_index": persona["member_index"],
                        "organization_id": "own-org", "team_id": "own-team"})
                dataset.state["teams"] = [{"id": "foreign-team", "organization_id": "foreign-org"}]
                info = {"max_budget": 30, "rpm_limit": None, "tpm_limit": 0,
                        "models": ["unit-model"], "status": "active"}
                calls = []

                def api(route, body=None, token=None, expected=200, method=None):
                    """用途：按独立权限矩阵模拟接口并记录请求；参数兼容 Dataset.api，返回正文和响应头；状态码不符抛错，仅写内存，供权限单测调用。"""
                    role = "platform_admin" if token in (None, "platform-session") else token
                    if route == "/v2/login":
                        return {"key": body["username"]}, {}
                    if route == "/team/list":
                        return [{}] * {"platform_admin": 9, "organization_admin": 3,
                                       "team_admin": 1, "member": 1}[role], {}
                    if route.startswith("/team/info?"):
                        return {"team_info": info.copy()}, {}
                    allowed = role == "platform_admin"
                    if route == "/team/update":
                        calls.append((role, body.copy(), expected))
                        own = body["team_id"] == "own-team"
                        if "team_description" in body:
                            allowed |= own and role in ("organization_admin", "team_admin")
                        elif any(limit in body for limit in ("max_budget", "rpm_limit", "tpm_limit")):
                            allowed |= own and role == "organization_admin"
                        if unexpected_allow and role == "organization_admin" and "models" in body:
                            allowed = True
                    actual = 200 if allowed else 403
                    if actual != expected:
                        raise AssertionError(f"{role} {route}: actual={actual}, expected={expected}")
                    return {}, {}

                dataset.api = api
                if unexpected_allow:
                    with self.assertRaisesRegex(AssertionError, "organization_admin.*actual=200, expected=403"):
                        dataset.verify_permissions()
                else:
                    dataset.verify_permissions()
                    self.assertEqual(len(dataset.state["personas"]), 4)
                    for role in ("platform_admin", "organization_admin", "team_admin", "member"):
                        for tid in ("own-team", "foreign-team"):
                            for limit in ("max_budget", "rpm_limit", "tpm_limit"):
                                matches = [(body, status) for caller, body, status in calls
                                           if caller == role and body["team_id"] == tid and limit in body]
                                self.assertEqual(len(matches), 1, f"{role}/{tid}/{limit} 必须独立验收")
                                self.assertEqual(matches[0][0][limit], info[limit], "不得改变团队原有配置")


    def test_permission_manifest_requires_distinct_limit_and_model_expectations(self):
        """目的：缺失或非法权限预期必须在写库前失败；前置真实清单副本，覆盖四个必填字段及无效状态码，临时清单自动清理。"""
        for field in ("own_team_limits", "foreign_team_limits", "team_models_update", "team_status_update"):
            for value in (None, 201):
                with self.subTest(field=field, value=value), tempfile.TemporaryDirectory() as directory:
                    data = load_manifest()
                    data["permission_scenarios"][0][field] = value
                    path = Path(directory) / "manifest.json"
                    path.write_text(json.dumps(data))
                    with self.assertRaisesRegex(ValueError, "预期状态码"):
                        load_manifest(path)


