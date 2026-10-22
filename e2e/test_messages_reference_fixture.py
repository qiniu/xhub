"""工具发现严格夹具的确定性测试；不使用真实供应商或用户数据。"""
import copy
import unittest
from unittest.mock import Mock
from e2e.messages_reference_fixture import reference_messages, reference_payload, serve_reference_history


def request_for(history=False):
    """按历史开关构造有效请求；返回独立字典供篡改测试，测试结束自动回收内存。"""
    return {"model": "e2e-claude-reference", "max_tokens": 64, "messages": reference_messages(history),
            "output_config": {"effort": "high", "future_option": True},
            "tools": [{"name": "ToolSearch", "input_schema": {"type": "object"}}, {"name": "lookup", "input_schema": {"type": "object"}, "defer_loading": True}]}


class ReferenceFixtureTest(unittest.TestCase):
    """覆盖新旧历史、终态、字段丢失和 HTTP 分派边界；无持久资源。"""

    def test_new_and_old_history(self):
        """新旧请求均生成完整回复及用量，且不修改输入；数据均在内存，无需清理。"""
        for history in (False, True):
            body = request_for(history)
            original = copy.deepcopy(body)
            result, events = reference_payload(body)
            self.assertEqual(body, original)
            self.assertEqual(result["usage"], {"input_tokens": 8, "output_tokens": 2})
            self.assertEqual(events[-1]["type"], "message_stop")
            self.assertEqual(events[2]["delta"]["text"], "REFERENCE_OK")

    def test_lost_fields_and_invalid_history(self):
        """丢配置、删引用、改顺序或改用户原文必须失败，防止宽松夹具假通过；无资源清理。"""
        cases = [{**request_for(), field: value} for field, value in [
            ("output_config", None), ("tools", []), ("max_tokens", 0), ("messages", [])]]
        for change in ("remove", "reverse", "text"):
            body = request_for(True)
            content = body["messages"][2]["content"][0]["content"]
            if change == "remove": content.pop(0)
            elif change == "reverse": content.reverse()
            else: content[1]["text"] = "translated"
            cases.append(body)
        for body in cases:
            with self.subTest(body=body), self.assertRaises(ValueError):
                reference_payload(body)

    def test_dispatch(self):
        """同步/流式、无关模型、认证失败、路径和正文错误均可观察；Mock仅验证夹具响应写入，无外部副作用。"""
        self.assertFalse(serve_reference_history(None, {"model": "other"}))
        for stream in (False, True):
            handler = Mock(headers={"x-api-key": "sk-fake"}, path="/v1/messages")
            self.assertTrue(serve_reference_history(handler, {**request_for(True), "stream": stream}))
            (handler._sse if stream else handler._json).assert_called_once()
            handler.send_error.assert_not_called()
        for headers, path, updates, status in [({}, "/v1/messages", {}, 401),
                ({"x-api-key": "sk-fake"}, "/v1/responses", {}, 400),
                ({"Authorization": "Bearer sk-fake"}, "/v1/messages", {"tools": None}, 400)]:
            handler = Mock(headers=headers, path=path)
            serve_reference_history(handler, {**request_for(), **updates})
            self.assertEqual(handler.send_error.call_args.args[0], status)
            handler._json.assert_not_called()
            handler._sse.assert_not_called()
