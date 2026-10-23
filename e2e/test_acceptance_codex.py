"""Codex 验收模拟器的标准会话协议与持久化证据单元测试。"""
import copy
import json
import tempfile
import unittest
from unittest import mock

from acceptance import Dataset, load_manifest


def codex_fixture(directory):
    """用途：为模拟器测试创建三轮响应与完整账单；参数为临时目录，返回数据集、密钥、会话和账单。

    调用方用于执行及断点复验；固定英文输入、五级身份与价格，不访问网络，目录由调用方清理。
    """
    dataset = Dataset("http://127.0.0.1:1", directory, load_manifest(), logger=lambda _: None)
    key = dict(key="secret", token_id="k", user_id="u", team_id="t",
               project_id="p", organization_id="o", profile="inherit", call_model="gpt-test")
    conversation = dict(agent_type="codex", session_id="codex-test", marker="project-test",
                        key_id="k", profile="inherit", model="gpt-test", turns=[])
    config = dataset.data["agent_conversation"]
    history, bills = [], []
    for index in range(1, 4):
        prompt = ["Remember the project marker project-test. Reply with only that marker.",
                  "What project marker did I give you? Reply with only the marker.",
                  "Confirm the same project marker once more. Reply with only the marker."][index - 1]
        request = dict(model="gpt-test", instructions=config["instructions"],
                       input=[dict(role="user", content=prompt)], store=False, stream=False,
                       max_output_tokens=config["max_output_tokens"])
        previous = "resp-%d" % (index - 1) if index > 1 else None
        if previous:
            request["previous_response_id"] = previous
        history += request["input"]
        stored = {name: value for name, value in request.items()
                  if name not in ("previous_response_id", "store")}
        stored["input"] = copy.deepcopy(history)
        response = dict(id="resp-%d" % index, status="completed",
                        output=[dict(content=[dict(type="output_text", text="project-test")])],
                        usage=dict(input_tokens=11, output_tokens=7))
        bill = dict(api_key="k", user="u", team_id="t", project_id="p", organization_id="o",
                    model="gpt-test", session_id="codex-test", session_source="parent" if previous else "explicit", prompt_tokens=11, completion_tokens=7,
                    spend=0.000025, metadata=dict(cost_breakdown=dict(source="snapshot")),
                    response=response, proxy_server_request=dict(body=stored, headers={
                        "User-Agent": config["user_agent"], "X-Session-Id": "codex-test",
                        "Originator": "codex_cli_rs"}))
        conversation["turns"].append(dict(turn=index, call_id="call-%d" % index,
                                          response_id=response["id"], previous_response_id=previous,
                                          prompt=prompt, request_body=request, prompt_tokens=11,
                                          completion_tokens=7, cost=0.000025))
        bills.append(bill)
        history.append(dict(role="assistant", content="project-test"))
    return dataset, key, conversation, bills


class CodexSessionTests(unittest.TestCase):
    """验证三轮标准会话协议和失败证据；每项使用自动清理的私有目录，无外部调用。"""

    def test_observer_matches_default_tags_and_rejects_ambiguous_deployments(self):
        """目的：验证默认标签兼容历史 null 并保留唯一性；前置三轮账单和观察器证据，覆盖缺省、空值、具名错配及重复部署；临时目录自动清理，无外部请求。"""
        for tag in (None, "", "default", "missing", "other", "duplicate"):
            with self.subTest(tag=tag), tempfile.TemporaryDirectory() as directory:
                dataset, key, _, bills = codex_fixture(directory)
                dep = dict(id="dep-test", public_name="gpt-test", provider="qiniu", model="upstream-test")
                if tag != "missing":
                    dep["route_tag"] = None if tag == "duplicate" else tag
                dataset.state["deployments"] = [dep, dict(dep, id="dep-duplicate")] if tag == "duplicate" else [dep]
                dataset.observer = mock.Mock(rows=[])
                observed = []
                for bill in bills:
                    messages = [dict(role="system", content=dataset.data["agent_conversation"]["instructions"])]
                    messages += copy.deepcopy(bill["proxy_server_request"]["body"]["input"])
                    observed.append([dict(status=200, forwarded=True, provider="qiniu", model="upstream-test",
                                          route_tag="default", messages=json.dumps(messages),
                                          usage=dict(prompt_tokens=11, completion_tokens=7))])
                replies = [(bill["response"], {"X-Session-Id": "codex-test", "x-litellm-call-id": "call-%d" % index})
                           for index, bill in enumerate(bills, 1)]
                with mock.patch("acceptance.chat.acceptance_run_id", side_effect=["test", "test"]), \
                        mock.patch.object(dataset, "api", side_effect=replies), \
                        mock.patch.object(dataset, "wait_bill", side_effect=bills), \
                        mock.patch.object(dataset, "observed_since", side_effect=observed):
                    if tag in ("other", "duplicate"):
                        with self.assertRaisesRegex(AssertionError, "匹配数量=" + ("0" if tag == "other" else "2")):
                            dataset.codex_conversation(key)
                        self.assertFalse(dataset.report.get("agent_conversations"))
                    else:
                        saved = dataset.codex_conversation(key)
                        self.assertEqual([turn["deployment_id"] for turn in saved["turns"]], ["dep-test"] * 3)

    def test_browser_preflight_revalidates_exact_saved_sessions_without_model_calls(self):
        """目的：浏览器只读核对其选中的会话；前置完整账单，验证正常、空来源、缺会话和缺身份，目录自动清理。"""
        for mode in ("valid", "empty_source", "missing_session", "missing_key"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                dataset, key, conversation, bills = codex_fixture(directory)
                dataset.data["verification_order"] = ["gpt-test", "glm-test"]
                dataset.state["keys"] = [] if mode == "missing_key" else [dict(key, token_id="k")]
                glm = copy.deepcopy(conversation)
                glm["model"] = "glm-test"
                dataset.report["agent_conversations"] = [] if mode == "missing_session" else [conversation, glm]
                glm_bills = copy.deepcopy(bills)
                for bill in glm_bills:
                    bill["model"] = "glm-test"
                    bill["proxy_server_request"]["body"]["model"] = "glm-test"
                for turn in glm["turns"]:
                    turn["request_body"]["model"] = "glm-test"
                if mode == "empty_source":
                    bills[0]["session_source"] = ""
                with mock.patch.object(dataset, "api", side_effect=[(bill, {}, 200) for bill in bills + glm_bills]) as api:
                    if mode == "valid":
                        dataset.validate_browser_codex_sessions()
                        self.assertEqual(api.call_count, 6)
                    else:
                        with self.assertRaisesRegex(AssertionError, "make e2e gpt-1 gpt-2 gpt-3 gpt-4 glm browser"):
                            dataset.validate_browser_codex_sessions()
                    self.assertTrue(all(call.args[0].startswith("/spend/logs/ui/") for call in api.call_args_list),
                                    "browser 前置检查不得隐式发送付费模型请求")

    def test_three_turns_send_standard_header_and_revalidate(self):
        """目的：三轮均发送标准头并可复验；前置完整响应账单，验证增量请求、稳定 ID 和检查点，临时目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset, key, conversation, bills = codex_fixture(directory)
            replies = [(bill["response"], {"X-Session-Id": "codex-test",
                        "x-litellm-call-id": "call-%d" % index})
                       for index, bill in enumerate(bills, 1)]
            with mock.patch("acceptance.chat.acceptance_run_id", side_effect=["test", "test"]), \
                    mock.patch.object(dataset, "api", side_effect=replies) as api, \
                    mock.patch.object(dataset, "wait_bill", side_effect=bills):
                saved = dataset.codex_conversation(key)
            self.assertEqual(saved["turns"], conversation["turns"])
            for call, turn in zip(api.call_args_list, conversation["turns"]):
                self.assertEqual(call.args[1], turn["request_body"])
                self.assertEqual(call.kwargs["request_headers"]["X-Session-Id"], "codex-test")
                self.assertNotIn("Session_id", call.kwargs["request_headers"])
            with mock.patch.object(dataset, "api", side_effect=[(bill, {}, 200) for bill in bills]):
                self.assertEqual(dataset.codex_conversation(key, revalidate=True), saved)

    def test_persisted_history_accepts_reasoning_items_and_rejects_broken_turns(self):
        """目的：Responses 跨协议续接允许助手公开推理项但仍严格核对完整轮次；前置第二轮合法账单，验证 reasoning 边界、缺助手和额外用户项，均为内存副本无需清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset, key, conversation, bills = codex_fixture(directory)
            turn = conversation["turns"][1]
            reasoning = {"type": "reasoning", "summary": [{"type": "summary_text", "text": "Recall the marker."}]}
            bills[1]["proxy_server_request"]["body"]["input"].insert(1, reasoning)
            dataset.check_codex_turn(key, conversation, turn, bills[1])

            missing_assistant = copy.deepcopy(bills[1])
            missing_assistant["proxy_server_request"]["body"]["input"] = [
                conversation["turns"][0]["request_body"]["input"][0],
                turn["request_body"]["input"][0],
            ]
            with self.assertRaisesRegex(AssertionError, "丢失前轮上下文"):
                dataset.check_codex_turn(key, conversation, turn, missing_assistant)

            extra_user = copy.deepcopy(bills[1])
            extra_user["proxy_server_request"]["body"]["input"].insert(2, {"role": "user", "content": "unexpected"})
            with self.assertRaisesRegex(AssertionError, "丢失前轮上下文"):
                dataset.check_codex_turn(key, conversation, turn, extra_user)

    def test_missing_or_wrong_response_session_header_rejects_checkpoint(self):
        """目的：缺失或错误响应头不能算成功；前置 HTTP 成功响应，验证核账前失败且无检查点，临时目录自动清理。"""
        for actual in (None, "prompt:v1:wrong"):
            with self.subTest(actual=actual), tempfile.TemporaryDirectory() as directory:
                dataset, key, _, bills = codex_fixture(directory)
                headers = {"x-litellm-call-id": "call-1"}
                if actual is not None:
                    headers["X-Session-Id"] = actual
                with mock.patch.object(dataset, "api", return_value=(bills[0]["response"], headers)), \
                        mock.patch.object(dataset, "wait_bill") as wait:
                    with self.assertRaisesRegex(AssertionError, "第 1 轮响应会话头错误：call_id=call-1"):
                        dataset.codex_conversation(key)
                wait.assert_not_called()
                self.assertFalse(dataset.report.get("agent_conversations"))

    def test_bill_identity_diagnostics_and_legacy_header_are_rejected(self):
        """目的：分别定位会话、模型和旧头错误；前置正常首轮账单后逐项破坏，验证执行拒绝及复验补跑，目录自动清理。"""
        with tempfile.TemporaryDirectory() as directory:
            dataset, key, conversation, bills = codex_fixture(directory)
            turn = conversation["turns"][0]
            dataset.check_codex_turn(key, conversation, turn, bills[0])
            for field in ("session_id", "model", "session_source"):
                for actual in (None, "wrong"):
                    with self.subTest(field=field, actual=actual):
                        changed = copy.deepcopy(bills)
                        changed[0][field] = actual
                        with self.assertRaisesRegex(AssertionError, "call_id=call-1，字段=" + field):
                            dataset.check_codex_turn(key, conversation, turn, changed[0])
                        dataset.report["agent_conversations"] = [conversation]
                        with mock.patch.object(dataset, "api", return_value=(changed[0], {}, 200)):
                            self.assertIsNone(dataset.codex_conversation(key, revalidate=True))
            for name in ("x-session-id", "X-SESSION-ID"):
                valid = copy.deepcopy(bills[0])
                valid["proxy_server_request"]["headers"] = {
                    "user-agent": dataset.data["agent_conversation"]["user_agent"],
                    name: "codex-test", "originator": "codex_cli_rs"}
                dataset.check_codex_turn(key, conversation, turn, valid)
            legacy = copy.deepcopy(bills[0])
            headers = legacy["proxy_server_request"]["headers"]
            headers["Session_id"] = headers.pop("X-Session-Id")
            with self.assertRaisesRegex(AssertionError, "客户端身份或稳定会话头缺失"):
                dataset.check_codex_turn(key, conversation, turn, legacy)
