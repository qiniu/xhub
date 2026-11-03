package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCachePriceDiagnostics 真实HTTP/PG验证缺缓存价格的成功对话仍标未定价，补价后准确收费。
// 本地上游报告8输入、2输出、4缓存读取；缺价、零价、正常价独立部署，私有schema和连接自动清理。
func TestCachePriceDiagnostics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"cache-price","choices":[{"message":{"role":"assistant","content":"PRICE_OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":4}}}`)
	}))
	defer upstream.Close()
	for _, tc := range []struct {
		name  string
		price any
		want  float64
	}{{"missing", nil, 0}, {"zero", float64(0), 0.000008}, {"priced", 0.000001, 0.000012}} {
		t.Run(tc.name, func(t *testing.T) {
			dep := chatDeployment("price-" + tc.name)
			dep.LiteLLMParams["api_base"] = upstream.URL
			dep.LiteLLMParams["input_cost_per_token"] = 0.000001
			dep.LiteLLMParams["output_cost_per_token"] = 0.000002
			if tc.price != nil {
				dep.LiteLLMParams["cache_read_input_token_cost"] = tc.price
			}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "price-check")
			got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest(dep.ModelName, "Verify cache pricing."))
			h.flushSpend()
			row := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), owner.session, nil).json()
			cost := row["metadata"].(map[string]any)["cost_breakdown"].(map[string]any)
			if !nearlyEqual(parseFloatOrZero(fmt.Sprint(row["spend"])), tc.want) {
				t.Fatal("实际账单错误", row)
			}
			if tc.price == nil {
				usage := cost["usage"].(map[string]any)
				if cost["pricing_status"] != "unpriced" || usage["PricingBlocked"] != "cache_read_price_missing" {
					t.Fatal("缺价原因未保留", cost)
				}
			} else if cost["pricing_status"] != "priced" {
				t.Fatal("明确零价或正常价被误判缺价", cost)
			}
		})
	}
}

// TestCachePriceDiagnosticBoundaries 使用真实网关、隔离数据库和本地供应商验证分桶缺价及上游失败的账单提示。
// t 为回归上下文；验证失败不误报缺价，成功分桶缺价准确说明；服务与私有 schema 由清理函数回收。
func TestCachePriceDiagnosticBoundaries(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%v", failure), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failure {
					w.WriteHeader(http.StatusTooManyRequests)
					fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"Try later"}}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_price", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "PRICE_OK"}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 8, "output_tokens": 2, "cache_creation_input_tokens": 3, "cache_creation": map[string]any{"ephemeral_5m_input_tokens": 3, "ephemeral_1h_input_tokens": 0}}})
			}))
			t.Cleanup(upstream.Close)
			dep := hardBudgetDeployment(fmt.Sprintf("price-boundary-%v", failure))
			dep.LiteLLMParams["api_base"] = upstream.URL
			dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
			dep.LiteLLMParams["input_cost_per_token"] = 1e-6
			dep.LiteLLMParams["output_cost_per_token"] = 2e-6
			delete(dep.LiteLLMParams, "cache_creation_input_token_cost")
			dep.ModelInfo["transport"] = "bypass_anthropic_messages"
			dep.ModelInfo["endpoint_types"] = []string{"messages"}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, dep.ModelName)
			body := chatRequest(dep.ModelName, "Verify pricing diagnostic.")
			body["max_tokens"] = 32
			response := h.do("POST", "/v1/messages", owner.key, body)
			if failure && response.status != 429 || !failure && response.status != 200 {
				t.Fatal("上游状态未按预期返回", response.describe())
			}
			h.flushSpend()
			row := h.ok("GET", "/spend/logs/ui/"+response.header("x-litellm-call-id"), owner.session, nil).json()
			if failure {
				if strings.Contains(fmt.Sprint(row["metadata"]), "pricing_unavailable") || strings.Contains(fmt.Sprint(row["metadata"]), "unpriced") || parseFloatOrZero(fmt.Sprint(row["spend"])) != 0 {
					t.Fatalf("不计费的上游失败被错误标为缺价或产生费用: %v", row)
				}
				return
			}
			cost := row["metadata"].(map[string]any)["cost_breakdown"].(map[string]any)
			usage := cost["usage"].(map[string]any)
			if usage["PricingBlocked"] != "cache_write_price_missing" {
				t.Fatalf("分桶写入缺价原因不明确: %v", cost)
			}
		})
	}
}
