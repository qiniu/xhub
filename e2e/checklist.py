"""保存本轮验收清单；旧检查点、未执行与本轮通过分别展示。"""
from contextlib import contextmanager
import json
from pathlib import Path
import time
import threading


class CaseBlocked(Exception):
    """用途：声明用例因外部前置条件缺失而未执行；参数为可公开原因，无额外返回值。

    仅供真实验收场景在任何资源创建或外部调用前抛出；Checklist 会记录为 blocked 并继续后续用例，
    runner 最终仍返回非零，避免把未验证能力误报为通过。
    """


class Checklist:
    """由真实验收 runner 持有，用例只有完成全部断言后才能通过。"""

    def __init__(self, directory, logger, plan, scenarios=None):
        """用途：初始化本轮清单；参数为目录、日志、ID/名称列表及可选例子/逻辑映射，返回实例。

        runner 提供实际场景说明，保存到报告并在执行时显示；覆盖旧清单，不读取历史通过状态，重复 ID 拒绝。
        """
        if len({ident for ident, _ in plan}) != len(plan):
            raise ValueError("验收用例 ID 重复")
        self.save_lock = threading.RLock()
        self.directory, self.logger = Path(directory), logger
        self.rows = [dict(id=ident, name=name, status="pending") for ident, name in plan]
        for row in self.rows:
            if scenarios and row["id"] in scenarios:
                row["example"], row["logic"] = scenarios[row["id"]]
        self.directory.mkdir(parents=True, exist_ok=True)
        self.save()
        for index, row in enumerate(self.rows, 1):
            logger(f"[待测 {index:03d}/{len(self.rows):03d}] {row['id']} {row['name']}")

    def save(self):
        """用途：保存本轮 JSON 与人工核对表；无参数/返回，供状态变更调用；未执行和运行中不能计为通过，异常向 runner 传播。"""
        with self.save_lock:
            self._save_locked()

    def _save_locked(self):
        """用途：持锁写入清单快照；无参数和返回值，仅由 save 调用，防止并行用例报告相互截断。"""
        counts = {status: sum(row["status"] == status for row in self.rows)
                  for status in ("passed", "failed", "blocked", "pending", "running", "revalidated", "reused")}
        (self.directory / "cases.json").write_text(json.dumps(
            dict(counts=counts, cases=self.rows), ensure_ascii=False, indent=2) + "\n")
        labels = dict(passed="通过", failed="失败", blocked="外部前置条件未满足", pending="未执行", running="执行中",
                      revalidated="检查点已复验", reused="沿用历史证据，本轮未执行")
        lines = ["# 本轮 E2E 核对清单", "", " | ".join(f"{labels[key]}={value}" for key, value in counts.items()),
                 "", "| 编号 | 用例 | 状态 | 耗时(s) |", "| --- | --- | --- | --- |"]
        for index, row in enumerate(self.rows, 1):
            name = row['name'].replace('|', '\\|').replace('\n', ' ')
            lines.append(f"| {index:03d} {row['id']} | {name} | {labels[row['status']]} | {row.get('seconds', '')} |")
        blocked = [row for row in self.rows if row["status"] == "blocked"]
        if blocked:
            lines.extend(("", "## 外部前置条件未满足", ""))
            for row in blocked:
                reason = row.get("reason", "未提供原因").replace("\n", " ")
                lines.append(f"- {row['id']}：{reason}")
        for row in self.rows:
            if "example" in row:
                lines.extend(("", "## " + row["id"] + "：" + row["name"], "",
                              "测什么、怎么测：" + row["example"], "", "判断依据：" + row["logic"]))
        (self.directory / "cases.md").write_text("\n".join(lines) + "\n")

    @contextmanager
    def case(self, ident):
        """用途：包围一个真实业务用例；参数为已登记 ID，产出可标注复验/沿用的记录；包括清理在内的全部步骤完成后才输出用例通过，任何异常或中断标失败并原样抛出；失败日志仅展示异常类型，避免泄漏正文和凭据。"""
        row = next(row for row in self.rows if row["id"] == ident)
        if row["status"] != "pending":
            raise ValueError("本轮用例重复执行: " + ident)
        label = f"{self.rows.index(row) + 1:03d}/{len(self.rows):03d} {ident} {row['name']}"
        row["status"] = "running"
        started = time.monotonic()
        self.save()
        self.logger("[开始 " + label + "]")
        if "example" in row:
            self.logger("    [测什么、怎么测] " + row["example"])
            self.logger("    [判断依据] " + row["logic"])
        try:
            yield row
        except CaseBlocked as error:
            row.update(status="blocked", reason=str(error))
            self.logger("ℹ️ 用例结果：[阻塞 " + label + "] 外部前置条件未满足：" + str(error))
        except BaseException as error:
            row.update(status="failed", error_type=type(error).__name__)
            self.logger("❌ 用例失败：[失败 " + label + "] 异常类型=" + type(error).__name__ + "；具体失败步骤请查看上方接口及动作日志")
            raise
        else:
            if row["status"] == "running":
                row["status"] = "passed"
            names = dict(passed="通过", blocked="外部前置条件未满足",
                         revalidated="检查点已复验", reused="沿用历史证据，本轮未执行")
            # 复验和历史沿用不能显示本轮完整用例通过，保留各自的明确语义。
            prefix = "✅ 用例通过：" if row["status"] == "passed" else "ℹ️ 用例结果："
            self.logger(prefix + "[结果 " + label + "] " + names[row["status"]])
        finally:
            row["seconds"] = round(time.monotonic() - started, 3)
            self.save()

    def summary(self):
        """用途：输出当前真实状态汇总和报告路径；无参数/返回，由 runner 在清理阶段调用；失败时也保留待测项，不序列化业务凭据。"""
        self.save()
        counts = json.loads((self.directory / "cases.json").read_text())["counts"]
        names = dict(passed="通过", failed="失败", blocked="外部前置条件未满足", pending="未执行",
                     running="执行中", revalidated="检查点已复验", reused="沿用历史")
        self.logger("[用例汇总] " + "，".join(f"{names[key]}={value}" for key, value in counts.items()))
        self.logger("[核对报告] " + str(self.directory / "cases.md"))
