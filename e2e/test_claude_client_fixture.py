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
