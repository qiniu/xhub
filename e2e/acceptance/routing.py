"""真实验收的 routing 模块；共享上下文由 Dataset 静态组合，资源与失败证据归本轮所有。"""
import json
import os
import re

from checklist import CaseBlocked
from .runner_runtime import sql

from .support import fallback_acceptance_deployments, fallback_acceptance_route, weighted_acceptance_route, response_cache_hit, cache_bill_check, bill_check


class RoutingCases:
    """用途：封装 routing 场景；通过 self 访问本轮状态、管理 API 和证据，供 Dataset 调用，不自行启动全套测试。"""

    def route_evidence(self, call_id, supplier, upstream=None, cache_hit=False):
        """用途：按请求 ID 核对持久化供应商、型号及缓存事实；参数为请求及预期路由，返回脱敏证据。

        供真实路由验收调用；等待日志落库后只读当前基线 schema，缺失、重复或路由错误抛断言，不修改历史数据。
        """
        if not call_id:
            raise AssertionError("路由验收缺少独立请求 ID")
        self.wait_log(call_id)
        schema_file = self.directory / "schema.txt"
        schema = schema_file.read_text().strip() if schema_file.exists() else "public"
        if not re.fullmatch(r"public|e2e_real_[a-f0-9]{12}", schema):
            raise ValueError("路由证据 schema 非法")
        ident = str(call_id).replace("'", "''")
        rows = json.loads(sql(f"SELECT coalesce(json_agg(json_build_object('request_id',request_id,"
            f"'supplier',supplier,'upstream_model',upstream_model,'cache_hit',cache_hit,"
            f"'outbound_endpoint',outbound_endpoint,'status',status,'http_status',http_status)), '[]'::json) "
            f"FROM {schema}.usage_events WHERE request_id='{ident}';").strip())
        if len(rows) != 1:
            raise AssertionError(f"请求 {call_id} 路由事实必须唯一，实际={len(rows)}")
        row = rows[0]
        if row["supplier"] != supplier or (upstream is not None and row["upstream_model"] != upstream) or row["cache_hit"] != cache_hit:
            raise AssertionError("真实路由与预期不符: " + json.dumps(row, ensure_ascii=False))
        if cache_hit and row["outbound_endpoint"]:
            raise AssertionError("缓存命中不应记录真实外发路径: " + call_id)
        if not cache_hit and not row["outbound_endpoint"]:
            raise AssertionError("未命中缓存的请求缺少真实外发路径: " + call_id)
        self.report.setdefault("route_evidence", []).append(row)
        self.save()
        return row

    def observed_since(self, start, marker):
        """用途：供其他观察器场景读取请求标记记录；参数为起始下标与标记，返回行副本；无观察器返回空列表，不修改资源。"""
        if not self.observer:
            return []
        with self.observer.lock:
            return [dict(row) for row in self.observer.rows[start:] if marker in row.get("messages", "")]

    def verify_cache_weight_chain(self, key):
        """用途：组合验证固定请求缓存、跨密钥隔离、封禁权限和 100:0 到 0:100 权重切换；参数为基线密钥，返回无；创建两部署、一路由和两把临时密钥，finally 全部删除。"""
        model = self.state["chat_models"]["fennoai"]
        protocol = "bypass_openai_chat"
        provider = self.state["selected"]["fennoai"]["provider"]
        definition = next(row for row in self.data["models"] if row["id"] == "fenno-chat")
        weights = self.data["logic_chains"]["weighted_route_switch"]
        timeout = self.data["routing"]["timeout_seconds"]
        deployment_ids, route_id, temporary_keys = [], None, []
        try:
            first_id = self.deployment(definition, provider, temporary=True, public_name=model)
            deployment_ids.append(first_id)
            second_definition = next(row for row in self.data["models"] if row["id"] == "qiniu-chat")
            second_provider = self.state["selected"]["qiniu"]["provider"]
            second_id = self.deployment(second_definition, second_provider, temporary=True, public_name=model)
            deployment_ids.append(second_id)
            excluded_ids = [row["id"] for row in self.state["deployments"]
                            if row["public_name"] == model and row["id"] not in deployment_ids]
            initial = weighted_acceptance_route(model, first_id, second_id,
                                                weights["initial_weights"], timeout, excluded_ids)
            route, _ = self.api("/route_template/new", {
                "name": "real-acceptance-cache-weight-" + self.run, "body": initial})
            route_id = route["id"]
            for suffix in ("primary", "isolation"):
                created, _ = self.api("/key/generate", {"key_alias": "real-cache-weight-" + suffix,
                    "owner_type": "personal", "user_id": key["user_id"], "team_id": key["team_id"],
                    "project_id": key["project_id"], "models": [model], "route_template_id": route_id})
                temporary_keys.append({**key, **created, "profile": "logic-chain", "call_model": model})
            primary, isolated = temporary_keys
            marker = "cache-weight-" + self.run
            body = {"model": model, "session_id": marker, "messages": [{"role": "user",
                    "content": "Reply only OK. " + marker}], "max_tokens": 32}

            self.action_start(model, protocol, "缓存首次未命中与权重 100:0",
                              "固定正文首次请求，要求只调用 Fenno 真实上游")
            first, first_headers, _, first_call = self.chat_request(primary, body)
            self.route_evidence(first_call, provider["id"], definition.get("stored_model", definition["upstream_model"]))
            if response_cache_hit(first_headers):
                raise AssertionError("首次请求没有按 100:0 只进入 weight-a 真实部署")
            first_bill = self.wait_bill(first_call)
            first_cost = bill_check(first_bill, primary, self.data["billing"])
            self.action_ok(model, protocol, "缓存首次未命中与权重 100:0",
                           f"cache_hit=false，实测供应商=fennoai，金额={first_cost:.9f}")

            second, second_headers, _, second_call = self.chat_request(primary, body)
            if not response_cache_hit(second_headers):
                raise AssertionError("相同请求没有由缓存回答或仍发生真实上游调用")
            if second != first or second_call == first_call:
                raise AssertionError("缓存响应不一致或缓存请求没有独立 call ID")
            self.route_evidence(second_call, "", "", cache_hit=True)
            second_bill = self.wait_bill(second_call)
            cache_bill_check(second_bill, primary, first["usage"])
            self.action_ok(model, protocol, "相同请求缓存命中与响应一致",
                           "响应 JSON 与首次完全一致，缓存日志已核对，独立请求日志 cache_hit=true，spend=0，usage 一致")

            _, isolated_headers, _, isolated_call = self.chat_request(isolated, body)
            self.route_evidence(isolated_call, provider["id"])
            if response_cache_hit(isolated_headers):
                raise AssertionError("另一把密钥错误复用了第一把密钥的缓存")
            isolated_cost = bill_check(self.wait_bill(isolated_call), isolated, self.data["billing"])
            self.action_ok(model, protocol, "跨密钥缓存隔离",
                           f"第二把密钥 cache_hit=false，实测供应商=fennoai，独立计费={isolated_cost:.9f}")

            self.api("/key/block", {"key": primary["key"]})
            _, blocked_headers, blocked_status, _ = self.chat_request(primary, body, expected=401)
            if response_cache_hit(blocked_headers):
                raise AssertionError("封禁密钥读取了缓存或发生上游调用")
            self.action_ok(model, protocol, "封禁权限先于缓存读取",
                           f"HTTP={blocked_status}，cache_hit=false，封禁请求已被拒绝")
            self.api("/key/unblock", {"key": primary["key"]})

            updated = weighted_acceptance_route(model, first_id, second_id,
                                                weights["updated_weights"], timeout, excluded_ids)
            self.api("/route_template/" + route_id + "/update", {"body": updated})
            switched, switched_headers, _, switched_call = self.chat_request(primary, body)
            self.route_evidence(switched_call, second_provider["id"], second_definition.get("stored_model", second_definition["upstream_model"]))
            if response_cache_hit(switched_headers):
                raise AssertionError("路由更新未使旧缓存失效并切换到 weight-b")
            switched_cost = bill_check(self.wait_bill(switched_call), primary, self.data["billing"])
            self.action_ok(model, protocol, "路由更新使缓存失效并切换权重 0:100",
                           f"cache_hit=false，实测供应商=qiniu，金额={switched_cost:.9f}")

            repeated, repeated_headers, _, repeated_call = self.chat_request(primary, body)
            if repeated != switched or not response_cache_hit(repeated_headers) or repeated_call == switched_call:
                raise AssertionError("切换后相同请求没有命中新路由缓存")
            self.route_evidence(repeated_call, "", "", cache_hit=True)
            cache_bill_check(self.wait_bill(repeated_call), primary, switched["usage"])
            self.action_ok(model, protocol, "新权重缓存命中与零权重排除",
                           "响应与权重切换后的真实回答一致，cache_hit=true，无外发，spend=0")
        finally:
            for subject in temporary_keys:
                self.api("/key/delete", {"keys": [subject["key"]]})
            if route_id:
                self.api("/route_template/" + route_id + "/delete", {})
            for deployment_id in deployment_ids:
                self.api("/model/delete", {"id": deployment_id})
            if deployment_ids:
                self.state["deployments"] = [row for row in self.state["deployments"]
                                               if row["id"] not in deployment_ids]
                self.save()

    def verify_fallback_policy_chain(self, key):
        """用途：真实供应商错误触发跨模型回退并验证逐轮调用、目标权限及禁用回退；参数为基线密钥，无返回。

        隔离模式由观察器注入可控 429；共享模式要求 E2E_FALLBACK_UPSTREAM_MODEL 明确指定会真实返回
        429/5xx 的型号，缺失时在创建资源前记为外部前置条件未满足；显式型号返回其他状态仍失败。
        每次记录实际错误码，finally 清理临时资源。
        """
        model = self.state["chat_models"]["fennoai"]
        definition = next(row for row in self.data["models"] if row["id"] == "fenno-chat")
        isolated_fault = bool(getattr(self, "isolated_upstreams", False) and self.observer)
        upstream = definition["upstream_model"] if isolated_fault else os.environ.get("E2E_FALLBACK_UPSTREAM_MODEL", "").strip()
        if not upstream:
            raise CaseBlocked("共享真实供应商回退需要设置 E2E_FALLBACK_UPSTREAM_MODEL，且该型号须稳定返回 429/5xx；本轮未创建临时资源、未调用供应商")
        provider = self.state["selected"]["fennoai"]["provider"]
        target = next(row for row in self.data["models"] if row["id"] == "qiniu-glm")
        fallback_model = target["public_name"]
        _, fallback_id = fallback_acceptance_deployments(self.state["deployments"],
            self.state["chat_models"]["fennoai"], fallback_model, target["provider"])
        excluded = [r["id"] for r in self.state["deployments"]
                    if r["public_name"] == fallback_model and r["id"] != fallback_id]
        deployment_id, route_id, subjects = None, None, []
        try:
            deployment_id = self.deployment(definition, provider, upstream, public_name=model,
                                            fault=isolated_fault, temporary=True)
            route, _ = self.api("/route_template/new", {"name": "real-fallback-" + self.run, "body": fallback_acceptance_route(
                model, deployment_id, [r["id"] for r in self.state["deployments"] if r["public_name"] == model and r["id"] != deployment_id], fallback_model, fallback_id, self.data["routing"]["timeout_seconds"], excluded)})
            route_id = route["id"]
            for suffix, models in (("allowed", [model, fallback_model]), ("primary-only", [model])):
                created, _ = self.api("/key/generate", {"key_alias": model + suffix, "owner_type": "personal",
                    "user_id": key["user_id"], "team_id": key["team_id"], "project_id": key["project_id"],
                    "models": models, "route_template_id": route_id})
                subjects.append({**key, **created, "profile": "fallback-real", "call_model": model})
            allowed, restricted = subjects
            body = {"model": model, "session_id": "real-fallback-" + self.run, "messages": [{"role": "user", "content": "Reply only OK. " + model}], "max_tokens": 32}
            # 禁止回退的探测先证实真实外发错误，避免本地拒绝被误认为供应商故障。
            error, headers, status, call_id = self.chat_request(allowed, {**body, "disable_fallbacks": True}, expected=(400, 401, 403, 404, 422, 429, 500, 502, 503, 504))
            if response_cache_hit(headers):
                raise AssertionError("禁用回退的失败请求错误命中缓存")
            proof = self.route_evidence(call_id, provider["id"], upstream)
            if proof["status"] == "success":
                raise AssertionError("指定主模型没有产生真实供应商错误")
            log = self.wait_log(call_id)
            diagnostic = log.get("metadata", {}).get("upstream_response")
            if not diagnostic:
                raise AssertionError("真实故障缺少供应商响应诊断")
            self.report.setdefault("fallback_failures", []).append({"call_id": call_id,
                "gateway_status": status, "upstream_response": diagnostic, "requested_upstream": upstream})
            self.save()
            upstream_status = diagnostic.get("status_code")
            if not isinstance(upstream_status, int) or not (upstream_status == 429 or upstream_status >= 500):
                raise AssertionError(f"真实回退前置条件未满足：供应商 HTTP={upstream_status}，通用回退需要 429/5xx；请通过 E2E_FALLBACK_UPSTREAM_MODEL 指定真实故障型号，不能将当前错误记为回退通过")
            self.action_ok(model, "bypass_openai_chat", "真实供应商错误与禁用回退", f"HTTP={status}，供应商={provider['id']}，已核对外发路径；实际错误已记录")
            call_ids = set()
            for index in range(self.data["logic_chains"]["fallback_cache"]["repeat_requests"]):
                # 同会话不同正文，排除响应缓存掩盖后续轮次的真实调用。
                request = {**body, "messages": [{"role": "user", "content": f"Reply only OK. {model} turn {index}"}]}
                answer, headers, _, call_id = self.chat_request(allowed, request)
                if response_cache_hit(headers) or call_id in call_ids:
                    raise AssertionError("回退逐轮请求必须真实外发并有独立 ID")
                call_ids.add(call_id)
                bill = self.wait_bill(call_id)
                bill_check(bill, allowed, self.data["billing"])
                if bill.get("session_id") != body["session_id"] or bill.get("model") != model:
                    raise AssertionError("回退请求丢失明确会话或请求模型")
                self.route_evidence(call_id, target["provider"], target.get("stored_model", target["upstream_model"]))
                self.action_ok(model, "bypass_openai_chat", f"真实回退第 {index + 1} 轮", f"备用供应商={target['provider']}，usage={answer['usage']}，cache_hit=false")
            denied, headers, _, denied_id = self.chat_request(restricted, body, expected=401)
            if response_cache_hit(headers) or "model not in allowed model list" not in json.dumps(denied):
                raise AssertionError("回退目标白名单未返回明确拒绝")
            self.action_ok(fallback_model, "bypass_openai_chat", "回退目标白名单拒绝", "HTTP=401，禁止目标调用")
        finally:
            for subject in subjects:
                self.api("/key/delete", {"keys": [subject["key"]]})
            if route_id:
                self.api("/route_template/" + route_id + "/delete", {})
            if deployment_id:
                self.api("/model/delete", {"id": deployment_id})
                self.state["deployments"] = [r for r in self.state["deployments"] if r["id"] != deployment_id]
                self.save()

    def verify_profile_route_isolation(self):
        """用途：主动调用两种单供应商密钥并核对精确持久化路由；无参数/返回，供真实验收调用。

        每轮重新请求，历史检查点不替代当前证据；账单由 call 核对，路由缺失或不符抛断言，无临时资源。
        """
        for profile, provider in (("fenno-only", "fennoai"), ("qiniu-only", "qiniu")):
            key = next(row for row in self.state["keys"] if row["profile"] == profile)
            marker = f"route-isolation-{profile}-{self.run}"
            self.call(key, marker, model=key["call_model"])
            receipt = next(row for row in self.report["calls"] if row["marker"] == marker)
            self.route_evidence(receipt["call_id"], provider, self.state["selected"][provider]["model"])
            self.action_ok(key["call_model"], "bypass_openai_chat", "真实模型路由隔离",
                           f"profile={profile}，持久化供应商={provider}，响应包含非零 usage")
        self.report["checks"] = [r for r in self.report["checks"] if r.get("name") != "profile-routes-real-models-and-vendors"]
        self.report["checks"].append({"name": "profile-routes-real-models-and-vendors", "passed": True})
        self.save()
