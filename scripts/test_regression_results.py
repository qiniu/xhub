"""Go JSON 证据门禁的快速单元测试；内存事件不代表产品回归已执行。"""
import importlib.util
import io
import json
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("gate", Path(__file__).with_name("check-regression-results.py"))
GATE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GATE)


def event(action, name=None, **extra):
    """构造单个 Go 事件供解析单测；返回 JSON 行，不访问服务、无需清理。"""
    return json.dumps({"Action": action, "Package": "fixture", **({"Test": name} if name else {}), **extra}) + "\n"


def test_events(name="TestParent", status="pass", child=None):
    """构造完成的父/子事件流；参数控制结果，返回文本，只供门禁算法测试。"""
    rows = [event("start"), event("run", name)]
    if child:
        rows += [event("run", name + "/" + child), event(status, name + "/" + child)]
    rows += [event("pass" if child and status == "skip" else status, name), event("pass")]
    return "".join(rows)


class RegressionResultsTests(unittest.TestCase):
    """验证计数、匹配边界、故障和证据完整性；无外部资源。"""

    def check(self, rows, pattern=""):
        """把指定事件送入实际解析器，返回摘要及完整日志；每例使用独立内存。"""
        log = io.StringIO()
        return GATE.check_results(io.StringIO(rows), log, pattern), log.getvalue()

    def test_normal_parent_and_child_count_only_leaf(self):
        """完成顶层/嵌套子例仅算叶节点；未要求外部服务，内存自动回收。"""
        for child in (None, "selected", "selected/nested"):
            with self.subTest(child=child):
                report, _ = self.check(test_events(child=child))
                self.assertEqual(report["status"], "passed")
                self.assertEqual(report["passed_leaf_tests"], 1)

    def test_zero_skips_missing_children_and_failures_rejected(self):
        """空跑、全跳过、子例未匹配、失败/截断事件不得借父 PASS 通过。"""
        for rows, pattern in [("", ""), (event("start") + event("pass"), ""),
                              (test_events(status="skip"), ""), (test_events(status="skip", child="optional"), ""),
                              (test_events(), "TestParent/missing"), (test_events(child="valid"), "TestParent/valid/missing"),
                              (test_events(status="fail"), ""), (event("start") + event("run", "TestX"), ""),
                              (test_events() + event("fail"), ""), (event("pass", "TestX") + event("pass"), "")]:
            with self.subTest(rows=rows, pattern=pattern):
                report, _ = self.check(rows, pattern)
                self.assertEqual(report["status"], "failed", report)

    def test_skipped_optional_not_counted_as_pass(self):
        """已有真实通过叶例时允许显式可选跳过，但通过计数不能包含跳过或父节点。"""
        rows = test_events(child="valid") + event("run", "TestOptional") + event("skip", "TestOptional")
        report, _ = self.check(rows)
        self.assertEqual((report["status"], report["passed_leaf_tests"], report["skipped_tests"]), ("passed", 1, 1))

    def test_forged_pass_text_malformed_and_duplicate_events_fail(self):
        """日志里的 PASS、损坏/非对象 JSON 或重复执行不能构成可信证据；原始日志仍保留。"""
        spoof = event("output", Output="--- PASS: TestForged (0.01s)\n") + event("pass")
        for rows in (spoof, test_events() + "invalid JSON\n", test_events() + "[]\n", test_events() + test_events()):
            with self.subTest(rows=rows):
                report, log = self.check(rows)
                self.assertEqual(report["status"], "failed")
                if "TestForged" in rows:
                    self.assertIn("TestForged", log)

    def test_selection_depth_matches_go_splitting(self):
        """Go 的字符组、括号内斜杠与转义不拆层；空层和普通斜杠维持实际层数。"""
        for pattern, expected in [("", 1), ("Test", 1), ("Test/child", 2), ("Test//grandchild", 3),
                                  ("Test[A/B]", 1), ("Test(A/B)/child", 2), (r"Test\/child", 1),
                                  (r"Test\\/child", 2)]:
            with self.subTest(pattern=pattern):
                self.assertEqual(GATE.selection_depth(pattern), expected)


if __name__ == "__main__":
    unittest.main()
