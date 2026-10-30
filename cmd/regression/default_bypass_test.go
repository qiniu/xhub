package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestModelContractDefaultDialogueBypass 验证无透传声明的部署通过真实管理接口保存后即可原生调用。
// 前置隔离 PG、真实 HTTP 网关及本地上游；覆盖三协议、未知字段、原始 JSON/SSE、发现、编辑、计费、拒绝与删除。
// 模型显式删除，用户、密钥、账务和异常路径由 harness 回收独立 schema，不依赖供应商凭据。
func TestModelContractDefaultDialogueBypass(t *testing.T) {
	for _, tc := range []struct{ protocol, transport, path, vendorPath, answer, sse string }{
		{"openai-chat", "bypass_openai_chat", "/bypass/openai/v1/chat/completions", "/v1/chat/completions", `{"id":"native-chat","choices":[{"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2},"vendor_result":9007199254740993}`, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2},\"vendor_result\":9007199254740993}\n\ndata: [DONE]\n\n"},
		{"openai-responses", "bypass_openai_responses", "/bypass/openai/v1/responses", "/v1/responses", `{"id":"native-response","object":"response","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":2},"vendor_result":9007199254740993}`, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"native-response-stream\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":8,\"output_tokens\":2},\"vendor_result\":9007199254740993}}\n\n"},
		{"anthropic-messages", "bypass_anthropic_messages", "/bypass/anthropic/v1/messages", "/v1/messages", `{"id":"native-message","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2},"vendor_result":9007199254740993}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"native-message-stream\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"real-model\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":8,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2},\"vendor_result\":9007199254740993}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.URL.Path != tc.vendorPath || string(body["model"]) != `"real-model"` || string(body["vendor_extension"]) != `{"opaque":9007199254740993}` {
					t.Errorf("透传路径、模型替换或未知字段损坏: %s %s", r.URL.Path, mustJSON(body))
				}
				if tc.protocol == "anthropic-messages" {
					if r.Header.Get("x-api-key") != "sk-fake" {
						t.Error("上游鉴权错误")
					}
				} else if r.Header.Get("Authorization") != "Bearer sk-fake" {
					t.Error("上游鉴权错误")
				}
				w.Header().Set("Content-Type", "application/json")
				if string(body["stream"]) == "true" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, tc.sse)
				} else {
					fmt.Fprint(w, tc.answer)
				}
			}))
			defer up.Close()
			h := newHarness(t)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "default-bypass")
			dep := chatDeployment("default-bypass")
			dep.LiteLLMParams["api_base"] = up.URL
			dep.LiteLLMParams["model"] = "real-model"
			dep.ModelInfo["transport"] = tc.transport
			delete(dep.ModelInfo, "endpoint_types")
			created := h.ok("POST", "/model/new", admin, map[string]any{"model_name": dep.ModelName, "litellm_params": dep.LiteLLMParams, "model_info": dep.ModelInfo}).json()
			id := stringField(created["model_info"].(map[string]any), "id")
			defer h.ok("POST", "/model/delete", admin, map[string]any{"id": id})
			// 旧配置只声明标准入口，回读不能被默认能力偷偷改写；编辑后的真实选路仍自动开放匹配原生入口。
			h.ok("PATCH", "/model/"+id+"/update", admin, map[string]any{"model_info": map[string]any{"endpoint_types": []string{"chat"}}})
			stored, err := h.store.ListProxyModels()
			if err != nil || len(stored) != 1 {
				t.Fatalf("部署未持久化: %v", err)
			}
			if got := string(mustJSON(stored[0].Info["endpoint_types"])); got != `["chat"]` {
				t.Fatalf("默认开放不应改写旧声明: %s", got)
			}
			rows := listField(h.ok("GET", "/model/available", owner.key, nil).json(), "data")
			if len(rows) != 1 {
				t.Fatalf("模型发现错误: %v", rows)
			}
			native := []string{}
			for _, raw := range rows[0]["endpoints"].([]any) {
				e := raw.(map[string]any)
				if strings.HasPrefix(stringField(e, "endpoint_id"), "bypass:") {
					native = append(native, stringField(e, "path"))
				}
			}
			if len(native) != 1 || native[0] != tc.path {
				t.Fatalf("匹配入口未自动公开: %v", native)
			}
			body := map[string]any{"model": dep.ModelName, "max_tokens": 32, "vendor_extension": map[string]any{"opaque": json.Number("9007199254740993")}}
			if tc.protocol == "openai-responses" {
				body["input"] = "Say hello."
				delete(body, "max_tokens")
				body["max_output_tokens"] = 32
			} else {
				body["messages"] = []any{map[string]any{"role": "user", "content": "Say hello."}}
			}
			for _, stream := range []bool{false, true} {
				body["stream"] = stream
				before := h.moneyOf(t, owner)
				r := h.ok("POST", tc.path, owner.key, body)
				want := tc.answer
				if stream {
					want = tc.sse
				}
				if string(r.body) != want {
					t.Fatalf("原生响应被转换 stream=%v: %s", stream, r.body)
				}
				if !h.moneyOf(t, owner).grewBy(before, 8*testInputRate+2*testOutputRate) {
					t.Fatal("默认透传未正确计费")
				}
			}
			before := calls.Load()
			if r := h.do("POST", tc.path, "sk-invalid", body); r.status != 401 {
				t.Fatalf("无效密钥未拒绝: %s", r.describe())
			}
			other := "/bypass/openai/v1/responses"
			if other == tc.path {
				other = "/bypass/anthropic/v1/messages"
			}
			if r := h.do("POST", other, owner.key, body); r.status < 400 {
				t.Fatalf("不兼容协议仍可透传: %s", r.describe())
			}
			h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "models": []string{"other-model"}})
			if r := h.do("POST", tc.path, owner.key, body); r.status != 401 {
				t.Fatalf("模型权限未拒绝: %s", r.describe())
			}
			h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "models": []string{dep.ModelName}})
			blocked := h.openScope(t, admin, "zero-budget")
			h.setUserBudget(t, admin, blocked.userID, 0)
			if r := h.do("POST", tc.path, blocked.key, body); r.status != 403 {
				t.Fatalf("零预算未拒绝: %s", r.describe())
			}
			h.ok("POST", "/model/disable", admin, map[string]any{"id": id})
			if r := h.do("POST", tc.path, owner.key, body); r.status < 400 {
				t.Fatalf("停用后仍可调用: %s", r.describe())
			}
			if calls.Load() != before {
				t.Fatal("拒绝请求仍到达上游")
			}
			h.ok("POST", "/model/enable", admin, map[string]any{"id": id})
			h.ok("POST", tc.path, owner.key, body)
			if calls.Load() != before+1 {
				t.Fatal("重新启用未恢复调用")
			}
		})
	}
}
