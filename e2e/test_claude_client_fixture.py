"""Claude 浏览器上游契约的快速单元测试，不启动网络或数据库。"""
import unittest
from e2e.claude_client_fixture import claude_client_payload, serve_claude_client


class ClaudeClientFixtureTest(unittest.TestCase):
    """覆盖纯函数的正常、边界、失败分支；所有对象仅在内存，无需清理。"""

    def test_full_lifecycle(self):
        """合法输出上限生成完整块及终态，usage与正文一致。"""
        payload, events = claude_client_payload({"max_tokens": 64})
        self.assertEqual(payload["usage"], {"input_tokens": 8, "output_tokens": 2})
        self.assertEqual(events[-3]["type"], "content_block_stop")
        self.assertEqual(events[-1]["type"], "message_stop")

    def test_invalid_requests(self):
        """缺cap、非法编辑及未转换参数必须明确失败。"""
        for body in ({}, {"max_tokens": 0}, {"max_tokens": 64, "context_management": {}},
                     {"max_tokens": 64, "parallel_tool_calls": True},
                     {"max_tokens": 64, "tools": [{"strict": False}]}):
            with self.subTest(body=body), self.assertRaises(ValueError):
                claude_client_payload(body)

    def test_keep_all(self):
        """已声明keep-all完整保留且能得到文本终态。"""
        payload, _ = claude_client_payload({"max_tokens": 64, "context_management": {"edits": [{"type": "clear_thinking_20251015", "keep": "all"}]}})
        self.assertEqual(payload["stop_reason"], "end_turn")

    def test_unrelated_model(self):
        """非专属模型不读取handler或修改HTTP响应。"""
        self.assertFalse(serve_claude_client(None, {"model": "other"}))

    def test_automatic_cache_buckets(self):
        """无缓存声明时夹具报告5m与1h合计5个token，供真实计费端到端核对。"""
        payload, events = claude_client_payload({"max_tokens": 64, "messages": [{"role": "user", "content": "Verify automatic cache."}]})
        self.assertEqual(payload["usage"]["cache_creation_input_tokens"], 5)
        self.assertEqual(events[0]["message"]["usage"]["cache_creation"]["ephemeral_1h_input_tokens"], 3)

    def test_explicit_cache_buckets(self):
        """显式5m/1h声明原样到达时均返回混合桶，错误或缺失声明必须拒绝，无外部资源。"""
        body = {"max_tokens": 64, "messages": [{"role": "user", "content": "Verify explicit cache."}]}
        for ttl in ("5m", "1h"):
            payload, _ = claude_client_payload({**body, "cache_control": {"type": "ephemeral", "ttl": ttl}})
            self.assertEqual(payload["usage"]["cache_creation_input_tokens"], 5)
        with self.assertRaises(ValueError):
            claude_client_payload(body)

    def test_bridge_thinking_and_native_preservation(self):
        """跨协议缺省保留上游默认，模拟拒绝disabled的模型；原生keep-all也不能注入thinking，仅操作内存。"""
        body = {"max_tokens": 64, "messages": [{"role": "user", "content": "Verify default bridge."}]}
        claude_client_payload(body)
        for thinking in ({"type": "disabled"}, None, {"type": "adaptive"}):
            with self.subTest(thinking=thinking), self.assertRaises(ValueError):
                claude_client_payload({**body, "thinking": thinking})
        with self.assertRaises(ValueError):
            claude_client_payload({"max_tokens": 64, "thinking": {"type": "disabled"},
                                  "context_management": {"edits": [{"type": "clear_thinking_20251015", "keep": "all"}]}})

    def test_empty_content_refusal(self):
        """拒绝保留空数组、明确原因、真实usage及message_stop，不能变成一般文本成功；无持久副作用。"""
        payload, events = claude_client_payload({"max_tokens": 64, "messages": [{"role": "user", "content": "Verify explicit refusal."}]})
        self.assertEqual(payload["content"], [])
        self.assertEqual(payload["stop_reason"], "refusal")
        self.assertEqual(payload["usage"]["output_tokens"], 0)
        self.assertEqual(events[-1]["type"], "message_stop")
        self.assertEqual(events[-2]["delta"]["stop_details"]["explanation"], "Synthetic upstream refusal.")

    def test_bridged_blocks_and_refusal_history(self):
        """桥接文本块同样触发场景断言，拒绝续轮必须保留assistant原文；无网络或持久资源。"""
        payload, _ = claude_client_payload({"max_tokens": 64, "messages": [
            {"role": "user", "content": [{"type": "text", "text": "Verify explicit refusal."}]}]})
        self.assertEqual(payload["stop_reason"], "refusal")
        with self.assertRaises(ValueError):
            claude_client_payload({"max_tokens": 64, "thinking": {"type": "disabled"}, "messages": [
                {"role": "user", "content": [{"type": "text", "text": "Verify default bridge."}]}]})
        body = {"max_tokens": 64, "messages": [{"role": "assistant", "content": "Synthetic upstream refusal."},
                                             {"role": "user", "content": "Verify refusal continuation."}]}
        claude_client_payload(body)
        with self.assertRaises(ValueError):
            claude_client_payload({**body, "messages": body["messages"][1:]})
