"""工具发现兼容夹具：验证真实出站正文，所有内容均为隔离测试合成数据。"""


def reference_messages(history):
    """按 history 生成新会话或完整工具发现历史，返回独立列表；测试与严格上游调用，无副作用。"""
    messages = [{"role": "user", "content": "Find and run lookup."}]
    if history:
        messages.extend([
            {"role": "assistant", "content": [{"type": "tool_use", "id": "search-1", "name": "ToolSearch", "input": {"query": "lookup"}}]},
            {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "search-1", "content": [
                {"type": "tool_reference", "tool_name": "lookup"}, {"type": "text", "text": "保留用户原文"}]}]},
        ])
    return messages


def reference_payload(request):
    """校验 Messages 新/旧会话的原样字段，返回 JSON 和 SSE；字段丢失或变形抛 ValueError，不访问外部服务。"""
    if request.get("output_config") != {"effort": "high", "future_option": True}:
        raise ValueError("output_config lost or changed")
    if request.get("tools") != [{"name": "ToolSearch", "input_schema": {"type": "object"}}, {"name": "lookup", "input_schema": {"type": "object"}, "defer_loading": True}]:
        raise ValueError("deferred tool lost or changed")
    if request.get("max_tokens") != 64:
        raise ValueError("max_tokens changed")
    if request.get("messages") not in (reference_messages(False), reference_messages(True)):
        raise ValueError("tool references, order or user text changed")
    payload = {"id": "msg_reference", "type": "message", "role": "assistant", "model": request["model"],
               "content": [{"type": "text", "text": "REFERENCE_OK"}], "stop_reason": "end_turn",
               "usage": {"input_tokens": 8, "output_tokens": 2}}
    events = [
        {"type": "message_start", "message": {**payload, "content": [], "stop_reason": None, "usage": {"input_tokens": 8, "output_tokens": 0}}},
        {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}},
        {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "REFERENCE_OK"}},
        {"type": "content_block_stop", "index": 0},
        {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"output_tokens": 2}},
        {"type": "message_stop"},
    ]
    return payload, events


def serve_reference_history(handler, request):
    """接管专属模型的 HTTP 请求并返回是否处理；认证/格式错误可见，非专属模型无副作用，进程结束无需清理状态。"""
    if request.get("model") != "e2e-claude-reference":
        return False
    if handler.headers.get("x-api-key") != "sk-fake" and handler.headers.get("Authorization") != "Bearer sk-fake":
        handler.send_error(401)
        return True
    try:
        if not handler.path.endswith("/messages"):
            raise ValueError("native Messages path required")
        payload, events = reference_payload(request)
    except (ValueError, TypeError, AttributeError) as exc:
        handler.send_error(400, str(exc))
        return True
    if request.get("stream"):
        handler._sse(events)
    else:
        handler._json(payload)
    return True
