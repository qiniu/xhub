#!/usr/bin/env python3
"""OpenAI-compatible fake upstream for browser e2e."""
from __future__ import annotations

import argparse
import json
import socket
import time
import uuid
from email.parser import BytesParser
from email.policy import default
from urllib.parse import urlsplit
from http.server import BaseHTTPRequestHandler, HTTPServer

if __package__:
    from .messages_reference_fixture import serve_reference_history
    from .stream_terminal_fixture import serve_stream_terminal
    from .claude_client_fixture import serve_claude_client
    from .messages_signed_fixture import serve_signed_history
else:
    from messages_reference_fixture import serve_reference_history
    from stream_terminal_fixture import serve_stream_terminal
    from claude_client_fixture import serve_claude_client
    from messages_signed_fixture import serve_signed_history
try:
    from e2e.agent_history_fixture import handle_agent_history
except ModuleNotFoundError:
    from agent_history_fixture import handle_agent_history


ERROR_LOG_JSON_DIAGNOSTIC = (
    "E2E upstream JSON diagnostic begin | "
    + "0123456789abcdef" * 128
    + " | E2E upstream JSON diagnostic end"
)
ERROR_LOG_TEXT_DIAGNOSTIC = (
    "E2E upstream plain-text diagnostic begin | "
    + "fedcba9876543210" * 128
    + " | E2E upstream plain-text diagnostic end"
)


VISION_PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNg+M8AAAICAQB7CYF4AAAAAElFTkSuQmCC"


def vision_answer(req: dict, path: str) -> str:
    """校验真实图片是否按目标协议完整抵达；参数为请求和上游路径，返回仅由图片决定的标记。

    前置浏览器上传的绿色 PNG，验证协议、MIME、Base64 字节及原生工具关联，缺图或错误内容抛 ValueError；不下载远程地址或保存文件。
    """
    messages = req.get("input" if path.endswith("/responses") else "messages", [])
    images = []
    tool_calls = set()
    for message in messages:
        parts = list(message.get("content", [])) if isinstance(message.get("content"), list) else []
        for part in parts:
            kind = part.get("type")
            if kind == "input_image" and path.endswith("/responses"):
                images.append(part.get("image_url"))
            elif kind == "image_url" and path.endswith("/chat/completions"):
                images.append(part.get("image_url", {}).get("url"))
            elif kind == "image" and path.endswith("/messages"):
                source = part.get("source", {})
                if source.get("type") != "base64" or source.get("media_type") != "image/png":
                    raise ValueError("invalid vision MIME or source")
                images.append("data:image/png;base64," + source.get("data", ""))
            elif kind == "tool_use" and path.endswith("/messages") and message.get("role") == "assistant":
                if not part.get("id") or part.get("name") != "read_image":
                    raise ValueError("invalid image tool call")
                tool_calls.add(part["id"])
            elif kind == "tool_result" and path.endswith("/messages") and message.get("role") == "user":
                if part.get("tool_use_id") not in tool_calls or not isinstance(part.get("content"), list):
                    raise ValueError("invalid image tool result")
                parts.extend(part["content"])
            elif kind not in ("text", "input_text", "output_text"):
                raise ValueError("wrong image protocol")
    if images != ["data:image/png;base64," + VISION_PNG]:
        raise ValueError("image payload lost or changed")
    return "green-pixel"


def codex_answer(req: dict) -> str:
    """用途：校验隔离 Codex 模拟器真实外发的历史；参数为 Chat 上游正文，返回项目标记。

    浏览器 E2E 专属模型调用；三轮必须依次包含 system、用户和助手历史，缺轮、乱序或泄漏代理字段抛 ValueError，无持久化副作用。
    """
    messages = req.get("messages", [])
    if "previous_response_id" in req or "store" in req or len(messages) not in (2, 4, 6):
        raise ValueError("Codex history or proxy fields invalid")
    first = messages[1].get("content", "")
    prefix, suffix = "Remember the project marker ", ". Reply with only that marker."
    if not first.startswith(prefix) or not first.endswith(suffix):
        raise ValueError("Codex initial project marker missing")
    marker = first[len(prefix):-len(suffix)]
    if not marker or messages[0].get("role") != "system":
        raise ValueError("Codex instructions or marker missing")
    prompts = [first, "What project marker did I give you? Reply with only the marker.",
               "Confirm the same project marker once more. Reply with only the marker."]
    expected = [messages[0]]
    for index in range(len(messages) // 2):
        expected.append({"role": "user", "content": prompts[index]})
        if index < len(messages) // 2 - 1:
            expected.append({"role": "assistant", "content": marker})
    if messages != expected:
        raise ValueError("Codex upstream did not receive full conversation")
    return marker


def continuation_answer(req: dict) -> str:
    """校验续接用例的完整历史；参数为真实上游正文，返回本轮标记或抛出 ValueError。

    浏览器 E2E 的专属模型调用；校验三轮用户/助手顺序和代理字段隔离，无外部调用或持久化。
    """
    messages = req.get("messages", [])
    if "previous_response_id" in req or "store" in req or len(messages) not in (1, 3, 5):
        raise ValueError("continuation history or proxy fields invalid")
    turn = (len(messages) + 1) // 2
    expected = []
    for index in range(1, turn + 1):
        expected.append({"role": "user", "content": f"continuation-{index}"})
        if index < turn:
            expected.append({"role": "assistant", "content": f"history-ok-{index}"})
    if messages != expected:
        raise ValueError("upstream did not receive full conversation")
    return f"history-ok-{turn}"


def continuation_usage(req: dict) -> dict:
    """返回续接夹具的原始统计；参数为上游请求，返回含供应商扩展字段的 usage。

    供本地三轮浏览器测试调用，首轮零缓存、后续读4个token；无外部服务或持久化副作用。
    """
    turn = (len(req.get("messages", [])) + 1) // 2
    return {"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10,
            "prompt_tokens_details": {"cached_tokens": 0 if turn == 1 else 4},
            "provider_statistics": {"turn": turn, "cache_source": "local-fixture"}}


class Handler(BaseHTTPRequestHandler):
    request_count = 0
    ark_tasks: dict[str, dict] = {}
    fal_tasks: dict[str, dict] = {}

    def _ark(self, req: dict | None = None) -> bool:
        """校验官方 Ark 本地任务请求；req 为正文或 None，返回路径是否匹配。

        调用：浏览器 E2E；只接受 Bearer 和显式模型、非空 content；保存内存任务，进程结束清理。
        """
        roots = ("/api/v3/contents/generations/tasks", "/v3/contents/generations/tasks")
        root = next((candidate for candidate in roots if self.path.startswith(candidate)), None)
        if root is None:
            return False
        if self.headers.get("Authorization") != "Bearer sk-fake":
            self.send_error(401)
            return True
        if req is not None and self.path == root:
            if not req.get("model") or not req.get("content"):
                self.send_error(400, "invalid Ark request body")
                return True
            task = "ark-e2e-" + uuid.uuid4().hex
            self.ark_tasks[task] = req
            self._json({"id": task})
            return True
        task = self.path.removeprefix(root + "/")
        if req is None and task in self.ark_tasks:
            # 特定生命周期夹具第一次查询保持运行，下一次完成；其他既有调用保持立即完成。
            facts = self.ark_tasks[task]
            controlled = any(item.get("text") in ("e2e-task-lifecycle", "e2e-task-failure") for item in facts.get("content", []))
            failed = any(item.get("text") == "e2e-task-failure" for item in facts.get("content", []))
            polls = facts.get("_test_polls", 0)
            facts["_test_polls"] = polls + 1
            state = "running" if controlled and polls == 0 else ("failed" if failed else "succeeded")
            self._json({"id": task, "status": state, "content": {"video_url": "https://example.invalid/ark-e2e.mp4"}, "usage": {"completion_tokens": 100}})
        else:
            self.send_error(404)
        return True

    def _fal(self, req: dict | None = None) -> bool:
        """验证已实现 Fal 队列与 Key 鉴权；req 为创建正文或 None，返回是否匹配。

        仅用于真实网关 E2E，覆盖 Doubao 与 Dreamina Seedance 路径，内存任务随服务退出清理。
        """
        root = next((prefix for prefix in ("/queue/bytedance/seedance-2.0", "/queue/byteplus/seedance-2.0") if self.path.startswith(prefix)), "")
        if not root:
            return False
        if self.headers.get("Authorization") != "Key sk-fake":
            self.send_error(401)
            return True
        if req is not None and self.path == root + "/text-to-video":
            if "model" in req or not req.get("prompt"):
                self.send_error(400)
                return True
            task = "fal-e2e-" + uuid.uuid4().hex
            self.fal_tasks[task] = req
            self._json({"request_id": task})
        elif req is None and self.path.removeprefix(root + "/requests/") in self.fal_tasks:
            self._json({"status": "COMPLETED", "video_url": "https://example.invalid/fal.mp4"})
        else:
            self.send_error(404)
        return True

    def do_GET(self) -> None:
        """按本地供应商路径返回目录，验证 /v1 双向回退、200 网站正文及失败降级。

        参数：无，使用当前 HTTP 请求；返回：无，写入目录或 404 响应。
        调用：浏览器模型配置 E2E；不访问外部服务，服务器结束即清理。
        """
        if self.path == "/e2e/request-count":
            self._json({"requests": Handler.request_count})
            return
        if self.path.rstrip("/") == "/v1/market/models":
            # 确定性市场数据覆盖公开、私有、退役、多模态与完整价格，测试进程结束回收。
            rows = []
            for model_id, private, retired, output in [
                ("e2e-market-chat", False, "", "text"),
                ("e2e-market-video", False, "", "video"),
                ("e2e-market-private", True, "", "text"),
                ("e2e-market-retired", False, "2020-01-01T00:00:00Z", "text"),
            ]:
                rows.append({"id": model_id, "name": model_id, "private": private,
                    "features": ["工具调用" if output == "text" else "视频生成"], "hot_tags": ["热门"],
                    "retirement_at": retired, "issuer": {"name": "OpenAI" if output == "text" else "ByteDance"},
                    "architecture": {"input_modalities": ["text"], "output_modalities": [output],
                        "function_calling": {"supported": output == "text"}},
                    "model_constraints": {"context_length": 128000},
                    "pricing_rules_v2": [{"input_range": [0, 128000], "details_v2": {
                        "text_input": {"name": "文本输入", "unit_name": "token", "unit_size": 1000000, "unit_price_usd": 2, "unit_price": 14},
                        "text_output": {"name": "文本输出", "unit_name": "token", "unit_size": 1000000, "unit_price_usd": 6, "unit_price": 42}}}],
                    "support_api_protocols": ["openai"]})
            self._json({"status": True, "data": rows})
            return
        if self._ark() or self._fal():
            return
        if self.path.rstrip("/") in ("/discovery-html/models", "/discovery-invalid/models", "/discovery-invalid/v1/models"):
            # 复现供应商网站的兜底路由：不存在的 API 目录仍返回 200 HTML。
            body = b"<!doctype html><html><title>Provider website</title></html>"
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path.rstrip("/") in ("/models", "/v1/models",
                                           "/discovery-v1/v1/models", "/discovery-root/models", "/discovery-html/v1/models"):
            # 完整返回155条上游模型，验证列表不会被截断或附加隐式专用模型。
            self._json({"data": [{"id": "gpt-4o-mini"}] + [{"id": f"e2e-model-{i}"} for i in range(154)]})
            return
        self.send_error(404)

    def do_POST(self) -> None:
        """读取当前 POST JSON 并返回对应本地推理结果，供浏览器 E2E 调用。

        参数：无；返回：无，写 HTTP 响应；非法 JSON 按空对象处理。
        Ark 任务由 _ark 保存；原生图片和 Google 用例校验字段与上游鉴权，进程结束清理数据。
        """
        Handler.request_count += 1
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n)
        try:
            req = json.loads(raw.decode() or "{}")
        except (json.JSONDecodeError, UnicodeDecodeError):
            req = {}
        if serve_reference_history(self, req):
            return
        if handle_agent_history(self, req):
            return
        if self._ark(req) or self._fal(req):
            return
        if self._monitor_tps(req):
            return
        if req.get("model", "").startswith("e2e-vision-"):
            try:
                text = vision_answer(req, self.path)
                # 软预算不注入cap：Chat/Responses允许省略，Messages保留协议必填约束；显式cap仍校验。
                if req["model"].startswith("e2e-vision-budget-"):
                    cap_field = "max_output_tokens" if self.path.endswith("/responses") else "max_tokens"
                    if (cap_field in req or self.path.endswith("/messages")) and (type(req.get(cap_field)) is not int or not 1 <= req[cap_field] <= 8192):
                        raise ValueError("budgeted vision output cap missing")
            except (ValueError, TypeError, AttributeError):
                self._response(400, b'{"error":{"message":"image payload invalid"}}', "application/json")
                return
            usage = {"prompt_tokens": 8, "completion_tokens": 2}
            if self.path.endswith("/chat/completions"):
                payload = {"id": "chatcmpl_vision", "choices": [{"message": {"role": "assistant", "content": text}, "finish_reason": "stop"}], "usage": usage}
                if req.get("stream"):
                    self._sse([{"id": "chatcmpl_vision", "choices": [{"index": 0, "delta": {"content": text}, "finish_reason": "stop"}], "usage": usage}, "[DONE]"])
                else:
                    self._json(payload)
            elif self.path.endswith("/messages"):
                payload = {"id": "msg_vision", "type": "message", "role": "assistant", "model": req["model"], "content": [{"type": "text", "text": text}], "stop_reason": "end_turn", "stop_sequence": None, "usage": {"input_tokens": 8, "output_tokens": 2}}
                if req.get("stream"):
                    self._sse([
                        {"type": "message_start", "message": {**payload, "content": [], "stop_reason": None, "usage": {"input_tokens": 8, "output_tokens": 0}}},
                        {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}},
                        {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": text}},
                        {"type": "content_block_stop", "index": 0},
                        {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 2}},
                        {"type": "message_stop"},
                    ])
                else:
                    self._json(payload)
            else:
                item = {"type": "message", "id": "msg_vision", "role": "assistant", "content": [{"type": "output_text", "text": text}]}
                payload = {"id": "resp_vision", "status": "completed", "output": [item], "usage": {"input_tokens": 8, "output_tokens": 2}}
                if req.get("stream"):
                    self._sse([{"type": "response.output_text.delta", "delta": text}, {"type": "response.completed", "response": payload}])
                else:
                    self._json(payload)
            return
        model = req.get("model") or "gpt-4o-mini"
        if serve_stream_terminal(self, req):
            return
        if serve_signed_history(self, req):
            return
        if serve_claude_client(self, req):
            return
        # 专属搜索软预算夹具：必须真实收到搜索且没有额外次数上限，返回完整token用量，不能靠删除搜索工具通过。
        if str(model).startswith("budget-search-browser-"):
            if not self.path.endswith("/responses") or "max_tool_calls" in req or not any(t.get("type") == "web_search" for t in req.get("tools", [])):
                self.send_error(400, "search fixture requires Responses, search and no injected tool cap")
                return
            result = {"id": "resp_budget_search", "object": "response", "status": "completed", "output": [{"id": "ws_budget_search", "type": "web_search_call", "status": "completed", "action": {"type": "search", "queries": ["Go programming language"]}}, {"id": "msg_budget_search", "type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "e2e-budget-search"}]}], "usage": {"input_tokens": 8, "output_tokens": 2}}
            if req.get("stream"):
                self._sse([{"type": "response.completed", "response": result}])
            else:
                self._json(result)
            return
        # 正常达到输出上限仍有可靠用量；原生 JSON/SSE 保留 incomplete，浏览器验证实际费用与释放预占。
        if str(model).startswith("pending-browser-hard-budget-length-"):
            if not self.path.endswith("/responses") or req.get("max_output_tokens") != 8:
                self.send_error(400, "length fixture requires Responses and cap 8")
                return
            result = {"id": "resp_budget_length", "object": "response", "status": "incomplete", "incomplete_details": {"reason": "max_output_tokens"}, "output": [{"id": "msg_budget_length", "type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "e2e-budget-length"}]}], "usage": {"input_tokens": 8, "output_tokens": 8}}
            if req.get("stream"):
                self._sse([{"type": "response.incomplete", "response": result}])
            else:
                self._json(result)
            return
        # 专属硬额度场景分别验证总量/缓存同帧矛盾不能被后续好帧清除，以及跨帧正常累计不能因旧别名误判。
        budget_prompt = req.get("messages", [{}])[-1].get("content") if req.get("messages") else None
        if str(model).startswith("pending-browser-hard-budget-") and budget_prompt in ("Return contradictory usage.", "Verify cumulative usage.", "Return contradictory cache write usage.", "Return contradictory cache read usage.", "Verify cumulative cache usage.", "Verify cache projection."):
            if budget_prompt == "Verify cache projection." and req.get("stream") is not True and "chat/completions" in self.path:
                # 此同步响应经真实网关转为 Messages，缓存命中属于 prompt_tokens 子集，不能额外再计输入费用。
                self._json({"id": "budget-cache-projection", "choices": [{"index": 0, "message": {"role": "assistant", "content": "e2e-budget-cache-projection"}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 8, "completion_tokens": 2, "cache_write_tokens": 2, "prompt_tokens_details": {"cached_tokens": 2}}})
                return
            if req.get("stream") is not True or "chat/completions" not in self.path:
                self.send_error(400, "budget usage fixture requires chat streaming")
                return
            conflict = budget_prompt == "Return contradictory usage."
            initial_usage = {"prompt_tokens": 8, "input_tokens": 9 if conflict else 8, "completion_tokens": 0, "output_tokens": 0}
            text = "e2e-budget-conflict" if conflict else "e2e-budget-growth"
            final_usage = {"prompt_tokens": 8, "completion_tokens": 2}
            if "cache" in budget_prompt:
                initial_usage.update(cache_creation_input_tokens=0, cache_write_tokens=0, cached_tokens=0, prompt_tokens_details={"cached_tokens": 0})
                final_usage.update(cache_write_tokens=2, cached_tokens=2)
                text = "e2e-budget-cache-growth"
                if "write" in budget_prompt:
                    initial_usage["cache_write_tokens"] = 50
                    text = "e2e-budget-cache-conflict"
                elif "read" in budget_prompt:
                    initial_usage["prompt_tokens_details"]["cached_tokens"] = 8
                    text = "e2e-budget-cache-conflict"
            self._sse([
                {"id": "budget-conflict", "choices": [{"index": 0, "delta": {"content": text}}], "usage": initial_usage},
                {"id": "budget-conflict", "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": final_usage},
                "[DONE]",
            ])
            return
        if model in ("e2e-item-id-chat", "e2e-item-id-native", "e2e-item-id-grok", "e2e-item-id-tracked"):
            # 严格原生夹具按真实 GPT 的 item 类型检查 ID，浏览器必须回放真实桥接产物而非手工修正。
            if self.headers.get("Authorization") != "Bearer sk-fake":
                self.send_error(401)
                return
            if model == "e2e-item-id-chat":
                if "chat/completions" not in self.path:
                    self.send_error(400, "Chat source received wrong protocol")
                    return
                message = {"role": "assistant", "content": "e2e-source", "reasoning_content": "Plan"}
                stop = "stop"
                if req.get("tools"):
                    message.update(content=None, tool_calls=[{"index": 0, "id": "call-e2e", "type": "function", "function": {"name": "lookup_value", "arguments": "{}"}}])
                    stop = "tool_calls"
                usage = {"prompt_tokens": 8, "completion_tokens": 2}
                chunk = {"id": "chatcmpl-e2e-source", "choices": [{"index": 0, "delta": message, "finish_reason": stop}]}
                if req.get("stream"):
                    self._sse([chunk, {"id": "chatcmpl-e2e-source", "choices": [], "usage": usage}, "[DONE]"])
                else:
                    self._json({"id": "chatcmpl-e2e-source", "choices": [{"message": message, "finish_reason": stop}], "usage": usage})
                return
            items = req.get("input", [])
            if not isinstance(items, list) or "responses" not in self.path:
                self.send_error(400, "Native target requires Responses history")
                return
            for item in items if model == "e2e-item-id-native" else []:
                if "internal_chat_message_metadata_passthrough" in item or "metadata" in item:
                    self._response(400, json.dumps({"error": {"message": "Unknown input message tracking metadata"}}).encode(), "application/json")
                    return
                prefix = {"message": "msg_", "reasoning": "rs_", "function_call": "fc_"}.get(item.get("type"))
                if item.get("type") == "reasoning" and (str(item.get("id", "")).startswith("rs_xhub_") or "92ae29c5-" in str(item.get("id", "")) or "status" in item):
                    self._response(400, json.dumps({"error": {"message": "Synthetic reasoning item not found"}}).encode(), "application/json")
                    return
                if prefix and item.get("id") and not str(item["id"]).startswith(prefix):
                    self._response(400, json.dumps({"error": {"message": "Invalid item id prefix"}}).encode(), "application/json")
                    return
            item = {"id": "msg_e2e_native", "type": "message", "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": "e2e-switched", "annotations": []}]}
            payload = {"id": "resp_e2e_native", "object": "response", "status": "completed", "output": [item], "usage": {"input_tokens": 8, "output_tokens": 2}}
            if model == "e2e-item-id-grok":
                # 真实 Grok 原生 Responses 公开摘要包含 UUID/status；这些输出元数据必须在跨厂商回放时规范化。
                seed = "92ae29c5-9e2c-9cbd-a94b-2fd2abc135be"
                item["id"] = "msg_" + seed
                payload["output"] = [{"type": "reasoning", "id": "rs_" + seed, "status": "completed", "summary": [{"type": "summary_text", "text": "Public Grok plan"}]}, item]
            if model == "e2e-item-id-tracked":
                # Fenno GPT 偶发返回同值双追踪对象；严格目标不接受这些输出专用字段再次作为输入。
                item.update(phase="final_answer", internal_chat_message_metadata_passthrough={"create_time": 1.0, "turn_id": "trace"}, metadata={"turn_id": "trace"})
            if req.get("stream"):
                self._sse([{"type": "response.created", "response": {"id": payload["id"], "status": "in_progress", "output": []}}, {"type": "response.output_item.added", "output_index": 0, "item": {**item, "status": "in_progress", "content": []}}, {"type": "response.output_text.delta", "item_id": item["id"], "output_index": 0, "content_index": 0, "delta": "e2e-switched"}, {"type": "response.output_item.done", "output_index": 0, "item": item}, {"type": "response.completed", "response": payload}])
            else:
                self._json(payload)
            return
        if model == "e2e-agent-proxy-slow":
            # 真实等待超过旧网关 60 秒，验证浏览器、控制台代理及网关不会提前断开；仅此隔离夹具触发。
            time.sleep(65)
        if self._error_log_response(str(model)):
            return
        # 兼容层允许保留 openai/ 前缀；只对这两个专属夹具规范化，避免影响其他模型行为。
        history_model = str(model).removeprefix("openai/")
        if history_model in ("e2e-responses-history", "e2e-codex-agent"):
            try:
                text = codex_answer(req) if history_model == "e2e-codex-agent" else continuation_answer(req)
            except ValueError as error:
                self.send_error(400, str(error))
                return
            response_id = "chatcmpl-" + uuid.uuid4().hex
            usage = continuation_usage(req) if history_model == "e2e-responses-history" else {"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10}
            if req.get("stream") is True:
                chunks = [
                    {"id": response_id, "choices": [{"index": 0, "delta": {"content": text}}]},
                    {"id": response_id, "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": usage},
                ]
                body = ("".join("data: " + json.dumps(chunk) + "\n\n" for chunk in chunks) + "data: [DONE]\n\n").encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self._diagnostic_headers(history_model)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            else:
                self._json({"id": response_id, "object": "chat.completion", "model": model,
                    "choices": [{"index": 0, "message": {"role": "assistant", "content": text}, "finish_reason": "stop"}], "usage": usage}, diagnostic_model=history_model)
            return
        # 仅测试自建型号前缀触发回退错误，不影响其他浏览器用例或已有模型。
        for prefix, status, code in (("e2e-fallback-500-", 500, "api_error"),
                                     ("e2e-fallback-429-", 429, "rate_limit_exceeded"),
                                     ("e2e-fallback-context-", 400, "context_length_exceeded"),
                                     ("e2e-fallback-content-", 400, "content_policy_violation"),
                                     ("e2e-fallback-normal-", 400, "invalid_request")):
            if str(model).startswith(prefix):
                payload = json.dumps({"error": {"code": code, "message": code}}).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)
                return
        path = self.path
        if "embeddings" in path:
            payload = {
                "object": "list",
                "model": model,
                "data": [{"object": "embedding", "index": 0, "embedding": [0.1, 0.2]}],
                "usage": {"prompt_tokens": 3, "total_tokens": 3},
            }
            self._json(payload)
            return
        if "transcription" in path or "translation" in path:
            self._json({"text": "e2e-ok", "language": "en", "duration": 1.0, "segments": []})
            return
        if "moderations" in path:
            self._json({
                "id": "modr_e2e",
                "model": model,
                "results": [{"flagged": False, "categories": {}, "category_scores": {}}],
            })
            return
        if "rerank" in path:
            self._json({
                "id": "rerank_e2e",
                "results": [{"index": 0, "relevance_score": 0.9}],
                "meta": {"tokens": {"input_tokens": 1}},
            })
            return
        if path.endswith("/messages"):
            payload = {"id": "msg_e2e", "type": "message", "role": "assistant", "model": model,
                       "content": [{"type": "text", "text": "e2e-ok"}], "stop_reason": "end_turn",
                       "usage": {"input_tokens": 8, "output_tokens": 2}}
            if req.get("stream") is True:
                self._sse([
                    {"type": "message_start", "message": {"id": "msg_e2e", "usage": {"input_tokens": 8}}},
                    {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}},
                    {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "e2e-ok"}},
                    {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"output_tokens": 2}},
                    {"type": "message_stop"},
                ])
            else:
                self._json(payload)
            return
        if model == "e2e-client-tool-search":
            # 客户端发现三轮夹具：只接收真正转换的 Chat，明确检查动态工具加载和结果关联；不替网关转换请求或补身份。
            tools = req.get("tools", [])
            if "chat/completions" not in path or any(t.get("type") != "function" for t in tools):
                self.send_error(400, "client search fixture requires Chat functions")
                return
            names = [t.get("function", {}).get("name") for t in tools]
            messages = req.get("messages", [])
            results = [m for m in messages if m.get("role") == "tool"]
            done = any(m.get("tool_call_id") == "read_1" and "file-value" in str(m.get("content")) for m in results)
            discovered = any(m.get("tool_call_id") == "search_1" for m in results)
            name = "fs__read" if discovered else "tool_search"
            if not done and name not in names:
                self.send_error(400, "client search did not load the required function")
                return
            message = {"role": "assistant", "content": "e2e-ok" if done else None}
            stop = "stop" if done else "tool_calls"
            if not done:
                # 七牛实测返回 input 别名，浏览器验收必须覆盖这个实际差异，不能继续只返回理想 content 包装。
                arguments = {"input": "Read the file."} if discovered else {"query": "read a file"}
                message["tool_calls"] = [{"index": 0, "id": "read_1" if discovered else "search_1", "type": "function", "function": {"name": name, "arguments": json.dumps(arguments)}}]
            usage = {"prompt_tokens": 8, "completion_tokens": 2}
            if req.get("stream"):
                self._sse([
                    {"id": "chat_search", "choices": [{"index": 0, "delta": message, "finish_reason": stop}]},
                    {"id": "chat_search", "choices": [], "usage": usage},
                    "[DONE]",
                ])
            else:
                self._json({"id": "chat_search", "choices": [{"message": message, "finish_reason": stop}], "usage": usage})
            return
        if model in ("e2e-agent-messages", "e2e-agent-messages-encrypted", "e2e-agent-messages-redacted", "e2e-agent-chat-tools", "e2e-agent-chat-reasoning") and ("responses" in path or "chat/completions" in path):
            # Chat/Messages 推理桥接夹具：校验工具续轮还原独立推理及调用 ID，而不是把推理当普通答案。
            is_responses = "responses" in path
            items = req.get("input" if is_responses else "messages", [])
            second = isinstance(items, list) and any(i.get("type") == "function_call_output" or i.get("role") == "tool" for i in items)
            chat_reasoning = model == "e2e-agent-chat-reasoning"
            encrypted = model in ("e2e-agent-messages-encrypted", "e2e-agent-messages-redacted", "e2e-agent-chat-reasoning")
            redacted = model == "e2e-agent-messages-redacted"
            if second and ((not redacted and "Plan" not in json.dumps(items)) or "call_1" not in json.dumps(items)):
                self.send_error(400, "public reasoning or tool identity lost")
                return
            if second and chat_reasoning:
                # Chat 扩展保留原厂 ID；客户端忽略扩展时允许普通文本/工具历史，有密文则必须原样还原。
                opaque_items = [i for i in items if i.get("encrypted_content")]
                if any(i.get("type") != "reasoning" or i.get("id") != "rs_public" or i.get("encrypted_content") not in ("fixture-encrypted", "fixture-encrypted-terminal") for i in opaque_items):
                    self.send_error(400, "Chat reasoning identity or ciphertext changed")
                    return
            if second and encrypted and not chat_reasoning and not any(i.get("type") == "reasoning" and i.get("encrypted_content") == "fixture-encrypted" and "id" not in i for i in items):
                # 只接受客户端原样回传的封装还原结果；夹具不替网关补齐密文或伪造 ID。
                self.send_error(400, "encrypted reasoning replay lost")
                return
            tool = bool(req.get("tools")) and not second
            tool_name, tool_arguments = "read", "{}"
            if model == "e2e-agent-chat-tools":
                # 夹具只暴露 Chat；浏览器的 namespace/custom 必须由真实网关映射，不能把 Responses 原文直接转发过来。
                if is_responses or req.get("include") or req.get("client_metadata"):
                    self.send_error(400, "Chat-only agent fixture received native Responses fields")
                    return
                if tool:
                    function = req["tools"][0].get("function", {})
                    tool_name = function.get("name")
                    if tool_name != "fs__read" or req.get("reasoning_effort") != "medium":
                        self.send_error(400, "namespace or reasoning effort lost")
                        return
                    if "content" in function.get("parameters", {}).get("properties", {}):
                        tool_arguments = json.dumps({"content": "Read the file."})
            if is_responses:
                output = [{"id": "rs_public", "type": "reasoning", "summary": [{"type": "summary_text", "text": "Plan"}]}]
                if encrypted:
                    output[0]["encrypted_content"] = "fixture-encrypted"
                if redacted:
                    output[0]["summary"] = []
                output.append({"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "read", "arguments": "{}"} if tool else {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "e2e-ok"}]})
                payload = {"id": "resp_public", "status": "completed", "output": output, "usage": {"input_tokens": 8, "output_tokens": 2}}
                events = [{"type": "response.reasoning_summary_text.delta", "delta": "Plan"}]
                if encrypted:
                    # 对应真实 Responses 生命周期，密文直到 output_item.done 才完整，Messages 必须先发送签名再关块。
                    events = [{"type": "response.output_item.added", "item": {"id": "rs_public", "type": "reasoning"}}]
                    if not redacted:
                        events.append({"type": "response.reasoning_summary_text.delta", "item_id": "rs_public", "delta": "Plan"})
                    events.append({"type": "response.output_item.done", "item": output[0]})
                if tool:
                    events.extend([{"type": "response.output_item.added", "output_index": 1, "item": {"type": "function_call", "call_id": "call_1", "name": "read", "arguments": ""}}, {"type": "response.function_call_arguments.delta", "output_index": 1, "delta": "{}"}])
                else:
                    events.append({"type": "response.output_text.delta", "delta": "e2e-ok"})
                # 部分真实 GPT 连接会在终态重新加密同一推理，item.done 的有效封装不能被重复或覆盖。
                terminal = {**payload, "output": [{**output[0], "encrypted_content": "fixture-encrypted-terminal"}, *output[1:]]} if encrypted else payload
                events.append({"type": "response.completed", "response": terminal})
            else:
                message = {"role": "assistant", "content": None if tool else "e2e-ok", "reasoning_content": "Plan"}
                if tool:
                    message["tool_calls"] = [{"index": 0, "id": "call_1", "type": "function", "function": {"name": tool_name, "arguments": tool_arguments}}]
                usage = {"prompt_tokens": 8, "completion_tokens": 2}
                stop = "tool_calls" if tool else "stop"
                payload = {"id": "chat_public", "choices": [{"message": message, "finish_reason": stop}], "usage": usage}
                # usage 单独出现在工具/正文结束之后，检验 Messages 桥接能把晚到的输入 Token 回填给客户端。
                events = [{"id": "chat_public", "choices": [{"index": 0, "delta": message, "finish_reason": stop}]}, {"id": "chat_public", "choices": [], "usage": usage}]
            if req.get("stream"):
                # Chat 的 DONE 是裸标记而非 JSON 字符串；不得用 _sse 把它转义为带引号的值。
                body = "".join("data: " + json.dumps(event) + "\n\n" for event in events)
                if not is_responses:
                    body += "data: [DONE]\n\n"
                self._response(200, body.encode(), "text/event-stream")
            else:
                self._json(payload)
            return
        if model in ("e2e-portable-gpt", "e2e-portable-grok", "e2e-portable-deepseek", "e2e-portable-outside"):
            # 迁移夹具模拟不同账户的密文边界，所有网络仍经真实网关和本地 HTTP。
            encoded = json.dumps(req)
            if (model != "e2e-portable-outside" and "outside-cipher" in encoded) or (model == "e2e-portable-outside" and "gateway-cipher" in encoded):
                self.send_error(400, "foreign encrypted reasoning")
                return
            items = req.get("input", [])
            second = isinstance(items, list) and any(item.get("type") == "function_call_output" for item in items)
            if "chat/completions" in path:
                payload = {"id": "chat-portable", "choices": [{"message": {"role": "assistant", "content": "portable-ok"}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 8, "completion_tokens": 2}}
                if req.get("stream"):
                    event = {"id": "chat-portable", "choices": [{"index": 0, "delta": {"content": "portable-ok"}, "finish_reason": "stop"}], "usage": payload["usage"]}
                    self._response(200, ("data: " + json.dumps(event) + "\n\ndata: [DONE]\n\n").encode(), "text/event-stream")
                else:
                    self._json(payload)
                return
            reasoning = {"id": "rs_portable", "type": "reasoning", "summary": [{"type": "summary_text", "text": "Public plan"}], "encrypted_content": "gateway-cipher"}
            answer = {"id": "msg_portable", "type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "portable-ok"}]}
            if req.get("tools") and not second:
                answer = {"id": "fc_portable", "type": "function_call", "call_id": "call_portable", "name": "read", "arguments": "{}"}
            payload = {"id": "resp_" + uuid.uuid4().hex, "object": "response", "status": "completed", "model": model, "output": [reasoning, answer], "usage": {"input_tokens": 8, "output_tokens": 2}}
            if req.get("stream"):
                events = [{"type": "response.output_item.added", "output_index": 0, "item": reasoning}, {"type": "response.output_item.done", "output_index": 0, "item": reasoning}]
                events.append({"type": "response.output_item.added", "output_index": 1, "item": answer})
                if answer["type"] == "message":
                    events.append({"type": "response.output_text.delta", "item_id": answer["id"], "output_index": 1, "delta": "portable-ok"})
                events.extend([{"type": "response.output_item.done", "output_index": 1, "item": answer}, {"type": "response.completed", "response": payload}])
                self._sse(events)
            else:
                self._json(payload)
            return
        if "responses" in path and model == "e2e-agent-native":
            # 专属 Agent 夹具检查字段和完整历史，网关仍是真实实现；不连接任何外部服务。
            advanced = "reasoning" in req or "include" in req
            if advanced and req.get("include") and (req.get("reasoning", {}).get("effort") != "medium" or not req.get("text")):
                self.send_error(400, "agent options lost")
                return
            items = req.get("input", [])
            second = isinstance(items, list) and any(i.get("type") == "function_call_output" for i in items)
            if second and not any(i.get("encrypted_content") == "fixture-cipher" or (i.get("type") == "reasoning" and i.get("summary")) for i in items):
                self.send_error(400, "reasoning history lost")
                return
            output = []
            if req.get("include"):
                output.append({"id": "rs_" + uuid.uuid4().hex, "type": "reasoning", "summary": [{"type": "summary_text", "text": "Plan"}], "encrypted_content": "fixture-cipher"})
            if req.get("tools") and not second:
                output.append({"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "read", "arguments": "{}", "status": "completed"})
            else:
                output.append({"id": "msg_" + uuid.uuid4().hex, "type": "message", "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": "e2e-ok", "annotations": []}]})
            payload = {"id": "resp_" + uuid.uuid4().hex, "object": "response", "status": "completed", "model": model, "output": output,
                       "usage": {"input_tokens": 8, "output_tokens": 2, "output_tokens_details": {"reasoning_tokens": 1}, "vendor_counter": 42}}
            if req.get("stream"):
                events = [{"type": "response.reasoning_summary_text.delta", "sequence_number": 7, "output_index": 0, "delta": "Plan"},
                          {"type": "response.completed", "sequence_number": 8, "response": payload}]
                self._sse(events)
            else:
                self._json(payload)
            return
        if "responses" in path:
            payload = {
                "id": "resp_e2e",
                "object": "response",
                "status": "completed",
                "model": model,
                "output": [{"type": "message", "content": [{"type": "output_text", "text": "e2e-ok"}]}],
                "usage": {"input_tokens": 8, "output_tokens": 2, "total_tokens": 10},
            }
            if req.get("stream") is True:
                event = json.dumps({"type": "response.output_text.delta", "delta": "e2e-ok"})
                done = json.dumps({"type": "response.completed", "response": payload})
                body = f"data: {event}\n\ndata: {done}\n\ndata: [DONE]\n\n".encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            self._json(payload)
            return
        if "/images" in path:
            # 专用图片夹具严格检查尺寸，复现中转渠道缺省 size 时的 400；普通夹具保持原行为。
            if req.get("model", "").startswith("e2e-native-real-") or b"e2e-native-real-image_edit" in raw:
                if self.headers.get("Authorization") != "Bearer sk-fake":
                    self.send_error(401)
                    return
                if path.endswith("/edits"):
                    message = BytesParser(policy=default).parsebytes(
                        ("Content-Type: " + self.headers.get("Content-Type", "") + "\r\n\r\n").encode() + raw)
                    fields = {part.get_param("name", header="content-disposition"): part.get_payload(decode=True) for part in message.iter_parts()}
                    if fields.get("model") != b"e2e-native-real-image_edit" or not fields.get("image") or fields.get("prompt") != b"hello":
                        self.send_error(400, "invalid native image edit")
                        return
                elif req.get("prompt") != "hello":
                    self.send_error(400, "invalid native image generation")
                    return
                if path.endswith("/generations") and req.get("size") not in ("1024x1024", "1536x1024", "1024x1536"):
                    self.send_error(400, 'image size "' + str(req.get("size", "")) + '" is not available for this channel')
                    return
                png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a0S8AAAAASUVORK5CYII="
                self._json({"created": 1, "data": [{"b64_json": png, "revised_prompt": "e2e-ok"}], "usage": {"input_tokens": 8, "output_tokens": 2}})
                return
            self._json({"created": 1, "data": [{"url": "https://example.invalid/img/1"}]})
            return
        if "/videos" in path:
            self._json({"id": "video_e2e", "object": "video", "status": "queued", "model": model})
            return
        if "generateContent" in path or "streamGenerateContent" in path or "countTokens" in path:
            if "e2e-native-real-" in path:
                is_gemini = path.startswith("/v1beta/")
                authenticated = self.headers.get("x-goog-api-key") == "sk-fake" if is_gemini else self.headers.get("Authorization") == "Bearer sk-fake"
                if not authenticated or "model" in req or not req.get("contents") or urlsplit(path).query not in ("", "alt=sse"):
                    self.send_error(400, "invalid Google native request")
                    return
            if "streamGenerateContent" in path:
                self._sse([{"candidates": [{"content": {"parts": [{"text": "e2e-ok"}]}, "finishReason": "STOP"}],
                            "usageMetadata": {"promptTokenCount": 8, "candidatesTokenCount": 2}}])
                return
            self._json({
                "candidates": [{"finishReason": "STOP", "content": {"parts": [{"text": "e2e-ok"}]}}],
                "usageMetadata": {"promptTokenCount": 8, "candidatesTokenCount": 2, "totalTokenCount": 10},
            })
            return
        if req.get("stream") is True:
            body = (
                b'data: {"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"content":"e2e-ok"}}]}\n\n'
                b'data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2}}\n\n'
                b"data: [DONE]\n\n"
            )
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        # 护栏 E2E 需要观察上游实际收到的请求内容，证明脱敏发生在
        # 数据面转发之前，而不是只有管理接口的试运行结果正确。
        message_text = ""
        for message in req.get("messages", []):
            if isinstance(message, dict) and isinstance(message.get("content"), str):
                message_text += message["content"]
        # 浏览器验收同时覆盖 UI 自定义替换文本和 API 默认替换文本；命中任一值都证明
        # 上游收到的是护栏处理后的正文，而不是仅有管理接口调试结果。
        if "[手机号已隐藏]" in message_text or "[REDACTED]" in message_text:
            self._json({
                "id": "chatcmpl_e2e_redacted",
                "object": "chat.completion",
                "model": model,
                "choices": [{"index": 0, "message": {"role": "assistant", "content": "upstream-saw-redacted"}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10},
            })
            return
        payload = {
            "id": "chatcmpl_e2e",
            "object": "chat.completion",
            "model": model,
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": "e2e-ok"},
                    "finish_reason": "stop",
                }
            ],
            "usage": {"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10},
        }
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _error_log_response(self, model: str) -> bool:
        """为错误日志 E2E 返回可辨识的上游失败；参数为请求模型，返回是否已处理。

        调用场景：数据面转发到本地上游后，在页面验证 JSON 长正文、纯文本正文或断网失败。
        副作用与边界：只匹配 e2e-error-log-* 测试模型；断网分支主动关闭当前连接，服务仍可处理后续请求。
        """
        if model.startswith("e2e-error-log-json-"):
            payload = {
                "error": {
                    "type": "upstream_gateway_error",
                    "code": "E2E_UPSTREAM_502",
                    "message": ERROR_LOG_JSON_DIAGNOSTIC,
                    "details": {
                        "upstream_request_id": "e2e-upstream-json-request",
                        "retryable": False,
                        "terminal_cause": "e2e-json-terminal-cause",
                    },
                }
            }
            self._response(502, json.dumps(payload).encode(), "application/json")
            return True
        if model.startswith("e2e-error-log-text-"):
            self._response(502, ERROR_LOG_TEXT_DIAGNOSTIC.encode(), "text/plain; charset=utf-8")
            return True
        if model.startswith("e2e-error-log-network-"):
            self.close_connection = True
            try:
                self.connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            self.connection.close()
            return True
        return False

    def _monitor_tps(self, req: dict) -> bool:
        """为 monitor-tps- 前缀的 Chat 测试型号提供长输入、短输出及可测生成区间。

        参数 req 为当前请求 JSON；返回是否已处理，非测试型号返回 False。
        同步和流式使用相同用量，流式两帧间隔 80ms；只写本地连接，不访问真实供应商，进程结束清理。
        """
        if not str(req.get("model", "")).startswith("monitor-tps-"):
            return False
        if not self.path.rstrip("/").endswith("/chat/completions"):
            self._response(400, b'{"error":"TPS fixture expects Chat Completions"}', "application/json")
            return True
        usage = {"prompt_tokens": 10000, "completion_tokens": 10, "total_tokens": 10010}
        if req.get("stream") is not True:
            time.sleep(0.03)
            self._json({"id": "chatcmpl-monitor", "object": "chat.completion", "choices": [{"index": 0, "message": {"role": "assistant", "content": "e2e-ok"}, "finish_reason": "stop"}], "usage": usage})
            return True
        first = b'data: {"id":"chatcmpl-monitor","choices":[{"index":0,"delta":{"content":"e2e-ok"}}]}\n\n'
        last = ("data: " + json.dumps({"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}], "usage": usage}) + "\n\ndata: [DONE]\n\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(first) + len(last)))
        self.end_headers()
        time.sleep(0.03)
        self.wfile.write(first)
        self.wfile.flush()
        time.sleep(0.08)
        self.wfile.write(last)
        self.wfile.flush()
        return True

    def _response(self, status: int, body: bytes, content_type: str) -> None:
        """写出指定 HTTP 响应；参数为状态码、完整正文和类型，无返回值。

        调用场景：错误日志测试需要保留非 200 上游的原始正文和 Content-Type。
        异常与副作用：直接写当前连接；调用方必须在写出后结束请求处理。
        """
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _sse(self, events: list[dict | str]) -> None:
        """输出本地原厂协议事件；参数为对象列表或 Chat 的原始 [DONE] 结束标记，无返回，E2E 转换调用，进程结束清理。"""
        body = "".join("data: " + (event if isinstance(event, str) else json.dumps(event)) + "\n\n" for event in events).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _diagnostic_headers(self, model: str) -> None:
        """为续接模型加入多值响应头；参数为模型，无返回，供本地 JSON/SSE 验证完整记录及脱敏。"""
        if model == "e2e-responses-history":
            self.send_header("X-Upstream-Trace", "trace-first")
            self.send_header("X-Upstream-Trace", "trace-second")
            self.send_header("Set-Cookie", "upstream-session=fixture-secret")

    def _json(self, payload: dict, diagnostic_model: str = "") -> None:
        """发送本地 JSON 响应；参数为正文和可选诊断模型，无返回，写入头及正文后结束响应。"""
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self._diagnostic_headers(diagnostic_model)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt: str, *args) -> None:
        return


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--port", type=int, default=4010)
    args = p.parse_args()
    HTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
