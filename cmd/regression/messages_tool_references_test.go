package regression

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// referenceContractBody 创建合成客户端旧会话或新会话；参数model和history决定路由/工具结果，返回可修改正文；测试调用，无外部依赖。
func referenceContractBody(model string, history bool) map[string]any {
	messages := []any{map[string]any{"role": "user", "content": "Find and run lookup."}}
	if history {
		messages = append(messages, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "search-1", "name": "ToolSearch", "input": map[string]any{"query": "lookup"}}}}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "search-1", "content": []any{map[string]any{"type": "tool_reference", "tool_name": "lookup"}, map[string]any{"type": "text", "text": "保留用户原文"}}}}})
	}
	return map[string]any{"model": model, "max_tokens": 64, "messages": messages, "output_config": map[string]any{"effort": "high", "future_option": true}, "tools": []any{map[string]any{"name": "ToolSearch", "input_schema": map[string]any{"type": "object"}}, map[string]any{"name": "lookup", "input_schema": map[string]any{"type": "object"}, "defer_loading": true}}}
}

// TestMessagesToolReferencesContract 通过真实HTTP/PG验证新会话选项和工具引用历史的同步/流式无损转发、账单及错误路径。
// 使用有限额Key和本地严格上游，核对无副作用的格式/权限/预算拒绝及上游400/402/403保留；harness与httptest自动清理所有资源。
func TestMessagesToolReferencesContract(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	var calls atomic.Int32
	var expected atomic.Value
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := expected.Load().(map[string]any)
		for _, field := range []string{"messages", "output_config", "tools"} {
			if !reflect.DeepEqual(body[field], want[field]) {
				t.Errorf("原生%s失真: %#v", field, body[field])
			}
		}
		if body["model"] != "native-real" || r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") == "" {
			t.Errorf("出站路由或认证错误: %s %#v", r.URL.Path, body["model"])
		}
		if body["max_tokens"] == float64(66) || body["max_tokens"] == float64(67) {
			code := 403
			if body["max_tokens"] == float64(67) {
				code = 402
			}
			w.Header().Set("X-Request-Id", "fixture-payg-id")
			w.WriteHeader(code)
			failure := map[string]any{"code": strconv.Itoa(code), "type": "access_denied", "message": "You have no permission to access this resource (api_key_source: payg) (request_id: fixture-payg-id)"}
			if code == 402 {
				failure = map[string]any{"error": map[string]any{"code": "402", "type": "reject_no_credit", "message": "Access denied: this model is only available to accounts with a balance greater than 0. This is an anti-abuse measure, not a usage charge. (request_id: fixture-payg-id)"}}
			}
			writeJSON(w, failure)
			return
		}
		if body["max_tokens"] == float64(65) {
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": "fixture native rejection"}})
			return
		}
		historyMessagesReply(w, []any{map[string]any{"type": "text", "text": "REFERENCE_OK"}}, body["stream"] == true)
	}))
	t.Cleanup(up.Close)
	dep := hardBudgetDeployment("claude-native")
	dep.LiteLLMParams["model"] = "native-real"
	dep.LiteLLMParams["api_base"] = up.URL
	dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
	dep.ModelInfo["transport"] = "bypass_anthropic_messages"
	dep.ModelInfo["endpoint_types"] = []string{"messages", "bypass:anthropic-messages"}
	h := openHarness(t, false, dep)
	admin := h.adminSession()
	owner := h.openScope(t, admin, dep.ModelName)
	h.setKeyBudget(t, admin, owner.key, 1)
	for _, history := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			body := referenceContractBody(dep.ModelName, history)
			body["stream"] = stream
			var normalized map[string]any
			_ = json.Unmarshal([]byte(mustJSON(body)), &normalized)
			expected.Store(normalized)
			before := calls.Load()
			got := h.ok("POST", "/v1/messages", owner.key, body)
			if !strings.Contains(got.text(), "REFERENCE_OK") || calls.Load() != before+1 {
				t.Fatalf("原生请求未完成: %s", got.describe())
			}
			if stream && !strings.Contains(got.text(), "message_stop") {
				t.Fatal("流缺少终态")
			}
			bill := logDetail(t, h, admin, got.header("x-litellm-call-id"))
			if asIntAny(bill["prompt_tokens"]) != 8 || asIntAny(bill["completion_tokens"]) != 2 || bill["status"] != "success" {
				t.Fatalf("账单不符: %v", bill)
			}
		}
	}
	body := referenceContractBody(dep.ModelName, true)
	var normalized map[string]any
	_ = json.Unmarshal([]byte(mustJSON(body)), &normalized)
	expected.Store(normalized)
	body["max_tokens"] = 65
	got := h.do("POST", "/v1/messages", owner.key, body)
	if got.status != 400 || !strings.Contains(got.text(), "fixture native rejection") {
		t.Fatalf("上游错误丢失: %s", got.describe())
	}
	// PAYG 拒绝来自合成上游：Bypass 保留错误和ID；关闭提示词保存时仍可在本人错误日志列表/详情查询，且不收费。
	for _, failureCase := range []struct{ status, cap int }{{403, 66}, {402, 67}} {
		code := failureCase.status
		marker := "api_key_source: payg"
		if code == 402 {
			marker = "reject_no_credit"
		}
		body["max_tokens"] = failureCase.cap
		payg := h.do("POST", "/bypass/anthropic/v1/messages", owner.key, body)
		if payg.status != code || payg.header("x-request-id") != "fixture-payg-id" || !strings.Contains(payg.text(), marker) {
			t.Fatalf("供应商%d或request ID丢失: %s", code, payg.describe())
		}
		id := payg.header("x-litellm-call-id")
		failure := logDetail(t, h, owner.session, id)
		diagnostic := failure["metadata"].(map[string]any)["upstream_response"].(map[string]any)
		if diagnostic["status_code"] != float64(code) || failure["spend"] != float64(0) || !strings.Contains(firstString(failure, "error"), marker) {
			t.Fatalf("供应商%d诊断或零费用错误: %v", code, failure)
		}
		rows := rowsOf(h.ok("GET", "/spend/logs/ui?status_filter=error&page_size=200", owner.session, nil), "data")
		if findLogByRequestID(rows, id) == nil {
			t.Fatalf("本人错误日志列表遗漏供应商%d: %s", code, id)
		}
	}
	body["max_tokens"] = 65
	before := calls.Load()
	malformed := referenceContractBody(dep.ModelName, true)
	malformed["output_config"] = "invalid"
	if got := h.do("POST", "/v1/messages", owner.key, malformed); got.status != 400 {
		t.Fatalf("畸形原生选项未拒绝: %s", got.describe())
	}
	if got := h.do("POST", "/v1/messages", "", body); got.status != 401 {
		t.Fatalf("匿名请求未拒绝: %s", got.describe())
	}
	h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "models": []string{"other"}})
	if got := h.do("POST", "/v1/messages", owner.key, body); got.status < 400 {
		t.Fatal("原生请求绕过模型权限")
	}
	h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "models": []string{dep.ModelName}})
	zero := h.openScope(t, admin, "zero-reference")
	h.setKeyBudget(t, admin, zero.key, 0)
	if got := h.do("POST", "/v1/messages", zero.key, body); got.status != 429 {
		t.Fatalf("零预算未拒绝: %s", got.describe())
	}
	if calls.Load() != before {
		t.Fatal("未授权/零预算请求到达上游")
	}
	holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
	if err != nil || len(holds) != 0 {
		t.Fatalf("软预算残留预占: %v %v", holds, err)
	}
}

// TestMessagesToolReferencesBridgeBoundary 验证GPT上游不能收到被删引用的请求；普通纯文本仍可切换，测试结束自动清理schema与本地服务。
func TestMessagesToolReferencesBridgeBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("chat-only"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "bridge-reference")
	body := referenceContractBody("chat-only", true)
	delete(body, "tools")
	delete(body, "output_config")
	got := h.do("POST", "/v1/messages", owner.key, body)
	if got.status != 400 || len(h.upstreamCalls()) != 0 || !strings.Contains(got.text(), "model_unavailable") {
		t.Fatalf("工具引用被有损桥接: %s", got.describe())
	}
	clean := map[string]any{"model": "chat-only", "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": "Hello"}}}
	if got := h.ok("POST", "/v1/messages", owner.key, clean); !strings.Contains(got.text(), "regression-ok") {
		t.Fatalf("普通跨协议回复回归: %s", got.describe())
	}
}
