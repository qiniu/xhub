package gateway

import (
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/gateway/billing"
)

// TestSpendQuoteHeadersPreserveExistingBehavior 固定计费成功、缓存、失败与缺价时已有响应头语义。
// 使用内存目录和独立 Server，不执行数据库、Redis 或外部请求；Cleanup 恢复目录。
func TestSpendQuoteHeadersPreserveExistingBehavior(t *testing.T) {
	const model = "test-sync-write-quote"
	catalog.SetModel(model, map[string]any{"rates": []catalog.Rate{{Measure: "token", UnitSize: 1, Side: "input", Window: "all", USD: 0.000002}, {Measure: "token", UnitSize: 1, Side: "output", Window: "all", USD: 0.000008}}})
	t.Cleanup(func() { catalog.RemoveModel(model) })
	usage := map[string]any{"prompt_tokens": 8, "completion_tokens": 2}
	for _, tc := range []struct {
		name, alias string
		status      int
		cache       bool
		want        string
	}{
		{"success", model, 200, false, "0.000032"}, {"cached", model, 200, true, "0"}, {"failure", model, 502, false, "0"}, {"unknown", "test-sync-quote-unknown", 200, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{Cfg: &config.Config{}}
			s.AnnotateCall("quote-call", dpcontract.CallNote{SkipRouteUsage: true})
			w := httptest.NewRecorder()
			s.recordSpend(w, nil, "quote-call", tc.alias, "chat", usage, time.Now(), tc.cache, tc.status, "")
			if got := w.Header().Get("x-litellm-response-cost"); got != tc.want {
				t.Fatalf("费用响应头变化: got=%q want=%q", got, tc.want)
			}
		})
	}
}

// TestQuoteSpendKeepsFrozenPriceAndPendingState 验证报价只读取冻结价格，不提前消费账单状态。
// 使用内存部署和请求临时数据；覆盖成功、失败、缓存及缺失用量，不依赖外部服务或持久资源。
func TestQuoteSpendKeepsFrozenPriceAndPendingState(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dep := config.ModelEntry{ModelName: "quote-model", LiteLLMParams: map[string]any{"model": "quote-upstream", "input_cost_per_token": 0.000002, "output_cost_per_token": 0.000008}, ModelInfo: map[string]any{"pricing_source": "manual"}}
	s := &Server{Cfg: &config.Config{}, budgetCalls: map[string]*budgetAttempt{"quote": {SoftPrice: billing.NewSoftPricePlan(dep, start, false)}}, exchanges: map[string]promptExchange{"quote": {rawResponse: "pending"}}}
	s.AnnotateCall("quote", dpcontract.CallNote{Provider: "provider"})
	// 调价后冻结报价仍使用出站时价格。
	dep.LiteLLMParams["input_cost_per_token"] = 1.0
	usage := map[string]any{"prompt_tokens": 8, "completion_tokens": 2}
	success := s.quoteSpend("quote", "quote-model", "", usage, start, false, 200)
	if !success.priced || !success.softActive || math.Abs(success.total-0.000032) > 1e-12 {
		t.Fatalf("冻结报价错误: %+v", success)
	}
	for _, tc := range []struct {
		status int
		cache  bool
	}{{502, false}, {200, true}} {
		q := s.quoteSpend("quote", "quote-model", "", usage, start, tc.cache, tc.status)
		if q.total != 0 || q.input != 0 || q.output != 0 {
			t.Fatalf("失败或缓存应维持零费用: %+v", q)
		}
	}
	invalid := s.quoteSpend("quote", "quote-model", "", nil, start, false, 200)
	if invalid.priced || invalid.billed.PricingBlocked == "" {
		t.Fatalf("缺少用量不能伪造有效价格: %+v", invalid)
	}
	if s.exchanges["quote"].rawResponse != "pending" || s.callNotes["quote"].Provider != "provider" || s.budgetCalls["quote"] == nil {
		t.Fatal("报价不应消费待结算请求状态")
	}
}
