package regression

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/config"
)

// nativeAgentResponse 生成本地原生协议夹具的回复；参数为响应 ID、文本、是否包含密文推理与函数调用。
// 后台协议回归及真实 CLI 验收调用；返回完整 Responses 对象，固定用量供精确计费，无持久化副作用。
func nativeAgentResponse(id, text string, encrypted bool, call map[string]any) map[string]any {
	output := []any{}
	if encrypted {
		output = append(output, map[string]any{"id": "rs_" + id, "type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "Read the file first."}}, "encrypted_content": "test-cipher"})
	}
	if call != nil {
		output = append(output, call)
	} else {
		output = append(output, map[string]any{"id": "msg_" + id, "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}})
	}
	return map[string]any{"id": id, "object": "response", "status": "completed", "model": "vendor-model", "output": output, "usage": map[string]any{"input_tokens": 8, "output_tokens": 2, "total_tokens": 10, "output_tokens_details": map[string]any{"reasoning_tokens": 1}, "vendor_counter": 42}}
}

// writeAgentResponse 通过真实 HTTP 写 JSON 或完整原生 SSE 生命周期；参数为写入器、已构造结果与流开关。
// 协议回归和 CLI 上游夹具调用；无返回，写错误由客户端观察，测试服务退出即清理数据。
func writeAgentResponse(w http.ResponseWriter, response map[string]any, stream bool) {
	if !stream {
		writeJSON(w, response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(kind string, payload map[string]any) {
		payload["type"] = kind
		raw, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	emit("response.created", map[string]any{"response": map[string]any{"id": response["id"], "object": "response", "status": "in_progress", "model": response["model"], "output": []any{}}})
	for index, value := range response["output"].([]any) {
		item := value.(map[string]any)
		added := map[string]any{}
		for k, v := range item {
			added[k] = v
		}
		if item["type"] == "function_call" {
			added["arguments"] = ""
		}
		if item["type"] == "custom_tool_call" {
			added["input"] = ""
		}
		emit("response.output_item.added", map[string]any{"output_index": index, "item": added})
		if item["type"] == "reasoning" {
			// 真实供应商先发推理摘要增量再发正文；CLI 必须在正文之前收到 thinking 块，不能只在终态补发。
			for _, value := range item["summary"].([]any) {
				part := value.(map[string]any)
				emit("response.reasoning_summary_text.delta", map[string]any{"output_index": index, "item_id": item["id"], "summary_index": 0, "delta": part["text"]})
			}
		} else if item["type"] == "function_call" {
			emit("response.function_call_arguments.delta", map[string]any{"output_index": index, "item_id": item["id"], "delta": item["arguments"]})
			emit("response.function_call_arguments.done", map[string]any{"output_index": index, "item_id": item["id"], "arguments": item["arguments"]})
		} else if item["type"] == "custom_tool_call" {
			emit("response.custom_tool_call_input.delta", map[string]any{"output_index": index, "item_id": item["id"], "delta": item["input"]})
			emit("response.custom_tool_call_input.done", map[string]any{"output_index": index, "item_id": item["id"], "input": item["input"]})
		} else if item["type"] == "message" {
			part := item["content"].([]any)[0].(map[string]any)
			if part["type"] == "refusal" {
				emit("response.refusal.delta", map[string]any{"output_index": index, "item_id": item["id"], "content_index": 0, "delta": part["refusal"]})
				emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
				continue
			}
			text := part["text"]
			emit("response.content_part.added", map[string]any{"output_index": index, "item_id": item["id"], "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
			emit("response.output_text.delta", map[string]any{"output_index": index, "item_id": item["id"], "content_index": 0, "delta": text})
			emit("response.output_text.done", map[string]any{"output_index": index, "item_id": item["id"], "content_index": 0, "text": text})
		}
		emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	}
	emit("response.completed", map[string]any{"response": response})
}

// TestAgentNativeProtocolMatrix 验证三类模型经过标准端点的原生参数、密文/推理响应、流式工具续轮和计费。
// 参数 t 为回归上下文；使用真实网关、隔离 PostgreSQL、本地 HTTP 上游与个人密钥；每例独立 schema 自动回收。
// 同时验证未授权、跨身份续接、未知桥接能力拒绝以及默认护栏不会因原生保留而被绕过。
func TestAgentNativeProtocolMatrix(t *testing.T) {
	for _, protocol := range []string{"responses", "chat"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer sk-fake" || strings.Contains(string(mustJSON(body)), "secret-word") {
					t.Error("原生链凭据或护栏脱敏错误")
				}
				if protocol == "responses" {
					if body["reasoning"] == nil || body["include"] == nil || body["text"] == nil || body["store"] != false {
						t.Errorf("Agent 参数丢失: %v", body)
					}
					response := nativeAgentResponse(fmt.Sprintf("resp_%d", calls.Load()), "Done", true, nil)
					writeAgentResponse(w, response, body["stream"] == true)
				} else {
					if body["thinking"] == nil || body["reasoning_effort"] != "high" {
						t.Error("DeepSeek 参数丢失")
					}
					answer := chatAnswer("real", map[string]any{"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10})
					answer["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["reasoning_content"] = "Plan"
					if body["stream"] == true {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"id\":\"chat-native\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"Plan\",\"content\":\"Done\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
					} else {
						writeJSON(w, answer)
					}
				}
			}))
			t.Cleanup(upstream.Close)
			var models []config.ModelEntry
			for _, name := range []string{"gpt-5.6-sol", "grok-4.6", "deepseek-v4-flash"} {
				dep := chatDeployment(name)
				dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
				dep.ModelInfo["endpoint_types"] = []string{"chat", "responses", "messages"}
				if protocol == "responses" {
					dep.ModelInfo["transport"] = "bypass_openai_responses"
				}
				models = append(models, dep)
			}
			h := newHarness(t, models...)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "native-owner")
			h.putGuardrail(t, "native-redact", guardrailRow("native-redact", kindRedact, []string{"secret-word"}, "redact"))
			path := "/v1/responses"
			if protocol == "chat" {
				path = "/v1/chat/completions"
			}
			for _, model := range models {
				for _, stream := range []bool{false, true} {
					body := map[string]any{"model": model.ModelName, "stream": stream, "guardrails": []string{"native-redact"}}
					if protocol == "responses" {
						body["input"] = "Read secret-word"
						body["reasoning"] = map[string]any{"effort": "high", "summary": "auto"}
						body["include"] = []string{"reasoning.encrypted_content"}
						body["text"] = map[string]any{"format": map[string]any{"type": "text"}}
					} else {
						body["messages"] = []any{map[string]any{"role": "user", "content": "Read secret-word"}}
						body["thinking"] = map[string]any{"type": "enabled"}
						body["reasoning_effort"] = "high"
					}
					reply := h.do("POST", path, owner.key, body)
					if reply.status != 200 {
						t.Fatalf("%s stream=%v: %s", model.ModelName, stream, reply.describe())
					}
					if !strings.Contains(string(reply.body), map[string]string{"responses": "Read the file first.", "chat": "Plan"}[protocol]) {
						t.Fatalf("原生推理字段丢失: %s", reply.body)
					}
					h.flushSpend()
					bill := h.ok("GET", "/spend/logs/ui/"+reply.headers.Get("x-litellm-call-id"), admin, nil).json()
					amount, _ := floatField(bill, "spend")
					if bill["status"] != "success" || bill["api_key"] != owner.keyID || !nearlyEqual(amount, 8*testInputRate+2*testOutputRate) {
						t.Fatalf("原生请求未正确记账: %v", bill)
					}
				}
			}
			// 明确验证预算拒绝仍发生在外发之前，不能因原生字段保留绕过用户额度。
			before := calls.Load()
			if denied := h.do("POST", path, "sk-invalid", map[string]any{"model": models[0].ModelName, "input": "Hello"}); denied.status != 401 {
				t.Fatal("无效密钥未拒绝")
			}
			body := map[string]any{"model": models[0].ModelName, "input": "Hello", "vendor_unknown": true}
			if protocol == "chat" {
				path = "/v1/responses"
			}
			if protocol == "responses" {
				body = map[string]any{"model": models[0].ModelName, "messages": []any{map[string]any{"role": "user", "content": "Hello"}}, "vendor_unknown": true}
				path = "/v1/chat/completions"
			}
			if denied := h.do("POST", path, owner.key, body); denied.status != 400 {
				t.Fatalf("未知跨协议能力应在外发前拒绝: %s", denied.describe())
			}
			if calls.Load() != before {
				t.Fatal("被拒绝的请求仍外发")
			}
			blockedOwner := h.openScope(t, admin, "native-zero-budget")
			h.setUserBudget(t, admin, blockedOwner.userID, 0)
			budgetPath := "/v1/responses"
			budgetBody := map[string]any{"model": models[0].ModelName, "input": "Hello"}
			if protocol == "chat" {
				budgetPath = "/v1/chat/completions"
				budgetBody = map[string]any{"model": models[0].ModelName, "messages": []any{map[string]any{"role": "user", "content": "Hello"}}}
			}
			denied := h.do("POST", budgetPath, blockedOwner.key, budgetBody)
			if denied.status != 403 || calls.Load() != before {
				t.Fatalf("原生入口未遵守预算: %s", denied.describe())
			}

		})
	}
}

// TestNativeAgentCancellation 验证原生流客户端取消会终止上游，且不会重试、提交成功历史或成功账单。
// 参数 t 为上下文；前置真实 HTTP 网关、阻塞的本地 SSE 与独立 PostgreSQL；超时作为兜底，defer 关闭正文并取消请求，harness 回收 schema。
func TestNativeAgentCancellation(t *testing.T) {
	var calls atomic.Int32
	stopped := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_cancelled\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		stopped <- struct{}{}
	}))
	t.Cleanup(upstream.Close)
	dep := chatDeployment("native-cancel")
	dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
	dep.ModelInfo = map[string]any{"id": "native-cancel-dep", "transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}
	h := newHarness(t, dep)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "native-cancel")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", h.server.URL+"/v1/responses", strings.NewReader(`{"model":"native-cancel","input":"Hello","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+owner.key)
	req.Header.Set("Content-Type", "application/json")
	reply, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer reply.Body.Close()
	buffer := make([]byte, 256)
	if _, err = reply.Body.Read(buffer); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = reply.Body.Close()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("客户端取消后上游未终止")
	}
	h.flushSpend()
	if calls.Load() != 1 || len(h.successRows(t, admin, "native-cancel")) != 0 {
		t.Fatal("取消流被重试或记作成功")
	}
	denied := h.do("POST", "/v1/responses", owner.key, map[string]any{"model": "native-cancel", "input": "Next", "previous_response_id": "resp_cancelled"})
	if denied.status != 400 || calls.Load() != 1 {
		t.Fatalf("取消流发布了成功归属: %s", denied.describe())
	}
}

// TestNativeEncryptedHistoryOwnership 验证统一入口返回公开历史，增量游标固定原连接，完整公开历史允许换连接。
// 参数 t 为上下文；真实管理接口创建同名部署和私有用户，覆盖公开续轮、游标跨身份拒绝、目标停用和备用连接。
// 上游仅为本地 HTTP，部署/账单/凭据均位于隔离 schema；清理由 harness 和 t.Cleanup 执行。
func TestNativeEncryptedHistoryOwnership(t *testing.T) {
	var calls atomic.Int32
	var actualModel string
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		actualModel = stringField(body, "model")
		mu.Unlock()
		n := calls.Add(1)
		writeAgentResponse(w, nativeAgentResponse(fmt.Sprintf("resp_owned_%d", n), "Done", true, nil), false)
	}))
	t.Cleanup(upstream.Close)
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "cipher-owner")
	ids := map[string]string{}
	for _, model := range []string{"connection-a", "connection-b"} {
		dep := chatDeployment("encrypted-native")
		dep.LiteLLMParams["model"] = model
		dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
		dep.ModelInfo = map[string]any{"transport": "bypass_openai_responses", "pricing_source": "manual", "endpoint_types": []string{"responses"}}
		saved := h.ok("POST", "/model/new", admin, map[string]any{"model_name": dep.ModelName, "litellm_params": dep.LiteLLMParams, "model_info": dep.ModelInfo}).json()
		ids[model] = saved["model_info"].(map[string]any)["id"].(string)
	}
	headers := map[string]string{"X-Session-Id": "encrypted-owned-session"}
	first := h.doHeaders("POST", "/v1/responses", owner.key, map[string]any{"model": "encrypted-native", "input": "Hello"}, headers)
	if first.status != 200 {
		t.Fatal(first.describe())
	}
	mu.Lock()
	original := actualModel
	mu.Unlock()
	input := append([]any{map[string]any{"role": "user", "content": "Hello"}}, first.json()["output"].([]any)...)
	input = append(input, map[string]any{"role": "user", "content": "Continue"})
	body := map[string]any{"model": "encrypted-native", "input": input}
	second := h.doHeaders("POST", "/v1/responses", owner.key, body, headers)
	mu.Lock()
	selected := actualModel
	mu.Unlock()
	if second.status != 200 || selected != original {
		t.Fatalf("密文被切换到其他连接: original=%s actual=%s %s", original, selected, second.describe())
	}
	stranger := h.openScope(t, admin, "cipher-stranger")
	denied := h.doHeaders("POST", "/v1/responses", stranger.key, body, headers)
	if denied.status != 200 || calls.Load() != 3 {
		t.Fatalf("自行提交的公开历史未正常执行: %s", denied.describe())
	}
	private := h.do("POST", "/v1/responses", stranger.key, map[string]any{"model": "encrypted-native", "previous_response_id": first.json()["id"], "input": "Continue"})
	if private.status != 400 || calls.Load() != 3 {
		t.Fatalf("跨身份游标未拒绝: %s", private.describe())
	}
	h.ok("PATCH", "/model/"+ids[original]+"/update", admin, map[string]any{"model_info": map[string]any{"disabled": true}})
	unavailable := h.doHeaders("POST", "/v1/responses", owner.key, body, headers)
	if unavailable.status != 200 || calls.Load() != 4 {
		t.Fatalf("公开历史未允许备用连接: %s", unavailable.describe())
	}
	plain := h.do("POST", "/v1/responses", stranger.key, map[string]any{"model": "encrypted-native", "input": "A fresh conversation"})
	if plain.status != 200 || calls.Load() != 5 {
		t.Fatalf("普通新会话未使用剩余连接: %s", plain.describe())
	}
}

// TestNativeRefusalAndLifecycleContract 验证拒绝响应保留为正常原生结果，续轮可恢复，异步请求不得创建上游任务。
// 参数 t 为回归上下文；使用真实鉴权/数据库和本地原生 Responses，覆盖 JSON/SSE、计费、历史和 400 错误；harness 回收全部测试数据。
func TestNativeRefusalAndLifecycleContract(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		result := nativeAgentResponse(fmt.Sprintf("resp_refusal_%d", calls.Load()), "", false, nil)
		result["output"] = []any{map[string]any{"id": "msg_refusal", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "refusal", "refusal": "Refused safely"}}}}
		writeAgentResponse(w, result, body["stream"] == true)
	}))
	t.Cleanup(upstream.Close)
	dep := chatDeployment("native-refusal")
	dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
	dep.ModelInfo = map[string]any{"max_input_tokens": 1024, "id": "refusal-dep", "transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}
	h := newHarness(t, dep)
	admin := h.personalBillingSession()
	for _, stream := range []bool{false, true} {
		reply := h.do("POST", "/v1/responses", admin, map[string]any{"model": "native-refusal", "input": "Hello", "stream": stream})
		if reply.status != 200 || !strings.Contains(string(reply.body), "Refused safely") || strings.Contains(string(reply.body), "response.failed") {
			t.Fatalf("有效拒绝响应被改为错误: %s", reply.describe())
		}
		h.flushSpend()
		bill := h.ok("GET", "/spend/logs/ui/"+reply.header("x-litellm-call-id"), admin, nil).json()
		if bill["status"] != "success" {
			t.Fatalf("拒绝响应记账错误: %v", bill)
		}
	}
	h.ok("POST", "/v1/responses", admin, map[string]any{"model": "native-refusal", "input": "A different request", "previous_response_id": "resp_refusal_1"})
	before := calls.Load()
	for _, extra := range []map[string]any{{"background": true}, {"n": 2}} {
		body := map[string]any{"model": "native-refusal", "input": "Hello"}
		for k, v := range extra {
			body[k] = v
		}
		denied := h.do("POST", "/v1/responses", admin, body)
		if denied.status != 400 || calls.Load() != before {
			t.Fatalf("不支持的生命周期创建了上游任务: %s", denied.describe())
		}
	}
}
