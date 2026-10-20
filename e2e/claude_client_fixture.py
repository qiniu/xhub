"""Claude 客户端浏览器夹具；只处理专属离线模型，验证实际出站契约。"""


def claude_client_payload(request):
    """检查 Messages 输出上限及无操作上下文；返回 JSON 与完整 SSE，无副作用，失败抛 ValueError。"""
    if not isinstance(request.get("max_tokens"), int) or request["max_tokens"] <= 0:
        raise ValueError("missing real output cap")
    if "context_management" in request and request["context_management"] != {"edits": [{"type": "clear_thinking_20251015", "keep": "all"}]}:
        raise ValueError("context edits changed")
    if any(key in request for key in ("prompt_cache_key", "parallel_tool_calls", "reasoning", "text")):
        raise ValueError("OpenAI options were not translated")
    if any("strict" in tool for tool in request.get("tools", [])):
        raise ValueError("strict=false leaked to Anthropic")
    payload = {"id": "msg_compat", "type": "message", "role": "assistant",
               "content": [{"type": "text", "text": "CLAUDE_COMPAT_OK"}], "stop_reason": "end_turn",
               "usage": {"input_tokens": 8, "output_tokens": 2}}
    if request.get("messages") == [{"role": "user", "content": "Verify automatic cache."}]:
        # 模拟 Fenno 在未声明 cache_control 时自动报告两种 TTL；不能靠修改用户请求通过验证。
        if "cache_control" in request:
            raise ValueError("unexpected cache directive")
        payload["usage"].update(cache_creation_input_tokens=5, cache_read_input_tokens=0,
                                cache_creation={"ephemeral_5m_input_tokens": 2, "ephemeral_1h_input_tokens": 3})
    events = [
        {"type": "message_start", "message": {"id": "msg_compat", "type": "message", "role": "assistant", "content": [], "usage": {"input_tokens": 8, "output_tokens": 0}}},
        {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}},
        {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "CLAUDE_COMPAT_OK"}},
        {"type": "content_block_stop", "index": 0},
        {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"output_tokens": 2}},
        {"type": "message_stop"},
    ]
    events[0]["message"]["usage"] = {**payload["usage"], "output_tokens": 0}
    return payload, events


def serve_claude_client(handler, request):
    """专属型号验证认证和路径后输出真实 HTTP JSON/SSE；返回是否处理，其他型号不产生副作用。"""
    if request.get("model") != "e2e-claude-client":
        return False
    if handler.headers.get("x-api-key") != "sk-fake" and handler.headers.get("Authorization") != "Bearer sk-fake":
        handler.send_error(401)
        return True
    try:
        if not handler.path.endswith("/messages"):
            raise ValueError("expected native Messages upstream")
        payload, events = claude_client_payload(request)
    except ValueError as exc:
        handler.send_error(400, str(exc))
        return True
    if request.get("stream"):
        handler._sse(events)
    else:
        handler._json(payload)
    return True
