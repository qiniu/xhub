package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestResponseConnectionChanges 验证连接变更后旧游标拒绝，统一入口公开历史和原生透传完整历史仍可继续。
// 参数 t 为上下文；真实 HTTP/PostgreSQL 创建供应商、用户、模型及账单，覆盖统一/原生入口和地址、型号、凭据名、同名凭据更新。
// 验证原连接续接、未授权拒绝、普通请求仍可重新选路、成功记账；本地 HTTP 上游和私有 schema 由 Cleanup/harness 回收。
func TestResponseConnectionChanges(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/bypass/openai/v1/responses"} {
		for _, mutation := range []string{"api_base", "model", "credential", "credential_values"} {
			t.Run(path+"/"+mutation, func(t *testing.T) {
				var calls atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					n := calls.Add(1)
					writeAgentResponse(w, nativeAgentResponse(fmt.Sprintf("resp_connection_%d", n), "Done", true, nil), body["stream"] == true)
				}))
				t.Cleanup(up.Close)
				h := newHarness(t)
				admin := h.adminSession()
				owner := h.openScope(t, admin, "connection-change")
				for _, name := range []string{"connection-a", "connection-b"} {
					h.ok("POST", "/credentials", admin, map[string]any{"credential_name": name, "credential_info": map[string]any{"custom_llm_provider": "openai"}, "credential_values": map[string]any{"api_base": up.URL + "/v1", "api_key": "sk-" + name}})
				}
				params := map[string]any{"model": "original-upstream", "custom_llm_provider": "openai", "litellm_credential_name": "connection-a", "input_cost_per_token": testInputRate, "output_cost_per_token": testOutputRate}
				saved := h.ok("POST", "/model/new", admin, map[string]any{"model_name": "connection-change", "litellm_params": params, "model_info": map[string]any{"transport": "bypass_openai_responses", "pricing_source": "manual", "endpoint_types": []string{"responses", "bypass:openai-responses"}}}).json()
				id := saved["model_info"].(map[string]any)["id"].(string)
				headers := map[string]string{"X-Session-Id": "connection-change-session"}
				first := h.doHeaders("POST", path, owner.key, map[string]any{"model": "connection-change", "input": "Hello"}, headers)
				if first.status != 200 {
					t.Fatal(first.describe())
				}
				previous := map[string]any{"model": "connection-change", "previous_response_id": first.json()["id"], "input": "Continue"}
				valid := h.doHeaders("POST", path, owner.key, previous, headers)
				if valid.status != 200 || calls.Load() != 2 {
					t.Fatalf("原连接不能正常续接: %s", valid.describe())
				}
				full := map[string]any{"model": "connection-change", "input": append([]any{map[string]any{"role": "user", "content": "Hello"}}, first.json()["output"].([]any)...)}
				switch mutation {
				case "api_base":
					params["api_base"] = up.URL + "/changed/v1"
				case "model":
					params["model"] = "changed-upstream"
				case "credential":
					params["litellm_credential_name"] = "connection-b"
				case "credential_values":
					h.ok("PATCH", "/credentials/connection-a", admin, map[string]any{"credential_values": map[string]any{"api_base": up.URL + "/v1", "api_key": "sk-changed-account"}})
				}
				if mutation != "credential_values" {
					h.ok("PATCH", "/model/"+id+"/update", admin, map[string]any{"litellm_params": params})
				}
				expectedCalls := int32(2)
				for index, body := range []map[string]any{previous, full} {
					expectedStatus := 409
					if index == 1 {
						expectedStatus = 200
						expectedCalls++
					}
					blocked := h.doHeaders("POST", path, owner.key, body, headers)
					if blocked.status != expectedStatus || calls.Load() != expectedCalls {
						t.Fatalf("连接变化后历史处理不符: %s %s", mutation, blocked.describe())
					}
				}
				if bad := h.doHeaders("POST", path, "sk-invalid", previous, headers); bad.status != 401 || calls.Load() != expectedCalls {
					t.Fatalf("未授权续接没有拒绝: %s", bad.describe())
				}
				// 完整历史由客户端提供，两种入口均可重新选路；父响应游标仍绑定原连接，普通请求不能替它重新签发归属。
				fresh := h.doHeaders("POST", path, owner.key, map[string]any{"model": "connection-change", "input": "A fresh plain request"}, headers)
				expectedCalls++
				if fresh.status != 200 || calls.Load() != expectedCalls {
					t.Fatalf("普通请求不能重新选路: %s", fresh.describe())
				}
				expectedStatus := 200
				expectedCalls++
				blocked := h.doHeaders("POST", path, owner.key, full, headers)
				if blocked.status != expectedStatus || calls.Load() != expectedCalls {
					t.Fatalf("完整历史不能正常续接: %s", blocked.describe())
				}
				// 普通请求成功不能重绑旧游标；仍必须拒绝从新连接读取原响应。
				got := h.doHeaders("POST", path, owner.key, previous, headers)
				if got.status != 409 || calls.Load() != expectedCalls {
					t.Fatalf("旧游标被普通请求重新授权: %s", got.describe())
				}
				detail := logDetail(t, h, owner.session, got.header("x-litellm-call-id"))
				route := detail["metadata"].(map[string]any)["upstream_response"].(map[string]any)["routing"].(map[string]any)
				if route["required_deployment_reason"] != "connection_identity_changed_or_missing" {
					t.Fatalf("连接改变的拒绝缺少原因: %v", route)
				}
				h.flushSpend()
				for _, response := range []reply{first, valid, fresh} {
					bill := h.ok("GET", "/spend/logs/ui/"+response.header("x-litellm-call-id"), admin, nil).json()
					if bill["status"] != "success" || bill["api_key"] != owner.keyID {
						t.Fatalf("连接检查影响记账归属: %v", bill)
					}
				}
			})
		}
	}
}
