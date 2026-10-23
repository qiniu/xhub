#!/usr/bin/env python3
"""按 docs 数据清单构建真实租户并验收；凭据仅来自环境，证据不含密钥。"""
import json
import math
import os
from pathlib import Path
import re
import secrets
import string
import subprocess
import time
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "docs/testdata/real-acceptance/dataset.json"


def acceptance_run_id(length=12):
    """用途：生成只含小写字母的本轮唯一标识；参数为长度，返回随机字符串；验收标记会进入真实护栏，避免连续数字被手机号规则改写后造成观察器误判。"""
    return "".join(secrets.choice(string.ascii_lowercase) for _ in range(length))


def temporary_retry_delay(summary, default_delay, rate_limit_delay, attempt=1):
    """用途：按真实上游错误选择有限重试间隔；参数为错误摘要、普通间隔、限流间隔和当前次数，返回等待秒数；429 使用完整冷却，连接类 5xx 指数退避并以同一上限封顶。"""
    if re.search(r"(?:^|\D)429(?:\D|$)", summary):
        return rate_limit_delay
    return min(default_delay * (2 ** max(attempt - 1, 0)), rate_limit_delay)


def chat_probe_result(status, reply):
    """用途：为供应商探测生成不含正文的判定证据；参数为 HTTP 状态和任意 JSON 响应，返回通过标记及结构诊断；供候选选择调用，只有非空文本回答和有限正数计量可通过，异常结构按失败返回且无副作用。"""
    reply = reply if isinstance(reply, dict) else {}
    choices = reply.get("choices")
    choice = choices[0] if isinstance(choices, list) and choices and isinstance(choices[0], dict) else {}
    message = choice.get("message")
    message = message if isinstance(message, dict) else {}
    usage = reply.get("usage")
    usage = usage if isinstance(usage, dict) else {}
    content, reasoning = message.get("content"), message.get("reasoning_content")
    finish = choice.get("finish_reason")
    # 仅保留协议枚举和长度，不能把供应商正文、错误文本或凭据写入探测报告。
    finish = finish if isinstance(finish, str) and finish in ("stop", "length", "tool_calls", "content_filter", "function_call") else "unknown"
    prompt, completion = usage.get("prompt_tokens", 0), usage.get("completion_tokens", 0)
    valid_usage = all(type(value) in (int, float) and value > 0 and math.isfinite(value)
                      for value in (prompt, completion))
    content_chars = len(content.strip()) if isinstance(content, str) else 0
    return {
        "passed": status == 200 and content_chars > 0 and valid_usage and not reply.get("error"),
        "prompt_tokens": prompt if type(prompt) in (int, float) and math.isfinite(prompt) else 0,
        "completion_tokens": completion if type(completion) in (int, float) and math.isfinite(completion) else 0,
        "finish_reason": finish, "content_chars": content_chars,
        "reasoning_chars": len(reasoning.strip()) if isinstance(reasoning, str) else 0,
        "response_error": bool(reply.get("error")),
    }


def fallback_acceptance_deployments(deployments, model, fallback_model, fallback_provider):
    """用途：从基线部署选择跨模型验收目标；参数为部署列表、主备公开模型和备用供应商，返回主模型全部健康部署 ID 与备用 ID；供回退验收调用，同名模型、缺失或含糊目标抛 ValueError，不修改数据。"""
    if model == fallback_model:
        raise ValueError("跨模型回退要求不同的公开模型")
    baseline = [row for row in deployments if not row.get("temporary") and not row.get("temporary_fault")]
    healthy_ids = [row["id"] for row in baseline if row["public_name"] == model]
    targets = [row["id"] for row in baseline
               if row["public_name"] == fallback_model and row["provider"] == fallback_provider]
    if not healthy_ids or len(targets) != 1:
        raise ValueError("回退验收缺少主模型部署或唯一指定供应商备用部署")
    return healthy_ids, targets[0]


def fallback_acceptance_route(model, fault_id, healthy_ids, fallback_model, fallback_id, timeout_seconds, excluded_fallback_ids=()):
    """用途：构造临时密钥的确定性跨模型回退模板；参数为主备模型、故障和健康部署、备用部署、超时及其他备用部署，返回完整正文。

    供真实验收及浏览器调用；主模型健康部署及其他备用部署显式置零，同名模型拒绝。专用类别按源禁用，避免空表继承默认导致意外选路；不修改部署数据。
    """
    if model == fallback_model:
        raise ValueError("跨模型回退要求不同的公开模型")
    healthy_ids = [healthy_ids] if isinstance(healthy_ids, str) else list(healthy_ids)
    return {
        "model_routes": [
            {"model": model, "strategy": "traffic-split", "allocations": [
                {"deployment_id": fault_id, "weight": 100},
                *[{"deployment_id": ident, "weight": 0} for ident in healthy_ids],
            ]},
            {"model": fallback_model, "strategy": "traffic-split", "allocations": [
                {"deployment_id": fallback_id, "weight": 100},
                *[{"deployment_id": ident, "weight": 0} for ident in excluded_fallback_ids],
            ]},
        ],
        "retry_policy": {"max_attempts": 1, "timeout_seconds": timeout_seconds,
                         "failure_threshold": 0, "cooldown_seconds": 60},
        "fallbacks": [{model: [fallback_model]}, {fallback_model: []}],
        "context_window_fallbacks": [{model: []}, {fallback_model: []}],
        "content_policy_fallbacks": [{model: []}, {fallback_model: []}],
    }


def weighted_acceptance_route(model, first_id, second_id, weights, timeout_seconds, excluded_ids=()):
    """用途：构造同模型两部署的确定性权重模板；参数为模型、两个部署、两项权重、超时及其他部署 ID，返回完整正文。

    供缓存权重验收调用；其他部署必须显式置零，因为未列出部署仍有权重 1。三类回退按源禁用；重复身份或非法权重抛 ValueError，无外部副作用。
    """
    ids = [first_id, second_id, *excluded_ids]
    if any(not ident for ident in ids) or len(set(ids)) != len(ids):
        raise ValueError("权重验收部署 ID 必须非空且唯一")
    if len(weights) != 2 or any(type(value) not in (int, float) or not math.isfinite(value) or value < 0 for value in weights) or not any(weights):
        raise ValueError("权重验收必须提供两项有限非负权重且至少一项为正")
    return {
        "model_routes": [{
            "model": model,
            "strategy": "traffic-split",
            "allocations": [
                {"deployment_id": first_id, "weight": weights[0]},
                {"deployment_id": second_id, "weight": weights[1]},
                *[{"deployment_id": ident, "weight": 0} for ident in ids[2:]],
            ],
        }],
        "retry_policy": {"max_attempts": 1, "timeout_seconds": timeout_seconds,
                         "failure_threshold": 0, "cooldown_seconds": 60},
        "fallbacks": [{model: []}],
        "context_window_fallbacks": [{model: []}],
        "content_policy_fallbacks": [{model: []}],
    }


def header_value(headers, name):
    """用途：按 HTTP 大小写不敏感规则读取响应头；参数为头字典和名称，返回字符串或空值；供缓存与请求 ID 验收复用。"""
    return next((value for key, value in headers.items() if key.lower() == name.lower()), None)


def response_cache_hit(headers):
    """用途：识别网关两种兼容缓存命中头；参数为响应头，返回布尔值；只接受显式 true，不把缺失或其他值视为命中。"""
    return any(str(header_value(headers, name) or "").lower() == "true"
               for name in ("cache_hit", "x-litellm-cache-hit"))


def cache_bill_check(bill, key, expected_usage):
    """用途：核对缓存命中的独立零费用日志；参数为账单、密钥和首次响应 usage，返回无；验证五级归属、usage 一致、命中标记、零费用及价格明细。"""
    for field, expected in (("api_key", key["token_id"]), ("user", key["user_id"]),
                            ("team_id", key["team_id"]), ("project_id", key["project_id"]),
                            ("organization_id", key["organization_id"])):
        if bill.get(field) != expected:
            raise AssertionError("缓存账单归属错误: " + field)
    if any(int(bill.get(field, -1)) != int(expected_usage.get(field, -2))
           for field in ("prompt_tokens", "completion_tokens")):
        raise AssertionError("缓存命中 usage 与首次真实响应不一致")
    if not bill.get("cache_hit"):
        raise AssertionError("缓存命中日志缺少 cache_hit=true")
    if not math.isclose(float(bill.get("spend", -1)), 0, rel_tol=0, abs_tol=1e-12):
        raise AssertionError("缓存命中仍产生费用")
    if not bill.get("metadata", {}).get("cost_breakdown"):
        raise AssertionError("缓存命中日志缺少价格明细")


def load_manifest(path=MANIFEST):
    """用途：读取并校验固定层级；参数为清单路径，返回字典；供启动/单测调用，非法或重复数据拒绝写库。"""
    data = json.loads(Path(path).read_text())
    if data.get("version") != 1 or len(data["organizations"]) != 3 or len(data["teams"]) != 3:
        raise ValueError("数据集必须为 v1，包含三个组织和三个团队定义")
    for field in ("organizations", "teams", "providers"):
        slugs = [row.get("slug", row.get("id")) for row in data[field]]
        if len(set(slugs)) != len(slugs) or not all(re.fullmatch(r"[a-z][a-z0-9-]*", s or "") for s in slugs):
            raise ValueError(field + " 标识必须唯一且可用于邮箱")
    if any(len(o["members"]) != 9 or len(set(o["members"])) != 9 for o in data["organizations"]):
        raise ValueError("每个组织必须列出九个不同成员")
    if data["key_profiles"] != ["inherit", "fenno-only", "qiniu-only"]:
        raise ValueError("每个成员的三个密钥策略必须完整")
    rates = data["billing"]
    if any(not math.isfinite(rates[k]) or rates[k] <= 0 for k in ("input_cost_per_token", "output_cost_per_token")):
        raise ValueError("验收费率必须为有限正数")
    if len(data["providers"]) != 2 or data["limits"]["max_upstream_attempts"] < 100:
        raise ValueError("需要两个供应商及至少 100 次请求额度")
    agent = data.get("agent_conversation", {})
    if agent.get("agent_type") != "codex" or agent.get("endpoint") != "/v1/responses" \
            or agent.get("turns") != 3 or not str(agent.get("user_agent", "")).startswith("codex_cli_rs/") \
            or not agent.get("instructions") or not isinstance(agent.get("max_output_tokens"), int) \
            or agent["max_output_tokens"] < 32:
        raise ValueError("必须声明 Codex Responses 三轮会话、客户端标识、指令和输出额度")
    if data["limits"]["max_upstream_attempts"] < 81 * agent["turns"] + 100:
        raise ValueError("上游额度必须覆盖 81 个 Codex 三轮会话及其他验收请求")
    chains = data.get("logic_chains", {})
    cache_chain = chains.get("cache_consistency", {})
    weight_chain = chains.get("weighted_route_switch", {})
    fallback_chain = chains.get("fallback_cache", {})
    if cache_chain.get("identical_replays", 0) < 2 or not cache_chain.get("cross_key_isolation") \
            or not cache_chain.get("route_change_invalidates") or cache_chain.get("cache_hit_cost") != 0:
        raise ValueError("缓存一致性链必须覆盖重复请求、跨密钥隔离、路由失效和零费用")
    initial = weight_chain.get("initial_weights")
    updated = weight_chain.get("updated_weights")
    if initial != [100, 0] or updated != [0, 100] or any(
            not isinstance(value, (int, float)) or value < 0 for value in (initial or []) + (updated or [])):
        raise ValueError("权重切换链必须固定为 100:0 到 0:100")
    if fallback_chain.get("primary_failure_status") != 429 or fallback_chain.get("repeat_requests", 0) < 2 \
            or any(fallback_chain.get(field) is not True for field in (
                "same_session", "different_request_bodies", "reuse_successful_target")):
        raise ValueError("回退链必须验证 429、同会话不同正文的成功目标复用")
    required_roles = {"platform_admin", "organization_admin", "team_admin", "member"}
    if {p["role"] for p in data.get("personas", [])} != required_roles:
        raise ValueError("必须包含四类可登录身份")
    if {s["role"] for s in data.get("permission_scenarios", [])} != required_roles:
        raise ValueError("四类角色必须具有可执行权限场景")
    for scenario in data["permission_scenarios"]:
        if any(scenario.get(field) not in (200, 403) for field in (
                "own_team_limits", "foreign_team_limits", "team_models_update", "team_status_update")):
            raise ValueError("权限场景必须分别声明团队额度、跨组织额度、模型和状态的预期状态码")
    for guard in data["guardrails"]:
        params = guard["litellm_params"]
        if params.get("guardrail") == "custom_code" and (params.get("custom_code_language") != "xgo" or
                "func ApplyGuardrail(" not in params.get("custom_code", "") or not guard.get("acceptance")):
            raise ValueError("XGo必须包含完整源码及执行验收场景")
    required_models = {
        "fenno-chat", "qiniu-chat", "qiniu-glm", "fenno-image", "qiniu-ark-seedance",
        "qiniu-fal-seedance", "qiniu-fal-kling",
    }
    definitions = {row.get("id"): row for row in data.get("models", [])}
    if set(definitions) != required_models:
        raise ValueError("真实验收必须声明 GPT 双供应商、GLM、图片和三种视频模型")
    if data.get("verification_order") != ["gpt-5.6-sol", "z-ai/glm-5", "fenno-image",
                                               "qiniu-ark-seedance", "qiniu-fal-seedance", "qiniu-fal-kling"]:
        raise ValueError("验收顺序必须为 GPT、GLM、图片、Seedance 和 Kling")
    glm = definitions["qiniu-glm"]
    if any(glm.get(field) != value for field, value in {
            "provider": "qiniu", "kind": "chat", "selection": "exact",
            "public_name": "z-ai/glm-5", "upstream_model": "z-ai/glm-5",
            "stored_model": "z-ai/glm-5", "transport": "bypass_openai_chat",
            "endpoint_types": ["chat", "responses"]}.items()):
        raise ValueError("GLM 必须明确声明七牛 z-ai/glm-5 的 Chat/Responses 部署")
    expected = {
        "fenno-image": ("gpt-image-2", "bypass_openai_image_generation", "bypass:openai-images"),
        "qiniu-ark-seedance": ("bytedance/doubao-seedance-2-0-mini-260615", "qiniu_contents_generation", "bypass:ark-video"),
        "qiniu-fal-seedance": ("bytedance/seedance-2.0/mini/text-to-video", "qiniu_fal_doubao_20", "fal:queue"),
        "qiniu-fal-kling": ("fal-ai/kling-video/v2.5-turbo/pro/text-to-video", "qiniu_fal_kling", "fal:queue"),
    }
    for ident, (model, transport, endpoint) in expected.items():
        row = definitions[ident]
        if row.get("public_name") != model or row.get("upstream_model") != model:
            raise ValueError(ident + " 的公开模型名必须等于真实上游模型 ID")
        if row.get("transport") != transport or row.get("endpoint_types") != [endpoint]:
            raise ValueError(ident + " 的传输协议或端点类型不正确")
    if any(row.get("public_name") and (not row["public_name"].isascii() or re.search(r"[\s\u4e00-\u9fff]", row["public_name"])) for row in data["models"]):
        raise ValueError("公开模型名必须是英文真实上游模型 ID")
    shared = data.get("shared_chat_model", {})
    if shared.get("public_name") != "gpt-5.6-sol" or shared.get("default_weights") != [3, 7] \
            or shared.get("deployments") != {"fennoai": "gpt-5.6-sol", "qiniu": "openai/gpt-5.6-sol"}:
        raise ValueError("共享聊天模型必须明确声明 Fenno/七牛 gpt-5.6-sol 及 3:7 默认权重")
    for ident, provider, upstream in (("fenno-chat", "fennoai", "gpt-5.6-sol"),
                                      ("qiniu-chat", "qiniu", "openai/gpt-5.6-sol")):
        row = definitions[ident]
        if row.get("provider") != provider or row.get("selection") != "exact" \
                or row.get("upstream_model") != upstream or row.get("stored_model") != upstream \
                or row.get("public_name") != shared["public_name"]:
            raise ValueError(ident + " 必须映射到共享 gpt-5.6-sol 的精确真实部署")
        if row.get("endpoint_types") != ["chat", "responses"]:
            raise ValueError(ident + " 必须同时开放 Chat 和 Codex Responses 入口")
    variables = {"shared_chat_model": "gpt-5.6-sol",
                 "fenno_deployment": "f", "qiniu_deployment": "q"}
    for profile in ("organization-default", "team-default", "fenno-only", "qiniu-only"):
        resolve_document(data["routing"]["templates"][profile], variables)
    return data


def resolve_document(value, variables):
    """用途：递归解析模板占位符；参数为 JSON 和运行期 ID，返回新文档；供模板实例化，未声明变量立即失败，不改原清单。"""
    if isinstance(value, str) and value.startswith("$"):
        if value[1:] not in variables:
            raise ValueError("未声明模板变量: " + value)
        return variables[value[1:]]
    if isinstance(value, list):
        return [resolve_document(item, variables) for item in value]
    if isinstance(value, dict):
        return {resolve_document(k, variables): resolve_document(v, variables) for k, v in value.items()}
    return value


def hierarchy(data):
    """用途：展开 3×3×3 身份；参数为已校验清单，返回组织团队成员列表；邮箱使用保留域，不代表实际公司员工。"""
    return [dict(organization=o, team=t, index=i, name=o["members"][ti * 3 + i],
                 email=f'{o["slug"]}.{t["slug"]}.{i+1}@acceptance.xhub.invalid')
            for o in data["organizations"] for ti, t in enumerate(data["teams"]) for i in range(3)]


def write_private(path, data):
    """用途：保存可续跑的访问材料；参数为路径和 JSON，返回无；权限固定 0600，父目录由调用方设为 0700。"""
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    os.chmod(path, 0o600)
    with os.fdopen(fd, "w") as stream:
        json.dump(data, stream, ensure_ascii=False, indent=2)


def http(base, route, body=None, token=None, method=None, request_headers=None):
    """用途：发送真实 HTTP JSON；参数为地址/路径/正文/身份/方法及可选客户端头，返回状态、正文、头；供管理接口和 Codex 模拟调用，网络异常交给调用方。"""
    headers = {"Content-Type": "application/json", **(request_headers or {})}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = Request(base.rstrip("/") + route, data=None if body is None else json.dumps(body).encode(),
                  headers=headers, method=method or ("GET" if body is None else "POST"))
    try:
        # 图片生成可能在供应商侧同步运行数分钟；客户端窗口必须长于清单中的
        # 300 秒上游窗口，否则会由验收客户端提前断开，误报成网关失败。
        response = urlopen(req, timeout=330)
    except HTTPError as error:
        response = error
    with response:
        raw = response.read()
        try:
            value = json.loads(raw)
        except ValueError:
            value = {"non_json": True}
        return response.status, value, dict(response.headers)


def curl_json(base, route, body, token, timeout=180):
    """用途：用系统 curl 兼容供应商长连接并读取 JSON；参数为地址、路径、可空正文、令牌和超时，返回状态、正文、空响应头；异常文本不复述命令参数或令牌。"""
    command = ["curl", "--silent", "--show-error", "--max-time", str(timeout), "--output", "-",
               "--write-out", "\n%{http_code}", "-H", "Content-Type: application/json",
               "-H", "Authorization: Bearer " + token]
    if body is not None:
        command.extend(["--data-binary", json.dumps(body)])
    command.append(base.rstrip("/") + route)
    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE) as process:
        stdout, stderr = process.communicate()
    if process.returncode != 0:
        raise ConnectionError("curl 供应商请求失败，退出码=" + str(process.returncode) + "，错误=" + stderr.decode(errors="replace")[:200])
    raw, separator, status_text = stdout.rpartition(b"\n")
    if not separator or not status_text.isdigit():
        raise ConnectionError("curl 供应商响应缺少 HTTP 状态")
    try:
        value = json.loads(raw)
    except ValueError:
        value = {"non_json": True}
    return int(status_text), value, {}


def forward_supplier_chat(provider, body):
    """用途：把观察器收到的聊天请求原样转发给真实供应商；参数为供应商配置和请求正文，返回 HTTP 状态、JSON 正文与响应头；供 81 密钥及回退验收使用，网络错误交由观察器转换成明确的 502。"""
    base = os.environ.get(provider["base_env"], provider["base"]).rstrip("/")
    route = "/chat/completions" if base.endswith("/v1") else "/v1/chat/completions"
    return curl_json(base, route, body, os.environ[provider["key_env"]], timeout=330)


def chat_candidates(catalog, rng, preferred=()):
    """用途：从真实目录选择聊天候选；参数为目录、随机源和已知稳定真实 ID，返回去重列表；目录内的稳定模型优先，其余候选随机回退，探测成功才部署。"""
    ids = catalog.get("model_ids", [])
    candidates = [m for m in ids if re.search(r"gpt|claude|gemini|deepseek|qwen|kimi|glm|llama|hy|grok", m, re.I)
                  and not re.search(r"image|video|audio|tts|embed|rerank|realtime|whisper|vision|preview.*image", m, re.I)]
    candidates = sorted(set(candidates))
    rng.shuffle(candidates)
    preferred = [model for model in preferred if model in candidates]
    return preferred + [model for model in candidates if model not in preferred]


def bill_check(bill, key, rates):
    """用途：核对真实用量及五级计费归属；参数为账单、含显式及可选派生计费归属的密钥记录和费率，返回费用；供 API/浏览器验收，缺字段或金额不符抛异常。

    未绑定团队的个人密钥仍按调用时用户成员关系计入团队和组织，因此浏览器夹具可用
    billing_team_id/billing_organization_id 传入权威计费链；其余数据集继续使用密钥显式字段。
    """
    expected_team = key.get("billing_team_id", key["team_id"])
    expected_organization = key.get("billing_organization_id", key["organization_id"])
    for field, expected in (("api_key", key["token_id"]), ("user", key["user_id"]),
                            ("team_id", expected_team), ("project_id", key["project_id"]),
                            ("organization_id", expected_organization)):
        if bill.get(field) != expected:
            raise AssertionError("账单归属错误: " + field)
    if int(bill.get("prompt_tokens", 0)) <= 0 or int(bill.get("completion_tokens", 0)) <= 0:
        raise AssertionError("供应商必须返回非零真实输入输出用量")
    expected = bill["prompt_tokens"] * rates["input_cost_per_token"] + bill["completion_tokens"] * rates["output_cost_per_token"]
    if not math.isclose(float(bill["spend"]), expected, rel_tol=0, abs_tol=1e-9):
        raise AssertionError("真实账单与固定验收费率不一致")
    if not bill.get("metadata", {}).get("cost_breakdown"):
        raise AssertionError("缺少历史价格快照")
    return float(bill["spend"])
