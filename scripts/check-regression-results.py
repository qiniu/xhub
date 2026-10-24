#!/usr/bin/env python3
"""消费 go test -json 证据：保留可读日志，拒绝空跑、全跳过及未完成的回归。"""
import argparse
import json
from pathlib import Path
import re
import sys


def selection_depth(pattern):
    """按 Go testing 的顶层斜杠规则计算筛选层数；参数为 -run，返回至少 1，无副作用。"""
    depth, brackets, parens, escaped = 1, False, 0, False
    for char in pattern:
        if escaped:
            escaped = False
        elif char == "\\":
            escaped = True
        elif char == "[":
            brackets = True
        elif char == "]":
            brackets = False
        elif not brackets:
            if char == "(":
                parens += 1
            elif char == ")":
                parens -= 1
            elif char == "/" and parens == 0:
                depth += 1
    return depth


def check_results(stream, log, pattern="", verbose=False):
    """读取 JSON 行流并写完整人类日志；返回门禁摘要，供脚本/单元测试调用。

    只信任结构化 run/pass/fail/skip 事件，日志中的 PASS 文字不能充当证据。
    父用例不重复算通过，且必须有符合筛选层级的已通过叶用例；跳过、截断和损坏均如实记录。
    """
    tests, packages, errors = {}, {}, []
    for line in stream:
        try:
            event = json.loads(line)
            if not isinstance(event, dict) or not isinstance(event.get("Action"), str):
                raise ValueError("invalid Go event")
        except (ValueError, TypeError):
            log.write(line)
            if "invalid event stream" not in errors:
                errors.append("invalid event stream")
            continue
        output = event.get("Output", "")
        if isinstance(output, str):
            log.write(output)
            if verbose:
                for text in output.splitlines():
                    if re.match(r"^=== RUN |^\s*--- (PASS|FAIL|SKIP):|^(PASS|FAIL|ok\s)", text):
                        print(text[:400], flush=True)
        action, package, name = event["Action"], event.get("Package"), event.get("Test")
        if not package:
            continue
        if name:
            key = (package, name)
            if action == "run":
                if key in tests:
                    errors.append("duplicate test execution")
                tests[key] = "running"
            elif action in ("pass", "fail", "skip"):
                if tests.get(key) != "running":
                    errors.append("test ended without matching run")
                tests[key] = action
        elif action in ("start", "pass", "fail", "skip"):
            packages[package] = action
    # 有任何后代事件即视为父节点；避免全跳过子例借父例 PASS 冒充成功。
    parents = {(pkg, "/".join(name.split("/")[:index]))
               for pkg, name in tests for index in range(1, len(name.split("/")))}
    required_depth = selection_depth(pattern)
    leaves = [state for (pkg, name), state in tests.items()
              if (pkg, name) not in parents and len(name.split("/")) >= required_depth]
    passed = leaves.count("pass")
    if not packages or any(state != "pass" for state in packages.values()):
        errors.append("package did not complete successfully")
    if any(state in ("fail", "running") for state in tests.values()):
        errors.append("failed or unfinished tests")
    if not passed:
        errors.append("no passed leaf tests matched the selection (skips do not count)")
    return {"status": "failed" if errors else "passed", "passed_leaf_tests": passed,
            "skipped_tests": sum(state == "skip" for state in tests.values()),
            "failed_tests": sum(state == "fail" for state in tests.values()),
            "selection": pattern, "errors": list(dict.fromkeys(errors))}


def main():
    """脚本入口读取 stdin；参数指定日志/摘要路径，证据写入或门禁失败返回非零，不删除文件。"""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--log", required=True)
    parser.add_argument("--summary", required=True)
    parser.add_argument("--pattern", default="")
    parser.add_argument("--verbose", action="store_true")
    args = parser.parse_args()
    with Path(args.log).open("w") as log:
        summary = check_results(sys.stdin, log, args.pattern, args.verbose)
    Path(args.summary).write_text(json.dumps(summary, indent=2) + "\n")
    print(f"regression evidence: {summary['passed_leaf_tests']} passed leaves, {summary['skipped_tests']} skipped")
    for error in summary["errors"]:
        print("regression gate: " + error, file=sys.stderr)
    return int(summary["status"] != "passed")


if __name__ == "__main__":
    sys.exit(main())
