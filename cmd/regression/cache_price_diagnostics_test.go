package regression

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
