"""真实验收的 limits 模块；共享上下文由 Dataset 静态组合，资源与失败证据归本轮所有。"""
from contextlib import ExitStack, contextmanager
import secrets



class LimitsCases:
    """用途：封装 limits 场景；通过 self 访问本轮状态、管理 API 和证据，供 Dataset 调用，不自行启动全套测试。"""

    @contextmanager
    def temporary_limit_subject(self, key, label):
        """用途：为限额验收建立未消费的独立归属链；参数为基线密钥与场景名，产出临时个人密钥及五级 ID；供预算/限流验收调用。复用基线实际生效路由与模型，所有额度先共享；每创建一项立即注册逆序删除，创建或验证失败也清理，不改变保留租户配置。"""
        binding, _ = self.api("/route_template/binding?scope=key&scope_id=" + key["token_id"])
        effective = binding.get("effective")
        if not isinstance(effective, dict) or not effective.get("scope_type"):
            raise AssertionError("临时限额验收缺少基线生效路由")
        label = "limits-" + label + "-" + self.run
        shared = {"max_budget": None, "rpm_limit": None, "tpm_limit": None}
        with ExitStack() as cleanup:
            org, _ = self.api("/organization/new", {"organization_alias": label, **shared})
            oid = org["organization_id"]
            cleanup.callback(self.api, "/organization/delete", {"organization_id": oid}, method="DELETE")
            user, _ = self.api("/user/new", {"user_email": label + "@example.com",
                "user_alias": label, "password": secrets.token_urlsafe(24), "user_role": "user",
                **shared})
            uid = user["user_id"]
            cleanup.callback(self.api, "/user/delete", {"user_id": uid})
            # 显式指定临时用户管理团队，防止无团队的平台管理员被自动入队，
            # 把其已有个人 Key 分配和消费带入本应全新的预算链。
            team, _ = self.api("/team/new", {"organization_id": oid, "team_alias": label,
                "admin_user_id": uid, **shared})
            tid = team["team_id"]
            cleanup.callback(self.api, "/team/delete", {"team_id": tid})
            project, _ = self.api("/project/new", {"team_id": tid, "project_alias": label,
                                                  "max_budget": None})
            pid = project["project_id"]
            cleanup.callback(self.api, "/project/delete", {"project_id": pid})
            generated, _ = self.api("/key/generate", {"owner_type": "personal", "user_id": uid,
                "team_id": tid, "project_id": pid, "key_alias": label,
                "models": [key["call_model"]], "route_template_id": effective.get("template_id"), **shared})
            cleanup.callback(self.api, "/key/delete", {"keys": [generated["token_id"]]})
            # 不复制基线归属 ID；否则成功请求虽用临时钥匙，账单却会与原租户错误比较。
            yield {**generated, "organization_id": oid, "team_id": tid, "user_id": uid,
                   "project_id": pid, "call_model": key["call_model"], "profile": "limits-temporary"}

    def verify_allocated_limits(self, key):
        """用途：验证五级预算及组织/团队/个人/密钥 RPM、TPM 拦截与恢复；参数为基线密钥，返回无；每项使用全新共享归属链，避免已有消费和下级保留额度阻止设零。拒绝必须无外发，恢复必须成功响应并核对五级账单，临时资源由上下文清理；任一步失败不记录通过。"""
        for scenario in self.data["limit_scenarios"]["budgets"]:
            scope = scenario["scope"]
            if self.checked("budget-" + scope + "-no-egress"):
                continue
            self.progress(f"    [预算] scope={scope} 临时归属链验证设零拒绝与恢复调用")
            route, field, method = scenario["route"], scenario["field"], scenario["method"]
            with self.temporary_limit_subject(key, "budget-" + scope) as subject:
                identity = subject[scope + "_id"] if scope != "key" else subject["token_id"]
                self.api(route, {field: identity, "max_budget": 0}, method=method)
                self.refuse(subject, "budget-" + scope + self.run, scenario["expected_http_status"], "budget")
                self.api(route, {field: identity, "max_budget": self.data["budgets"][scope]}, method=method)
                self.call(subject, "budget-" + scope + "-restored-" + self.run)
            self.report["checks"].append({"name": "budget-" + scope + "-no-egress", "passed": True})
            self.action_ok(key["call_model"], "identity-budget", scope + " 预算链",
                           "预算设为零后 HTTP=403 且上游增量=0；恢复后模型响应、usage、五级账单及资源清理均通过", record=False)
        for scenario in self.data["limit_scenarios"]["budgets"]:
            scope = scenario["scope"]
            if scope == "project":
                continue
            for rate in self.data["limit_scenarios"]["rate_limits"]:
                field = rate["field"]
                check = ("" if scope == "key" else scope + "-") + field + "-reject-and-real-recovery"
                if self.checked(check):
                    continue
                self.progress(f"    [限流] {scope}/{field} 临时归属链验证拒绝与恢复调用")
                with self.temporary_limit_subject(key, scope + "-" + field) as subject:
                    identity = subject[scope + "_id"] if scope != "key" else subject["token_id"]
                    self.api(scenario["route"], {scenario["field"]: identity, field: rate["blocked_value"]},
                             method=scenario["method"])
                    self.refuse(subject, scope + "-" + field + self.run, 429, field)
                    self.api(scenario["route"], {scenario["field"]: identity, field: rate["restored_value"]},
                             method=scenario["method"])
                    self.call(subject, scope + "-" + field + "-restored-" + self.run)
                self.report["checks"].append({"name": check, "passed": True})
                self.action_ok(key["call_model"], "identity-rate-limit", scope + "/" + field + " 限流链",
                               "限制为零时本地拒绝且无外发；提高限额后模型响应、usage、五级账单及资源清理均通过", record=False)

    def verify_limits(self, key):
        """用途：串联预算/限流、模型白名单与成员密钥生命周期；参数为已成功计费的基线密钥，返回无；独立限额夹具不修改保留数据，生命周期临时密钥 finally 删除，任一断言失败即停止。"""
        if getattr(self, "selected_categories", None) is not None:
            # 显式重跑 limits 必须产生本轮证据；保留其它分类的历史数据，不把历史通过当作本轮通过。
            self.report["checks"] = [row for row in self.report["checks"]
                if not (row["name"].startswith("budget-") or
                        row["name"].endswith("-reject-and-real-recovery") or
                        row["name"] in ("model-allowlist-no-egress", "member-key-generate-block-rotate-real-calls"))]
        self.verify_allocated_limits(key)
        # 基线81把钥匙保持不变，生命周期使用成员自己签发的临时密钥。
        if not self.checked("model-allowlist-no-egress"):
            self.progress("    [模型白名单] 验证非白名单英文模型 ID 在本地拒绝且无上游外发")
            scenario = self.data["limit_scenarios"]["model_allowlist"]
            self.refuse(key, "allowlist-" + self.run, scenario["expected_status"], model=scenario["disallowed_model"])
            self.report["checks"].append({"name": "model-allowlist-no-egress", "passed": True})
            self.action_ok(scenario["disallowed_model"], "identity-model-allowlist", "模型白名单拒绝",
                           f"HTTP={scenario['expected_status']}，拒绝发生在外发前，上游增量=0", record=False)
        if self.checked("member-key-generate-block-rotate-real-calls"):
            return
        user = next(u for u in self.state["users"] if u["id"] == key["user_id"])
        self.progress("    [密钥生命周期] 验证成员签发、封禁、解封、轮换、旧密钥失效和删除")
        login, _ = self.api("/v2/login", {"username": user["email"], "password": self.state["member_password"]})
        token, temp = login["key"], None
        try:
            temp, _ = self.api("/key/generate", {"team_id": key["team_id"], "project_id": key["project_id"], "key_alias": "成员生命周期"}, token=token)
            self.call({**key, **temp}, "lifecycle-new-" + self.run)
            self.api("/key/block", {"key": temp["key"]}, token=token)
            self.refuse({**key, **temp}, "lifecycle-block-" + self.run, 401)
            self.api("/key/unblock", {"key": temp["key"]}, token=token)
            old = dict(temp)
            rotated, _ = self.api("/key/regenerate", {"key": temp["key"]}, token=token)
            temp = {**temp, **rotated}
            self.refuse({**key, **old}, "lifecycle-old-" + self.run, 401)
            self.call({**key, **temp}, "lifecycle-rotated-" + self.run)
        finally:
            if temp:
                self.api("/key/delete", {"keys": [temp["key"]]}, token=token)
        self.refuse({**key, **temp}, "lifecycle-deleted-" + self.run, 401)
        self.report["checks"].append({"name": "member-key-generate-block-rotate-real-calls", "passed": True})

