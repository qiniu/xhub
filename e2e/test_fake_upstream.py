"""本地 Ark 假上游的协议单元测试，不访问外部服务。"""
import json
import threading
import unittest
from unittest.mock import patch
from http.server import HTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen
from e2e.fake_upstream import Handler, ERROR_LOG_JSON_DIAGNOSTIC, ERROR_LOG_TEXT_DIAGNOSTIC, VISION_PNG, vision_answer


class ArkUpstreamTest(unittest.TestCase):
    def test_hard_budget_search(self):
        """本地HTTP验证搜索声明不被注入cap，返回完整同步/流计费快照；注入cap拒绝，tearDown回收服务。"""
        body = {"model": "budget-search-browser-unit", "input": "Search for Go.", "tools": [{"type": "web_search"}]}
        result = self.request("/v1/responses", body)
        self.assertEqual(result["output"][0]["action"]["type"], "search")
        with urlopen(Request(self.base + "/v1/responses", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
            self.assertIn(b"response.completed", response.read())
        for wrong in ({**body, "max_tool_calls": 2}, {**body, "tools": []}):
            with self.assertRaises(HTTPError) as error:
                self.request("/v1/responses", wrong)
            self.assertEqual(error.exception.code, 400)
            error.exception.close()

    def test_hard_budget_length(self):
        """私有本地 HTTP 验证 cap8 的 JSON/SSE 保留 incomplete 和完整用量，错误 cap/协议拒绝；tearDown 关闭服务。"""
        body = {"model": "pending-browser-hard-budget-length-unit", "input": "Verify normal output limit.", "max_output_tokens": 8}
        result = self.request("/v1/responses", body)
        self.assertEqual(result["status"], "incomplete")
        self.assertEqual(result["incomplete_details"], {"reason": "max_output_tokens"})
        self.assertEqual(result["usage"], {"input_tokens": 8, "output_tokens": 8})
        with urlopen(Request(self.base + "/v1/responses", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
            frames = [json.loads(line[6:]) for line in response.read().decode().splitlines() if line.startswith("data: {")]
            self.assertEqual(frames, [{"type": "response.incomplete", "response": result}])
        for path, cap in (("/v1/responses", 7), ("/v1/chat/completions", 8)):
            with self.assertRaises(HTTPError) as error:
                self.request(path, {**body, "max_output_tokens": cap})
            self.assertEqual(error.exception.code, 400)
            error.exception.close()

    def test_hard_budget_cache_projection(self):
        """本地同步Chat夹具报告含缓存的输入总量，供真实Messages桥接计费；错误目标400，tearDown回收HTTP服务。"""
        body = {"model": "pending-browser-hard-budget-projection", "messages": [{"role": "user", "content": "Verify cache projection."}]}
        response = self.request("/v1/chat/completions", body)
        self.assertEqual(response["usage"], {"prompt_tokens": 8, "completion_tokens": 2, "cache_write_tokens": 2, "prompt_tokens_details": {"cached_tokens": 2}})
        self.assertEqual(response["choices"][0]["message"]["content"], "e2e-budget-cache-projection")
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/messages", body)
        self.assertEqual(error.exception.code, 400)
        error.exception.close()

    def test_hard_budget_cache_usage(self):
        """本地HTTP覆盖缓存read/write矛盾、合法累计和非流拒绝；终态必须正常且原矛盾仍在中间帧，tearDown清理服务。"""
        for mode in ("write", "read", "growth"):
            with self.subTest(mode=mode):
                prompt = "Verify cumulative cache usage." if mode == "growth" else f"Return contradictory cache {mode} usage."
                body = {"model": "pending-browser-hard-budget-cache", "messages": [{"role": "user", "content": prompt}]}
                with self.assertRaises(HTTPError) as error:
                    self.request("/v1/chat/completions", body)
                self.assertEqual(error.exception.code, 400)
                error.exception.close()
                with urlopen(Request(self.base + "/v1/chat/completions", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
                    raw = response.read().decode()
                    frames = [json.loads(line[6:]) for line in raw.splitlines() if line.startswith("data: {")]
                    first = frames[0]["usage"]
                    self.assertEqual(first["cache_creation_input_tokens"], 0)
                    self.assertEqual(first["cache_write_tokens"], 50 if mode == "write" else 0)
                    self.assertEqual(first["cached_tokens"], 0)
                    self.assertEqual(first["prompt_tokens_details"]["cached_tokens"], 8 if mode == "read" else 0)
                    self.assertEqual(frames[-1]["usage"], {"prompt_tokens": 8, "completion_tokens": 2, "cache_write_tokens": 2, "cached_tokens": 2})
                    self.assertEqual(frames[-1]["choices"][0]["finish_reason"], "stop")
                    self.assertIn("[DONE]", raw)

    def test_hard_budget_conflicting_usage(self):
        """私有本地HTTP夹具验证冲突中间帧/正常终态并存，非流输入明确400；tearDown清理服务。"""
        body = {"model": "pending-browser-hard-budget-unit", "messages": [{"role": "user", "content": "Return contradictory usage."}]}
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/chat/completions", body)
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        with urlopen(Request(self.base + "/v1/chat/completions", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
            text = response.read().decode()
            self.assertIn('"input_tokens": 9', text)
            self.assertIn('"prompt_tokens": 8', text)
            self.assertIn('"finish_reason": "stop"', text)
            self.assertIn("[DONE]", text)

    def test_hard_budget_cumulative_usage(self):
        """本地HTTP夹具首帧等价别名为零、末帧单一别名增长；验证合法累计与非流失败，tearDown清理服务。"""
        body = {"model": "pending-browser-hard-budget-growth", "messages": [{"role": "user", "content": "Verify cumulative usage."}]}
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/chat/completions", body)
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        with urlopen(Request(self.base + "/v1/chat/completions", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
            frames = [json.loads(line[6:]) for line in response.read().decode().splitlines() if line.startswith("data: {")]
            self.assertEqual(frames[0]["usage"], {"prompt_tokens": 8, "input_tokens": 8, "completion_tokens": 0, "output_tokens": 0})
            self.assertEqual(frames[-1]["usage"], {"prompt_tokens": 8, "completion_tokens": 2})
            self.assertEqual(frames[0]["choices"][0]["delta"]["content"], "e2e-budget-growth")

    def test_monitor_tps_fixture(self):
        """前置真实本地上游；验证 TPS 夹具流式/同步用量一致、确有分帧间隔，非法协议返回 400；tearDown 关闭服务。"""
        import time
        body = {"model": "monitor-tps-probe", "messages": [{"role": "user", "content": "Verify output TPS."}]}
        result = self.request("/v1/chat/completions", body)
        self.assertEqual(result["usage"], {"prompt_tokens": 10000, "completion_tokens": 10, "total_tokens": 10010})
        with urlopen(Request(self.base + "/v1/chat/completions", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
            first = response.readline()
            start = time.monotonic()
            rest = response.read()
            self.assertGreaterEqual(time.monotonic() - start, 0.04)
            self.assertIn(b"e2e-ok", first)
            self.assertIn(b'"completion_tokens": 10', rest)
            self.assertIn(b"[DONE]", rest)
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/responses", body)
        self.assertEqual(error.exception.code, 400)
        error.exception.close()

    def test_vision_payload(self):
        """前置真实本地 HTTP 上游；图片字节正确才回答，错误图片/缺图必须失败，覆盖 JSON/SSE；tearDown 清理服务。"""
        image = {"type": "image_url", "image_url": {"url": "data:image/png;base64," + VISION_PNG}}
        body = {"model": "e2e-vision-chat", "messages": [{"role": "user", "content": [image]}]}
        self.assertEqual(self.request("/v1/chat/completions", body)["choices"][0]["message"]["content"], "green-pixel")
        with urlopen(Request(self.base + "/v1/chat/completions", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
            self.assertIn("green-pixel", response.read().decode())
        response_image = {"type": "input_image", "image_url": image["image_url"]["url"]}
        native = {"model": "e2e-vision-native", "input": [{"role": "user", "content": [response_image]}]}
        self.assertEqual(self.request("/v1/responses", native)["output"][0]["content"][0]["text"], "green-pixel")
        with urlopen(Request(self.base + "/v1/responses", data=json.dumps({**native, "stream": True}).encode()), timeout=2) as response:
            self.assertIn("response.completed", response.read().decode())
        for parts in ([], [{"type": "image_url", "image_url": {"url": "data:image/png;base64,changed"}}]):
            body["messages"][0]["content"] = parts
            with self.assertRaises(HTTPError) as error:
                self.request("/v1/chat/completions", body)
            self.assertEqual(error.exception.code, 400)
            error.exception.close()
        messages = {"messages": [{"content": [{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": VISION_PNG}}]}]}
        self.assertEqual(vision_answer(messages, "/v1/messages"), "green-pixel")
        messages["messages"][0]["content"][0]["source"]["media_type"] = "image/jpeg"
        with self.assertRaises(ValueError):
            vision_answer(messages, "/v1/messages")

    def test_budgeted_vision_messages_and_tool_result(self):
        """前置本地HTTP原生Messages；核对用户/工具图片的JSON与完整SSE、cap上下界及错误关联/MIME拒绝，tearDown清理服务。"""
        image = {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": VISION_PNG}}
        user_image = [{"role": "user", "content": [{"type": "text", "text": "Describe this image."}, image]}]
        tool_image = [
            {"role": "user", "content": "Read and describe the image."},
            {"role": "assistant", "content": [{"type": "tool_use", "id": "tool_image", "name": "read_image", "input": {}}]},
            {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "tool_image", "content": [{"type": "text", "text": "Image bytes:"}, image]}]},
        ]
        for messages, cap in ((user_image, 2), (tool_image, 8192)):
            body = {"model": "e2e-vision-budget-messages", "max_tokens": cap, "messages": messages}
            with self.subTest(tool_result=messages is tool_image):
                reply = self.request("/v1/messages", body)
                self.assertEqual(reply["content"], [{"type": "text", "text": "green-pixel"}])
                self.assertEqual(reply["usage"], {"input_tokens": 8, "output_tokens": 2})
                self.assertEqual(reply["stop_reason"], "end_turn")
                with urlopen(Request(self.base + "/v1/messages", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
                    frames = [json.loads(line[6:]) for line in response.read().decode().splitlines() if line.startswith("data: {")]
                self.assertEqual([frame["type"] for frame in frames], ["message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"])
                self.assertEqual(frames[0]["message"]["usage"], {"input_tokens": 8, "output_tokens": 0})
                self.assertEqual(frames[-2]["usage"], {"output_tokens": 2})
        bad_tool = json.loads(json.dumps(tool_image))
        bad_tool[-1]["content"][0]["tool_use_id"] = "not_called"
        bad_mime = json.loads(json.dumps(tool_image))
        bad_mime[-1]["content"][0]["content"][1]["source"]["media_type"] = "image/jpeg"
        invalid = [{"max_tokens": cap, "messages": user_image} for cap in (None, 0, 8193, True)]
        invalid.extend([{"max_tokens": 64, "messages": messages} for messages in (bad_tool, bad_mime, [])])
        for fields in invalid:
            with self.subTest(invalid=fields), self.assertRaises(HTTPError) as error:
                self.request("/v1/messages", {"model": "e2e-vision-budget-messages", **fields})
            self.assertEqual(error.exception.code, 400)
            error.exception.close()

    def test_budgeted_vision_requires_protocol_cap(self):
        """前置Chat/Responses图片夹具；验证各协议实际输出cap到达，缺失或错字段拒绝，tearDown回收本地HTTP服务。"""
        for path, field, body in (
            ("/v1/chat/completions", "max_tokens", {"messages": [{"role": "user", "content": [{"type": "image_url", "image_url": {"url": "data:image/png;base64," + VISION_PNG}}]}]}),
            ("/v1/responses", "max_output_tokens", {"input": [{"role": "user", "content": [{"type": "input_image", "image_url": "data:image/png;base64," + VISION_PNG}]}]}),
        ):
            body["model"] = "e2e-vision-budget-cap"
            self.request(path, {**body, field: 64})
            for fields in ({}, {"max_completion_tokens": 64}):
                with self.subTest(path=path, fields=fields), self.assertRaises(HTTPError) as error:
                    self.request(path, {**body, **fields})
                self.assertEqual(error.exception.code, 400)
                error.exception.close()

    def test_item_id_model_switch_fixture(self):
        """目的：验证 Chat源/严格原生目标夹具的成功、错误ID、协议及鉴权；前置本机服务，tearDown关闭，无真实凭据。"""
        source = self.request("/v1/chat/completions", {"model": "e2e-item-id-chat", "messages": []})
        self.assertTrue(source["id"].startswith("chatcmpl-"))
        history = [{"type": "message", "id": "msg_valid", "role": "assistant", "content": [{"type": "output_text", "text": "Hello"}]}]
        body = {"model": "e2e-item-id-native", "input": history}
        self.assertEqual(self.request("/v1/responses", body)["output"][0]["id"], "msg_e2e_native")
        with urlopen(Request(self.base + "/v1/responses", data=json.dumps({**body, "stream": True}).encode(), headers={"Authorization": "Bearer sk-fake"}), timeout=2) as response:
            self.assertIn("response.completed", response.read().decode())
        public = {"model": "e2e-item-id-native", "input": [{"type": "reasoning", "summary": []}]}
        self.assertEqual(self.request("/v1/responses", public)["status"], "completed")
        public["input"][0]["id"] = "rs_xhub_" + "a" * 32
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/responses", public)
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        # Grok 原生完整公开项是真实 HTTP 产物；只清理 status 或只清理 id 都仍需失败，两者规范后成功。
        grok = self.request("/v1/responses", {"model": "e2e-item-id-grok", "input": []})
        reasoning = grok["output"][0]
        self.assertEqual(reasoning["id"], "rs_92ae29c5-9e2c-9cbd-a94b-2fd2abc135be")
        for omit in ((), ("id",), ("status",)):
            invalid_public = {"model": "e2e-item-id-native", "input": [{k: v for k, v in reasoning.items() if k not in omit}]}
            with self.assertRaises(HTTPError) as error:
                self.request("/v1/responses", invalid_public)
            self.assertEqual(error.exception.code, 400)
            error.exception.close()
        normalized = {"type": "reasoning", "summary": reasoning["summary"]}
        self.assertEqual(self.request("/v1/responses", {"model": "e2e-item-id-native", "input": [normalized]})["status"], "completed")
        tracked = self.request("/v1/responses", {"model": "e2e-item-id-tracked", "input": []})["output"][0]
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/responses", {"model": "e2e-item-id-native", "input": [tracked]})
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        for key in ("internal_chat_message_metadata_passthrough", "metadata"):
            tracked.pop(key)
        self.assertEqual(self.request("/v1/responses", {"model": "e2e-item-id-native", "input": [tracked]})["status"], "completed")
        tool_source = self.request("/v1/chat/completions", {"model": "e2e-item-id-chat", "tools": [{"type": "function", "function": {"name": "lookup_value"}}]})
        self.assertEqual(tool_source["choices"][0]["message"]["tool_calls"][0]["id"], "call-e2e")
        function = {"type": "function_call", "id": "call-e2e", "call_id": "call-e2e", "name": "lookup_value", "arguments": "{}"}
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/responses", {"model": "e2e-item-id-native", "input": [function]})
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        function["id"] = "fc_xhub_" + "a" * 32
        self.assertEqual(self.request("/v1/responses", {"model": "e2e-item-id-native", "input": [function]})["status"], "completed")
        history[0]["id"] = source["id"] + "_message"
        for path, invalid, key in [("/v1/responses", body, "Bearer sk-fake"), ("/v1/responses", {"model": "e2e-item-id-chat"}, "Bearer sk-fake"), ("/v1/chat/completions", {"model": "e2e-item-id-chat"}, "Bearer invalid")]:
            with self.assertRaises(HTTPError) as error:
                self.request(path, invalid, key)
            self.assertIn(error.exception.code, (400, 401))
            error.exception.close()

    def test_client_search_fixture(self):
        """目的：验证发现→加载→执行结果三轮及错误路径；前置本机夹具，断言真实 JSON/SSE 返回，tearDown 关闭服务，无供应商调用。"""
        search = {"type": "function", "function": {"name": "tool_search", "parameters": {"type": "object"}}}
        read = {"type": "function", "function": {"name": "fs__read", "parameters": {"type": "object"}}}
        body = {"model": "e2e-client-tool-search", "messages": [{"role": "user", "content": "Discover the file tool."}], "tools": [search]}
        first = self.request("/v1/chat/completions", body)
        self.assertEqual(first["choices"][0]["message"]["tool_calls"][0]["function"]["name"], "tool_search")
        body["messages"].append({"role": "tool", "tool_call_id": "search_1", "content": "Loaded read"})
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/chat/completions", body)
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        body["tools"].append(read)
        second = self.request("/v1/chat/completions", body)
        self.assertEqual(json.loads(second["choices"][0]["message"]["tool_calls"][0]["function"]["arguments"]), {"input": "Read the file."})
        body["messages"].append({"role": "tool", "tool_call_id": "read_1", "content": "file-value"})
        self.assertEqual(self.request("/v1/chat/completions", body)["choices"][0]["message"]["content"], "e2e-ok")
        with urlopen(Request(self.base + "/v1/chat/completions", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
            self.assertTrue(response.read().decode().endswith("data: [DONE]\n\n"))

    def test_chat_reasoning_fixture(self):
        """目的：Chat 专属密文夹具保留原厂 ID 和终态密文，支持普通历史并拒绝错误身份；前置本地服务，tearDown 清理线程，不访问供应商。"""
        body = {"model": "e2e-agent-chat-reasoning", "input": [{"role": "user", "content": "Read the file."}], "tools": [{"name": "read"}]}
        first = self.request("/v1/responses", body)
        reasoning = first["output"][0]
        self.assertEqual(reasoning["id"], "rs_public")
        history = [*body["input"], reasoning, first["output"][1], {"type": "function_call_output", "call_id": "call_1", "output": "file-value"}]
        self.assertEqual(self.request("/v1/responses", {**body, "input": history})["output"][1]["content"][0]["text"], "e2e-ok")
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/responses", {**body, "input": [*history[:1], {**reasoning, "id": "wrong-id"}, *history[2:]]})
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        plain = {"type": "reasoning", "summary": [{"type": "summary_text", "text": "Plan"}]}
        self.assertEqual(self.request("/v1/responses", {**body, "input": [*history[:1], plain, *history[2:]]})["output"][1]["content"][0]["text"], "e2e-ok")

    def test_portable_history_encryption_boundaries(self):
        """目的：严格区分外部与网关账户密文并保留公开工具往返；前置本地 HTTP，验证正常/失败/流式，tearDown 清理服务。"""
        source = {"type": "reasoning", "id": "rs_outside", "summary": [{"type": "summary_text", "text": "Public plan"}], "encrypted_content": "outside-cipher"}
        for model in ("e2e-portable-gpt", "e2e-portable-grok", "e2e-portable-deepseek"):
            with self.assertRaises(HTTPError) as error:
                self.request("/v1/responses", {"model": model, "input": [source]})
            self.assertEqual(error.exception.code, 400)
            error.exception.close()
            result = self.request("/v1/responses", {"model": model, "input": [{"role": "user", "content": "Hello"}]})
            self.assertEqual(result["output"][0]["encrypted_content"], "gateway-cipher")
        self.assertEqual(self.request("/v1/responses", {"model": "e2e-portable-outside", "input": [source]})["status"], "completed")
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/responses", {"model": "e2e-portable-outside", "input": result["output"]})
        self.assertEqual(error.exception.code, 400)
        error.exception.close()
        first = self.request("/v1/responses", {"model": "e2e-portable-gpt", "input": "Read", "tools": [{"type": "function", "name": "read"}]})
        self.assertEqual(first["output"][1]["call_id"], "call_portable")
        public = [{"type": "reasoning", "summary": source["summary"]}, first["output"][1], {"type": "function_call_output", "call_id": "call_portable", "output": "file-value"}]
        second = self.request("/v1/responses", {"model": "e2e-portable-gpt", "input": public})
        self.assertEqual(second["output"][1]["content"][0]["text"], "portable-ok")
        with urlopen(Request(self.base + "/v1/responses", data=json.dumps({"model": "e2e-portable-gpt", "input": "Hello", "stream": True}).encode()), timeout=2) as response:
            self.assertIn('"response.completed"', response.read().decode())

    def test_slow_agent_proxy_fixture(self):
        """目的：仅专属模型触发超时边界等待并返回正常用量；前置本地服务，单元层替换等待避免耗时，浏览器层实际等待31秒，tearDown关闭服务。"""
        with patch("e2e.fake_upstream.time.sleep") as wait:
            result = self.request("/v1/chat/completions", {"model": "e2e-agent-proxy-slow", "messages": [{"role": "user", "content": "Reply OK."}]})
            wait.assert_called_once_with(31)
            self.assertEqual(result["choices"][0]["message"]["content"], "e2e-ok")
        with patch("e2e.fake_upstream.time.sleep") as wait:
            self.request("/v1/chat/completions", {"model": "normal-model"})
            wait.assert_not_called()

    def test_agent_chat_tool_fixture(self):
        """目的：Chat 专属夹具校验 namespace、推理和 custom 包装，并拒绝原生字段；前置随机端口本地服务，tearDown 关闭线程，不访问供应商。"""
        for custom in (False, True):
            parameters = {"type": "object", "properties": {"content": {"type": "string"}} if custom else {}}
            body = {"model": "e2e-agent-chat-tools", "messages": [{"role": "user", "content": "Read the file."}], "reasoning_effort": "medium", "tools": [{"type": "function", "function": {"name": "fs__read", "parameters": parameters}}]}
            first = self.request("/v1/chat/completions", body)
            call = first["choices"][0]["message"]["tool_calls"][0]
            self.assertEqual(call["function"]["name"], "fs__read")
            self.assertEqual(json.loads(call["function"]["arguments"]), {"content": "Read the file."} if custom else {})
            with urlopen(Request(self.base + "/v1/chat/completions", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
                raw = response.read().decode()
                self.assertIn('"choices": []', raw)
                self.assertTrue(raw.endswith("data: [DONE]\n\n"))
            for invalid in ({"include": ["reasoning.encrypted_content"]}, {"reasoning_effort": "lost"}):
                with self.assertRaises(HTTPError) as error:
                    self.request("/v1/chat/completions", {**body, **invalid})
                self.assertEqual(error.exception.code, 400)
                error.exception.close()

    def test_messages_public_reasoning_fixture(self):
        """目的：公开推理夹具返回独立推理、函数调用和正确 SSE 终态，坏续轮400；前置本地随机端口服务，tearDown 回收服务和状态，不依赖外部供应商。"""
        for protocol in ("responses", "chat/completions"):
            is_responses = protocol == "responses"
            key = "input" if is_responses else "messages"
            body = {"model": "e2e-agent-messages", key: [{"role": "user", "content": "Read the file."}], "tools": [{"name": "read"}]}
            first = self.request("/v1/" + protocol, body)
            if is_responses:
                self.assertEqual(first["output"][0]["summary"][0]["text"], "Plan")
                self.assertEqual(first["output"][1]["call_id"], "call_1")
            else:
                self.assertEqual(first["choices"][0]["message"]["reasoning_content"], "Plan")
                self.assertEqual(first["choices"][0]["message"]["tool_calls"][0]["id"], "call_1")
            data = json.dumps({**body, "stream": True}).encode()
            with urlopen(Request(self.base + "/v1/" + protocol, data=data), timeout=2) as response:
                raw = response.read().decode()
                self.assertIn("Plan", raw)
                self.assertIn("response.completed" if is_responses else "data: [DONE]\n\n", raw)
                self.assertNotIn('data: "[DONE]"', raw)
            bad_history = {"type": "function_call_output", "call_id": "call_1", "output": "file-value"} if is_responses else {"role": "tool", "tool_call_id": "call_1", "content": "file-value"}
            with self.assertRaises(HTTPError) as error:
                self.request("/v1/" + protocol, {**body, key: [bad_history]})
            self.assertEqual(error.exception.code, 400)
            error.exception.close()

    def test_messages_encrypted_reasoning_fixture(self):
        """目的：密文夹具要求原文往返且不伪造 ID，覆盖公开摘要和无摘要；前置本地随机端口服务，tearDown 回收，不调用外部供应商。"""
        for suffix in ("encrypted", "redacted"):
            body = {"model": "e2e-agent-messages-" + suffix, "input": [{"role": "user", "content": "Read the file."}], "tools": [{"name": "read"}]}
            first = self.request("/v1/responses", body)
            reasoning = dict(first["output"][0])
            self.assertEqual(reasoning["encrypted_content"], "fixture-encrypted")
            with urlopen(Request(self.base + "/v1/responses", data=json.dumps({**body, "stream": True}).encode()), timeout=2) as response:
                events = [json.loads(line[5:]) for line in response.read().decode().splitlines() if line.startswith("data:")]
            done = next(event["item"] for event in events if event["type"] == "response.output_item.done")
            final = events[-1]["response"]["output"][0]
            self.assertEqual(done["summary"], final["summary"])
            self.assertEqual(done["encrypted_content"], "fixture-encrypted")
            self.assertEqual(final["encrypted_content"], "fixture-encrypted-terminal")
            if suffix == "redacted":
                self.assertEqual(reasoning["summary"], [])
            del reasoning["id"]
            history = [*body["input"], reasoning, first["output"][1], {"type": "function_call_output", "call_id": "call_1", "output": "file-value"}]
            second = self.request("/v1/responses", {**body, "input": history})
            self.assertEqual(second["output"][1]["content"][0]["text"], "e2e-ok")
            bad_history = [item for item in history if item.get("type") != "reasoning"]
            with self.assertRaises(HTTPError) as error:
                self.request("/v1/responses", {**body, "input": bad_history})
            self.assertEqual(error.exception.code, 400)
            error.exception.close()

    def test_permission_probe_counts_success_and_failure(self):
        """目的：权限探针读操作不增加计数，成功和失败推理均增加；前置本地服务，tearDown 回收线程及状态。"""
        before = self.request("/e2e/request-count")["requests"]
        self.assertEqual(self.request("/e2e/request-count")["requests"], before)
        self.request("/v1/chat/completions", {"model":"normal-model"})
        with self.assertRaises(HTTPError):
            self.request("/v1/chat/completions", {"model":"e2e-fallback-429-probe"})
        self.assertEqual(self.request("/e2e/request-count")["requests"], before + 2)

    def test_native_image_size_and_preview(self):
        """前置本地图片服务；验证空尺寸和渠道不支持尺寸400、合法尺寸返回真实 PNG 与用量、缺鉴权401；tearDown关闭服务清理。"""
        body = {"model": "e2e-native-real-image_generation", "prompt": "hello", "n": 1}
        for size in (None, "", "2048x2048"):
            with self.assertRaises(HTTPError) as error:
                self.request("/v1/images/generations", {**body, "size": size})
            self.assertEqual(error.exception.code, 400)
        result = self.request("/v1/images/generations", {**body, "size": "1024x1024"})
        import base64
        self.assertTrue(base64.b64decode(result["data"][0]["b64_json"]).startswith(bytes([137, 80, 78, 71])))
        self.assertEqual(result["usage"], {"input_tokens": 8, "output_tokens": 2})
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/images/generations", {**body, "size": "1024x1024"}, key="")
        self.assertEqual(error.exception.code, 401)

    def test_fallback_rate_limit_is_scoped_to_test_models(self):
        """目的：为浏览器回退流程提供可控429；前置本地HTTP服务，验证故障型号返回限流码、普通型号正常及相邻前缀不误伤，tearDown关闭服务清理。"""
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/chat/completions", {"model": "e2e-fallback-429-case"})
        self.assertEqual(error.exception.code, 429)
        self.assertEqual(json.load(error.exception)["error"]["code"], "rate_limit_exceeded")
        for model in ("normal-model", "e2e-fallback-429"):
            response = self.request("/v1/chat/completions", {"model": model})
            self.assertEqual(response["model"], model)

    def test_continuation_diagnostics(self):
        """前置本地HTTP夹具，验证三轮缓存统计、多值头与坏历史400；tearDown关闭服务清理数据。"""
        messages = []
        for turn in range(1, 4):
            messages.append({"role": "user", "content": f"continuation-{turn}"})
            data = json.dumps({"model": "e2e-responses-history", "messages": messages}).encode()
            with urlopen(Request(self.base + "/v1/chat/completions", data=data), timeout=2) as response:
                self.assertEqual(response.headers.get_all("X-Upstream-Trace"), ["trace-first", "trace-second"])
                usage = json.load(response)["usage"]
                self.assertEqual(usage["prompt_tokens_details"]["cached_tokens"], 0 if turn == 1 else 4)
                self.assertEqual(usage["provider_statistics"]["turn"], turn)
            messages.append({"role": "assistant", "content": f"history-ok-{turn}"})
        with self.assertRaises(HTTPError) as error:
            self.request("/v1/chat/completions", {"model": "e2e-responses-history", "messages": []})
        self.assertEqual(error.exception.code, 400)

    def test_error_log_diagnostics(self):
        """前置本地 HTTP 上游；验证长 JSON、纯文本无截断，普通模型仍成功，断网可恢复；tearDown 关闭服务并清理任务。"""
        for kind in ("json", "text"):
            with self.subTest(kind=kind), self.assertRaises(HTTPError) as error:
                self.request("/v1/chat/completions", {"model": "e2e-error-log-" + kind + "-unit", "messages": []})
            with error.exception as response:
                self.assertEqual(response.code, 502)
                body = response.read().decode()
                if kind == "json":
                    details = json.loads(body)["error"]
                    self.assertEqual(details["message"], ERROR_LOG_JSON_DIAGNOSTIC)
                    self.assertEqual(details["details"]["terminal_cause"], "e2e-json-terminal-cause")
                else:
                    self.assertEqual(body, ERROR_LOG_TEXT_DIAGNOSTIC)
        from http.client import RemoteDisconnected
        with self.assertRaises(RemoteDisconnected):
            self.request("/v1/chat/completions", {"model": "e2e-error-log-network-unit", "messages": []})
        normal = self.request("/v1/chat/completions", {"model": "e2e-error-log-success-unit", "messages": []})
        self.assertEqual(normal["choices"][0]["message"]["content"], "e2e-ok")

    def setUp(self):
        """启动随机端口本地 HTTPServer；无参数/返回，供每个用例使用，tearDown 清理。"""
        Handler.ark_tasks.clear()
        self.server = HTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()
        self.base = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        """结束本用例服务器、线程及任务；无参数/返回，不留下测试数据。"""
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        Handler.ark_tasks.clear()

    def request(self, path, body=None, key="Bearer sk-fake"):
        """向本地路径发 JSON 请求；body 决定 POST/GET，key 为鉴权。

        返回解码对象，错误状态抛 HTTPError；供本类测试使用，响应上下文自动关闭。
        """
        data = None if body is None else json.dumps(body).encode()
        with urlopen(Request(self.base + path, data=data, headers={"Authorization": key}), timeout=2) as response:
            return json.load(response)

    def test_task_failure(self):
        """前置本地上游；失败夹具先运行后失败，验证错误终态；tearDown 清理服务器与任务。"""
        path = "/api/v3/contents/generations/tasks"
        created = self.request(path, {"model": "doubao-seedance-2-0-260128", "content": [{"type": "text", "text": "e2e-task-failure"}]})
        task_path = path + "/" + created["id"]
        self.assertEqual(self.request(task_path)["status"], "running")
        self.assertEqual(self.request(task_path)["status"], "failed")

    def test_codex_model_prefix_and_history(self):
        """目的：Codex 模型带协议前缀仍必须验证历史；前置本地 HTTP 上游，验证三轮独立ID、正确标记与坏历史400，tearDown清理服务。"""
        first = "Remember the project marker project-alpha. Reply with only that marker."
        prompts = [first, "What project marker did I give you? Reply with only the marker.",
                   "Confirm the same project marker once more. Reply with only the marker."]
        for model in ("e2e-codex-agent", "openai/e2e-codex-agent"):
            messages = [{"role": "system", "content": "You are Codex."}]
            ids = set()
            for prompt in prompts:
                messages.append({"role": "user", "content": prompt})
                answer = self.request("/v1/chat/completions", {"model": model, "messages": messages})
                self.assertEqual(answer["choices"][0]["message"]["content"], "project-alpha")
                self.assertNotIn(answer["id"], ids)
                ids.add(answer["id"])
                messages.append({"role": "assistant", "content": "project-alpha"})
            with self.assertRaises(HTTPError) as error:
                self.request("/v1/chat/completions", {"model": model, "messages": messages[-2:-1]})
            self.assertEqual(error.exception.code, 400)

    def test_task_lifecycle(self):
        """前置本地上游；标记任务先运行再完成，重复查询维持完成；tearDown 清理服务器和任务。"""
        path = "/api/v3/contents/generations/tasks"
        created = self.request(path, {"model": "doubao-seedance-2-0-260128", "content": [{"type": "text", "text": "e2e-task-lifecycle"}]})
        task_path = path + "/" + created["id"]
        self.assertEqual(self.request(task_path)["status"], "running")
        self.assertEqual(self.request(task_path)["status"], "succeeded")
        self.assertEqual(self.request(task_path)["status"], "succeeded")

    def test_create_and_poll(self):
        """验证合法创建、唯一任务 ID、完成结果及用量；使用本地服务，tearDown 清理。"""
        path = "/api/v3/contents/generations/tasks"
        first = self.request(path, {"model":"doubao-seedance-2-0-260128","content":[{"type":"text","text":"apple"}]})
        second = self.request(path, {"model":"doubao-seedance-2-0-260128","content":[{"type":"text","text":"apple"}]})
        self.assertNotEqual(first["id"], second["id"])
        result = "/api/v3/contents/generations/tasks/" + first["id"]
        for suffix in ("", ""):
            doc = self.request(result + suffix)
            self.assertEqual(doc["status"], "succeeded")
            self.assertEqual(doc["usage"]["completion_tokens"], 100)

    def test_invalid_auth_body_and_unknown_task(self):
        """验证错误鉴权、网关字段泄漏、空输入和未知任务分别拒绝；tearDown 清理服务。"""
        root = "/api/v3/contents/generations/tasks"
        for path, body, key, status in [
            (root, {"model":"m","content":[{}]}, "Key sk-fake", 401),
            (root, {"content":[{}]}, "Bearer sk-fake", 400),
            (root, {}, "Bearer sk-fake", 400),
            (root + "/missing", None, "Bearer sk-fake", 404),
        ]:
            with self.subTest(path=path, body=body, key=key):
                with self.assertRaises(HTTPError) as error:
                    self.request(path, body, key)
                self.assertEqual(error.exception.code, status)
                error.exception.close()

    def test_existing_catalog(self):
        """验证非 Ark 的目录路径仍返回完整旧目录；本地服务与内存任务由 tearDown 清理。"""
        self.assertEqual(len(self.request("/v1/models")["data"]), 155)
