package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestNativeResponsesDecoupling 验证真实路由的默认关闭、账号灰度、全量开关、原生扩展、SSE及历史回退。
// 隔离PG和本地上游统计外发，既有账号及账务由harness清理；所有断言均经过真实HTTP边界。
func TestNativeResponsesDecoupling(t *testing.T) {
	var calls atomic.Int32
	var extended atomic.Bool
	var continuation atomic.Bool
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer local-key" || body["store"] != false {
			t.Error("出站协议或凭据错误")
		}
		if continuation.Load() {
			items, _ := body["input"].([]any)
			if len(items) < 3 {
				t.Error("previous_response_id历史丢失")
			}
		}
		response := nativeAgentResponse(fmt.Sprintf("resp_native_%d", n), "Native reply", false, nil)
		if extended.Load() {
			items, _ := body["input"].([]any)
			if len(items) == 0 || items[0].(map[string]any)["type"] != "future_item" {
				t.Error("原生输入扩展丢失")
			}
			response["output"].([]any)[0].(map[string]any)["content"] = []any{map[string]any{"type": "future_output", "payload": "Preserved"}}
		}
		writeAgentResponse(w, response, body["stream"] == true)
	}))
	// setGlobal 通过配置接口设置总开关和真实身份名单，关闭后不删除历史。
	setGlobal := func(enabled bool, user string) {
		t.Helper()
		h.ok("POST", "/config/update", admin, map[string]any{"general_settings": map[string]any{"native_responses": map[string]any{"enabled": enabled, "user_ids": []string{user}}}})
	}
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
	body := map[string]any{"model": "history-model", "input": []any{map[string]any{"type": "future_item", "payload": map[string]any{"x": "keep"}}}}
	extended.Store(true)
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || calls.Load() != 0 {
		t.Fatal("默认关闭没有保留原有校验")
	}
	setGlobal(true, "not-owner")
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || calls.Load() != 0 {
		t.Fatal("未命中名单却启用")
	}
	setGlobal(true, owner.userID)
	for _, stream := range []bool{false, true} {
		body["stream"] = stream
		got := h.do("POST", "/v1/responses", owner.key, body)
		if got.status != 200 || !strings.Contains(got.text(), "future_output") || got.header("x-xhub-responses-path") != "native" {
			t.Fatalf("原生扩展未保留: %s", got.describe())
		}
		if stream && !strings.Contains(got.text(), "response.completed") {
			t.Fatal("完成事件缺失")
		}
		h.flushSpend()
		log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
		if log["status"] != "success" {
			t.Fatalf("原生请求未正确记账: %v", log)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("重复外发")
	}
	// 显式全量无需名单，已授权的其他账号也能使用原生路径；仍由 harness 清理。
	h.ok("POST", "/config/update", admin, map[string]any{"general_settings": map[string]any{"native_responses": map[string]any{"enabled": true, "all_users": true}}})
	otherAll := h.openScope(t, admin, "all-users")
	if got := h.do("POST", "/v1/responses", otherAll.key, body); got.status != 200 || got.header("x-xhub-responses-path") != "native" {
		t.Fatalf("全量未生效: %s", got.describe())
	}
	extended.Store(false)
	first := h.ok("POST", "/v1/responses", owner.key, map[string]any{"model": "history-model", "input": "Hello"})
	parent := first.json()["id"]
	continuation.Store(true)
	next := map[string]any{"model": "history-model", "input": "Continue.", "previous_response_id": parent}
	if got := h.do("POST", "/v1/responses", owner.key, next); got.status != 200 {
		t.Fatalf("原生旧会话续接失败: %s", got.describe())
	}
	other := h.openScope(t, admin, "not-owner")
	before := calls.Load()
	if got := h.do("POST", "/v1/responses", other.key, next); got.status != 400 || calls.Load() != before {
		t.Fatal("父响应归属检查失效")
	}
	setGlobal(false, owner.userID)
	got := h.do("POST", "/v1/responses", owner.key, next)
	if got.status != 200 || got.header("x-xhub-responses-path") != "legacy" {
		t.Fatalf("关闭后旧会话不能续接: %s", got.describe())
	}
	before = calls.Load()
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || calls.Load() != before {
		t.Fatal("关闭后扩展仍被放行")
	}
}

// TestNativeResponsesGovernance 验证原生灰度仍执行真实鉴权、预算和护栏，原生新内容遇不支持的护栏失败关闭。
// 隔离数据库和计数上游，无真实供应商；harness清理账号、规则及账务。
func TestNativeResponsesGovernance(t *testing.T) {
	var calls atomic.Int32
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(fmt.Sprint(body["input"]), "secret") {
			t.Error("脱敏内容泄漏")
		}
		writeJSON(w, nativeAgentResponse("resp_guard", "Safe reply", false, nil))
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
	h.ok("POST", "/config/update", admin, map[string]any{"general_settings": map[string]any{"native_responses": map[string]any{"enabled": true, "user_ids": []string{owner.userID}}}})
	body := map[string]any{"model": "history-model", "input": "Hello"}
	if got := h.do("POST", "/v1/responses", "invalid", body); got.status != 401 {
		t.Fatal("未授权请求通过")
	}
	raw, _ := json.Marshal(map[string]any{"guardrail_name": "block-native", "litellm_params": map[string]any{"default_on": true, "mode": "pre_call", "blocked_words": []string{"bomb"}}})
	if err := h.store.PutKV("guardrails", "block-native", string(raw)); err != nil {
		t.Fatal(err)
	}
	body["input"] = "bomb"
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || calls.Load() != 0 {
		t.Fatal("原生路径绕过护栏")
	}
	body["input"] = []any{map[string]any{"type": "future_item", "payload": "unknown"}}
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 400 || !strings.Contains(got.text(), "active guardrail") || calls.Load() != 0 {
		t.Fatalf("未知内容未失败关闭: %s", got.describe())
	}
	body["input"] = "Hello"
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 200 || calls.Load() != 1 {
		t.Fatalf("护栏错误拦截正常原生请求: %s", got.describe())
	}
	h.ok("POST", "/key/update", admin, map[string]any{"key": owner.keyID, "max_budget": 0})
	if got := h.do("POST", "/v1/responses", owner.key, body); got.status != 403 || calls.Load() != 1 {
		t.Fatalf("原生请求绕过预算: %s", got.describe())
	}
}

// TestNativeResponsesFailureNoReplay 验证已收到上游成功HTTP后遇无效终态或断流，不重放生成，错误及日志一致。
// 每个子例真实HTTP和隔离PG；harness清理，计数断言无隐式旧路径回退/二次扣费。
func TestNativeResponsesFailureNoReplay(t *testing.T) {
	for _, kind := range []string{"malformed", "stream-first-error", "stream-truncated", "connection-lost", "body-truncated"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if kind == "connection-lost" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				if kind == "body-truncated" {
					w.Header().Set("Content-Length", "10000")
					fmt.Fprint(w, "{}")
					return
				}
				if kind == "malformed" {
					writeJSON(w, map[string]any{"id": "bad", "status": "queued", "output": []any{}})
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if kind == "stream-first-error" {
					fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"r\",\"error\":{\"message\":\"Vendor unavailable\",\"type\":\"api_error\"}}}\n\n")
					return
				}
				fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Partial\"}\n\n")
			}))
			h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
			h.ok("POST", "/config/update", admin, map[string]any{"general_settings": map[string]any{"native_responses": map[string]any{"enabled": true, "user_ids": []string{owner.userID}}}})
			got := h.do("POST", "/v1/responses", owner.key, map[string]any{"model": "history-model", "input": "Hello", "stream": strings.HasPrefix(kind, "stream-")})
			if calls.Load() != 1 {
				t.Fatalf("上游已执行却重放: %d", calls.Load())
			}
			if kind == "stream-truncated" {
				if got.status != 200 || !strings.Contains(got.text(), "response.failed") || strings.Contains(got.text(), "response.completed") {
					t.Fatalf("断流伪成功: %s", got.describe())
				}
			} else if got.status != 502 {
				t.Fatalf("错误终态未返回502: %s", got.describe())
			}
			if kind == "stream-first-error" && !strings.Contains(got.text(), "Vendor unavailable") {
				t.Fatal("上游错误丢失")
			}
			h.flushSpend()
			log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
			if log["status"] != "error" {
				t.Fatal("失败未记入日志")
			}
		})
	}
}

// TestNativeResponsesKnownHistory 验证开启后的压缩历史、工具图片和流选项保持原值，上游拒绝状态与正文保持。
// 使用真实网关/PG和本地上游，流式与非流式均调用；harness回收全部隔离资源。
func TestNativeResponsesKnownHistory(t *testing.T) {
	input := []any{
		map[string]any{"type": "compaction", "id": "cmp_old", "encrypted_content": "fixture-cipher"},
		map[string]any{"type": "function_call", "call_id": "call_1", "name": "read", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": []any{map[string]any{"type": "input_text", "text": "Image result"}, map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA"}}},
	}
	options := map[string]any{"reasoning_summary_delivery": "sequential_cutoff", "include_obfuscation": false}
	var status atomic.Int32
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if string(mustJSON(body["input"])) != string(mustJSON(input)) || string(mustJSON(body["stream_options"])) != string(mustJSON(options)) {
			t.Error("历史/图片/流参数被改写")
		}
		if code := status.Load(); code != 0 {
			w.WriteHeader(int(code))
			writeJSON(w, map[string]any{"error": map[string]any{"type": "vendor_reject", "message": "Vendor rejection preserved"}})
			return
		}
		writeAgentResponse(w, nativeAgentResponse("resp_known", "Known history retained", true, nil), body["stream"] == true)
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}})
	h.ok("POST", "/config/update", admin, map[string]any{"general_settings": map[string]any{"native_responses": map[string]any{"enabled": true, "user_ids": []string{owner.userID}}}})
	body := map[string]any{"model": "history-model", "input": input, "stream_options": options}
	for _, stream := range []bool{false, true} {
		body["stream"] = stream
		got := h.do("POST", "/v1/responses", owner.key, body)
		if got.status != 200 || !strings.Contains(got.text(), "Known history retained") || strings.Contains(got.text(), "encrypted_content") {
			t.Fatalf("原生历史或portable契约回归: %s", got.describe())
		}
	}
	body["stream"] = false
	for _, code := range []int32{402, 403, 429, 503} {
		status.Store(code)
		got := h.do("POST", "/v1/responses", owner.key, body)
		if got.status != int(code) || !strings.Contains(got.text(), "Vendor rejection preserved") {
			t.Fatalf("上游状态/正文丢失: %s", got.describe())
		}
	}
}

// TestNativeResponsesBypassBoundary 验证标准与透传入口使用同一部署时，账号灰度仅改变标准入口。
// 本地上游返回扩展内容；覆盖鉴权、原生JSON/SSE、错误正文及日志，harness自动清理隔离数据库。
func TestNativeResponsesBypassBoundary(t *testing.T) {
	var code atomic.Int32
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/responses" {
			t.Errorf("同协议错误转换到 %s", r.URL.Path)
		}
		if code.Load() != 0 {
			w.WriteHeader(int(code.Load()))
			writeJSON(w, map[string]any{"error": map[string]any{"type": "vendor_reject", "message": "Upstream denied"}})
			return
		}
		writeAgentResponse(w, nativeAgentResponse("resp_boundary", "Boundary reply", false, nil), body["stream"] == true)
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_responses", "endpoint_types": []string{"responses", "bypass:openai-responses"}}})
	for _, enabled := range []bool{false, true} {
		h.ok("POST", "/config/update", admin, map[string]any{"general_settings": map[string]any{"native_responses": map[string]any{"enabled": enabled, "user_ids": []string{owner.userID}}}})
		for _, path := range []string{"/v1/responses", "/bypass/openai/v1/responses"} {
			body := map[string]any{"model": "history-model", "input": "Hello"}
			if got := h.do("POST", path, "invalid", body); got.status != 401 {
				t.Fatal("入口鉴权失效", path)
			}
			for _, streaming := range []bool{false, true} {
				body["stream"] = streaming
				got := h.do("POST", path, owner.key, body)
				if got.status != 200 || !strings.Contains(got.text(), "Boundary reply") {
					t.Fatalf("入口失败 %s: %s", path, got.describe())
				}
				if path == "/v1/responses" {
					want := "legacy"
					if enabled {
						want = "native"
					}
					if got.header("x-xhub-responses-path") != want {
						t.Fatal("标准路径灰度错误")
					}
				}
				h.flushSpend()
				log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
				if log["status"] != "success" {
					t.Fatal("入口成功日志丢失", path)
				}
			}
			body["stream"] = false
			for _, status := range []int32{402, 403, 429, 503} {
				code.Store(status)
				got := h.do("POST", path, owner.key, body)
				if got.status != int(status) || !strings.Contains(got.text(), "Upstream denied") {
					t.Fatalf("入口错误失真 %s: %s", path, got.describe())
				}
				h.flushSpend()
				log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
				if log["status"] != "error" {
					t.Fatal("入口错误日志丢失", path)
				}
			}
			code.Store(0)
		}
	}
}

// TestNativeResponsesCrossProtocolHTTP 验证灰度账号遇 Chat 上游仍转换，未知扩展不静默丢弃。
// 真实HTTP验证出站messages和入站Responses，harness清理。
func TestNativeResponsesCrossProtocolHTTP(t *testing.T) {
	var calls atomic.Int32
	h, owner, admin, id := historyHarness(t, "responses", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/chat/completions" || body["messages"] == nil || body["input"] != nil {
			t.Errorf("跨协议出站错误: %s %v", r.URL.Path, body)
		}
		writeJSON(w, map[string]any{"id": "chat_cross", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Bridge reply"}, "finish_reason": "stop"}}, "usage": openAIUsage(nil)})
	}))
	h.ok("POST", "/model/update", admin, map[string]any{"id": id, "model_info": map[string]any{"transport": "bypass_openai_chat", "endpoint_types": []string{"responses"}}})
	h.ok("POST", "/config/update", admin, map[string]any{"general_settings": map[string]any{"native_responses": map[string]any{"enabled": true, "all_users": true}}})
	got := h.do("POST", "/v1/responses", owner.key, map[string]any{"model": "history-model", "input": "Hello"})
	if got.status != 200 || !strings.Contains(got.text(), "Bridge reply") || got.header("x-xhub-responses-path") != "legacy" {
		t.Fatalf("跨协议响应错误: %s", got.describe())
	}
	got = h.do("POST", "/v1/responses", owner.key, map[string]any{"model": "history-model", "input": []any{map[string]any{"type": "future_item", "payload": "Keep"}}})
	if got.status != 400 || calls.Load() != 1 {
		t.Fatalf("跨协议扩展被丢弃: %s calls=%d", got.describe(), calls.Load())
	}
}
