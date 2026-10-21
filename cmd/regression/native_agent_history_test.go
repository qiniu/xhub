package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// historyHarness 为协议回归建立真实管理员、用户Key、凭据和部署；参数为协议及本地HTTP处理器，返回测试上下文。
// 私有 schema 由 newHarness 回收；上游只返回合成内容，无供应商费用或生产数据。
func historyHarness(t *testing.T, protocol string, handler http.Handler) (*harness, chained, string, string) {
	t.Helper()
	up := httptest.NewServer(handler)
	t.Cleanup(up.Close)
	h := newHarness(t)
	admin := h.adminSession()
	h.ok("POST", "/credentials", admin, map[string]any{"credential_name": "history-credential", "credential_info": map[string]any{"custom_llm_provider": "openai"}, "credential_values": map[string]any{"api_base": up.URL + "/v1", "api_key": "local-key"}})
	dep := chatDeployment("history-model")
	delete(dep.LiteLLMParams, "api_key")
	dep.LiteLLMParams["litellm_credential_name"] = "history-credential"
	dep.ModelInfo["transport"] = "bypass_openai_chat"
	if protocol == "messages" {
		dep.ModelInfo["transport"] = "bypass_anthropic_messages"
	}
	dep.ModelInfo["endpoint_types"] = []string{"chat", "responses", "messages"}
	dep.ModelInfo["pricing_source"] = "manual"
	created := h.ok("POST", "/model/new", admin, map[string]any{"model_name": dep.ModelName, "litellm_params": dep.LiteLLMParams, "model_info": dep.ModelInfo}).json()
	return h, h.openScope(t, admin, "history-owner"), admin, created["model_info"].(map[string]any)["id"].(string)
}

// historyMessagesReply 按合成完整内容发送合法原生 JSON/SSE；参数为写入器、内容和流开关，无返回值。
// 思考签名分片、工具caller和usage都来自明确样本，模拟真实上游协议，不修改网关或数据库。
func historyMessagesReply(w http.ResponseWriter, content []any, stream bool) {
	stop := "end_turn"
	for _, x := range content {
		if x.(map[string]any)["type"] == "tool_use" {
			stop = "tool_use"
		}
	}
	response := map[string]any{"id": "msg_history", "type": "message", "role": "assistant", "model": "private", "content": content, "stop_reason": stop, "usage": map[string]any{"input_tokens": 8, "output_tokens": 2}}
	if !stream {
		writeJSON(w, response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(v any) { b, _ := json.Marshal(v); fmt.Fprintf(w, "data: %s\n\n", b) }
	emit(map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_history", "type": "message", "role": "assistant", "model": "private", "content": []any{}, "usage": map[string]any{"input_tokens": 8}}})
	for i, x := range content {
		block := x.(map[string]any)
		start := map[string]any{}
		for k, v := range block {
			start[k] = v
		}
		var delta map[string]any
		switch block["type"] {
		case "thinking":
			start["thinking"] = ""
			delete(start, "signature")
			delta = map[string]any{"type": "thinking_delta", "thinking": block["thinking"]}
		case "tool_use":
			start["input"] = map[string]any{}
			b, _ := json.Marshal(block["input"])
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(b)}
		}
		emit(map[string]any{"type": "content_block_start", "index": i, "content_block": start})
		if delta != nil {
			emit(map[string]any{"type": "content_block_delta", "index": i, "delta": delta})
		}
		if block["type"] == "thinking" {
			emit(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "signature_delta", "signature": block["signature"]}})
		}
		emit(map[string]any{"type": "content_block_stop", "index": i})
	}
	emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 2}})
	emit(map[string]any{"type": "message_stop"})
}

// historyReadMessages 从真实网关回复重建客户端将原样回传的内容块；参数为回复和流开关，返回完整历史。
// 仅消费客户端实际收到的JSON/SSE，不删除字段或补造签名；解析错误令当前测试失败，无外部副作用。
func historyReadMessages(t *testing.T, r reply, stream bool) []any {
	t.Helper()
	if !stream {
		return r.json()["content"].([]any)
	}
	blocks := []any{}
	args := map[int]string{}
	for _, line := range strings.Split(r.text(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var e map[string]any
		if json.Unmarshal([]byte(line[6:]), &e) != nil {
			t.Fatal("非法SSE")
		}
		index := int(asIntAny(e["index"]))
		switch e["type"] {
		case "content_block_start":
			blocks = append(blocks, e["content_block"])
		case "content_block_delta":
			b := blocks[index].(map[string]any)
			d := e["delta"].(map[string]any)
			switch d["type"] {
			case "thinking_delta":
				b["thinking"] = fmt.Sprint(b["thinking"]) + d["thinking"].(string)
			case "signature_delta":
				old, _ := b["signature"].(string)
				b["signature"] = old + d["signature"].(string)
			case "input_json_delta":
				args[index] += d["partial_json"].(string)
			}
		case "content_block_stop":
			if raw := args[index]; raw != "" {
				var a map[string]any
				if json.Unmarshal([]byte(raw), &a) != nil {
					t.Fatal("非法工具参数")
				}
				blocks[index].(map[string]any)["input"] = a
			}
		}
	}
	return blocks
}

// TestNativeMessagesAgentHistory 验证原生思考/密文和caller在标准入口JSON/SSE完整工具续轮、账单、模型改名、权限及上游验签失败。
// 参数 t 为上下文；真实HTTP/私有数据库/用户Key，所有上游内容为合成；harness清理模型、凭据、用户和账单。
func TestNativeMessagesAgentHistory(t *testing.T) {
	for _, kind := range []string{"thinking", "redacted_thinking"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", kind, stream), func(t *testing.T) {
				block := map[string]any{"type": "thinking", "thinking": "Plan", "signature": "native-signature"}
				if kind == "redacted_thinking" {
					block = map[string]any{"type": kind, "data": "native-cipher"}
				}
				content := []any{block, map[string]any{"type": "tool_use", "id": "call_history", "name": "lookup", "input": map[string]any{"path": "a"}, "caller": map[string]any{"type": "direct"}}}
				var mu sync.Mutex
				var calls []map[string]any
				up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					mu.Lock()
					calls = append(calls, body)
					mu.Unlock()
					if r.Header.Get("x-api-key") == "changed-key" {
						w.WriteHeader(400)
						writeJSON(w, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": "signature rejected by upstream"}})
						return
					}
					raw, _ := json.Marshal(body)
					if strings.Contains(string(raw), "tool_result") {
						messages := body["messages"].([]any)
						if !reflect.DeepEqual(messages[1].(map[string]any)["content"], content) {
							t.Errorf("原生签名/工具历史改变: %v", messages)
						}
						historyMessagesReply(w, []any{map[string]any{"type": "text", "text": "history-ok"}}, body["stream"] == true)
					} else {
						historyMessagesReply(w, content, body["stream"] == true)
					}
				})
				h, owner, admin, id := historyHarness(t, "messages", up)
				headers := map[string]string{"X-Session-Id": "native-history-session"}
				base := []any{map[string]any{"role": "user", "content": "Read the file."}}
				first := h.doHeaders("POST", "/v1/messages", owner.key, map[string]any{"model": "history-model", "messages": base, "max_tokens": 64, "stream": stream}, headers)
				if first.status != 200 {
					t.Fatal(first.describe())
				}
				actual := historyReadMessages(t, first, stream)
				if !reflect.DeepEqual(actual, content) {
					t.Fatalf("首轮原生内容失真: %v", actual)
				}
				body := map[string]any{"model": "history-model", "messages": append(base, map[string]any{"role": "assistant", "content": actual}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_history", "content": "File data"}}}), "max_tokens": 64, "stream": stream}
				second := h.doHeaders("POST", "/v1/messages", owner.key, body, headers)
				if second.status != 200 || !strings.Contains(second.text(), "history-ok") {
					t.Fatal(second.describe())
				}
				h.flushSpend()
				for _, r := range []reply{first, second} {
					bill := h.ok("GET", "/spend/logs/ui/"+r.header("x-litellm-call-id"), admin, nil).json()
					if bill["status"] != "success" || bill["api_key"] != owner.keyID || asIntAny(bill["prompt_tokens"]) != 8 || asIntAny(bill["completion_tokens"]) != 2 {
						t.Fatalf("原生思考账单不符: %v", bill)
					}
				}
				// 改公开名、上游ID后回传同一完整历史；签名及工具结果仍须原样到达上游。
				h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_name": "history-renamed", "litellm_params": map[string]any{"model": "renamed-upstream"}})
				body["model"] = "history-renamed"
				body["max_tokens"] = 65
				if got := h.doHeaders("POST", "/v1/messages", owner.key, body, headers); got.status != 200 {
					t.Fatalf("模型改名破坏原生续轮: %s", got.describe())
				}
				other := h.openScope(t, admin, "history-other")
				h.ok("POST", "/key/update", admin, map[string]any{"key": other.key, "models": []string{"another-model"}})
				if got := h.doHeaders("POST", "/v1/messages", other.key, body, headers); got.status < 400 {
					t.Fatal("完整历史绕过模型权限")
				}
				h.ok("PATCH", "/credentials/history-credential", admin, map[string]any{"credential_values": map[string]any{"api_key": "changed-key"}})
				body["max_tokens"] = 66
				if got := h.doHeaders("POST", "/v1/messages", owner.key, body, headers); got.status != 400 || !strings.Contains(got.text(), "signature rejected by upstream") {
					t.Fatalf("上游签名错误未保留: %s", got.describe())
				}
				mu.Lock()
				n := len(calls)
				mu.Unlock()
				if n != 4 {
					t.Fatalf("上游调用次数错误: %d", n)
				}
				// 未支持的程序化 caller 在同协议也明确拒绝，不删除来源后假装普通工具。
				invalid := map[string]any{"model": "history-renamed", "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "bad", "name": "lookup", "input": map[string]any{}, "caller": map[string]any{"type": "code_execution_20250825"}}}}}}
				if rejected := h.do("POST", "/v1/messages", owner.key, invalid); rejected.status != 400 {
					t.Fatalf("不支持的 caller 被接受: %s", rejected.describe())
				}
				h.ok("POST", "/model/delete", admin, map[string]any{"id": id})
				h.ok("DELETE", "/credentials/history-credential", admin, nil)
			})
		}
	}
}

// historyValidateChat 验证Chat工具必须按同一assistant分组并紧随匹配结果；参数为上游正文，返回契约错误。
// 仅受控上游调用，依照工具ID校验，不调整网关输出或放宽供应商契约，无外部副作用。
func historyValidateChat(body map[string]any) error {
	pending := map[string]bool{}
	for _, v := range body["messages"].([]any) {
		m := v.(map[string]any)
		if m["role"] == "tool" {
			id, _ := m["tool_call_id"].(string)
			if !pending[id] {
				return fmt.Errorf("orphan result %s", id)
			}
			delete(pending, id)
			continue
		}
		if len(pending) > 0 {
			return fmt.Errorf("tool results must follow all assistant calls")
		}
		for _, x := range listField(m, "tool_calls") {
			pending[x["id"].(string)] = true
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("missing tool results")
	}
	return nil
}

// TestChatAgentToolHistory 验证原生Chat index和Responses双并行调用通过完整历史/游标续轮可用，且非法元数据不上游。
// 参数 t 为上下文；本地严格上游与真实用户Key/数据库，JSON/SSE首轮；harness回收所有临时数据。
func TestChatAgentToolHistory(t *testing.T) {
	for _, endpoint := range []string{"chat/completions", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", endpoint, stream), func(t *testing.T) {
				var mu sync.Mutex
				calls := 0
				up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					mu.Lock()
					calls++
					n := calls
					mu.Unlock()
					if err := historyValidateChat(body); err != nil {
						w.WriteHeader(400)
						writeJSON(w, map[string]any{"error": map[string]any{"message": err.Error()}})
						return
					}
					tools := []any{}
					for i, id := range []string{"a", "b"} {
						tools = append(tools, map[string]any{"index": i, "id": id, "type": "function", "function": map[string]any{"name": "read", "arguments": "{}"}})
					}
					m := map[string]any{"role": "assistant", "content": "history-ok"}
					stop := "stop"
					raw, _ := json.Marshal(body)
					if !strings.Contains(string(raw), "result-") {
						m["content"] = nil
						m["tool_calls"] = tools
						stop = "tool_calls"
					}
					response := map[string]any{"id": fmt.Sprintf("chat_history_%d", n), "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": m, "finish_reason": stop}}, "usage": openAIUsage(nil)}
					if body["stream"] == true {
						w.Header().Set("Content-Type", "text/event-stream")
						b, _ := json.Marshal(map[string]any{"id": response["id"], "choices": []any{map[string]any{"index": 0, "delta": m, "finish_reason": stop}}, "usage": openAIUsage(nil)})
						fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
					} else {
						writeJSON(w, response)
					}
				})
				h, owner, admin, id := historyHarness(t, "chat", up)
				initial := map[string]any{"model": "history-model", "stream": stream}
				if endpoint == "responses" {
					initial["input"] = "Read both files."
				} else {
					initial["messages"] = []any{map[string]any{"role": "user", "content": "Read both files."}}
				}
				first := h.ok("POST", "/v1/"+endpoint, owner.key, initial)
				var output map[string]any
				if !stream {
					output = first.json()
				} else {
					for _, line := range strings.Split(first.text(), "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						var e map[string]any
						_ = json.Unmarshal([]byte(line[6:]), &e)
						if endpoint == "responses" && e["type"] == "response.completed" {
							output = e["response"].(map[string]any)
						} else if endpoint != "responses" && e["choices"] != nil && len(e["choices"].([]any)) > 0 {
							choice := e["choices"].([]any)[0].(map[string]any)
							if m, ok := choice["delta"].(map[string]any); ok && m["tool_calls"] != nil {
								output = map[string]any{"choices": []any{map[string]any{"message": m}}}
							}
						}
					}
				}
				if output == nil {
					t.Fatalf("首轮工具内容缺失: %s", first.text())
				}
				body := map[string]any{"model": "history-model"}
				if endpoint == "responses" {
					results := []any{map[string]any{"type": "function_call_output", "call_id": "b", "output": "result-b"}, map[string]any{"type": "function_call_output", "call_id": "a", "output": "result-a"}}
					body["input"] = results
					body["previous_response_id"] = output["id"]
					h.ok("POST", "/v1/responses", owner.key, body)
					delete(body, "previous_response_id")
					body["input"] = append(append([]any{map[string]any{"role": "user", "content": "Read both files."}}, output["output"].([]any)...), results...)
				} else {
					body["messages"] = []any{map[string]any{"role": "user", "content": "Read both files."}, output["choices"].([]any)[0].(map[string]any)["message"], map[string]any{"role": "tool", "tool_call_id": "b", "content": "result-b"}, map[string]any{"role": "tool", "tool_call_id": "a", "content": "result-a"}}
				}
				got := h.ok("POST", "/v1/"+endpoint, owner.key, body)
				if !strings.Contains(got.text(), "history-ok") {
					t.Fatal(got.describe())
				}
				// index 是位置元数据但必须是非负整数；非法 SDK 历史在入口拒绝，不能交给供应商猜测。
				invalid := map[string]any{"model": "history-model", "messages": []any{map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": -1, "id": "bad", "type": "function", "function": map[string]any{"name": "read", "arguments": "{}"}}}}}}
				mu.Lock()
				beforeInvalid := calls
				mu.Unlock()
				if rejected := h.do("POST", "/v1/chat/completions", owner.key, invalid); rejected.status != 400 {
					t.Fatalf("非法工具index未拒绝: %s", rejected.describe())
				}
				mu.Lock()
				afterInvalid := calls
				mu.Unlock()
				if afterInvalid != beforeInvalid {
					t.Fatal("非法工具index进入上游")
				}
				h.ok("POST", "/model/delete", admin, map[string]any{"id": id})
				h.ok("DELETE", "/credentials/history-credential", admin, nil)
			})
		}
	}
}
