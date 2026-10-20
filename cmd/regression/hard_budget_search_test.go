package regression

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/gateway/billing"
)

// budgetSearchDeployment 返回本地受信搜索部署；参数为夹具地址，供后台回归使用，不连接外部供应商。
func budgetSearchDeployment(base string) config.ModelEntry {
	dep := hardBudgetDeployment("search-budget")
	dep.LiteLLMParams["api_base"] = base
	dep.LiteLLMParams["search_context_cost_per_query"] = .01
	dep.ModelInfo["transport"] = "bypass_openai_responses"
	dep.ModelInfo["endpoint_types"] = []string{"responses", "bypass:openai-responses"}
	dep.ModelInfo["web_search_budget_profile"] = "openai_responses_search_actions"
	dep.ModelInfo["web_search_budget_source"] = "Local fixture enforces response caps and reports cumulative input and search actions."
	dep.ModelInfo["web_search_budget_input_tokens"] = 2048
	dep.ModelInfo["web_search_budget_max_calls"] = 2
	return dep
}

// budgetSearchItem 构造已完成的公开搜索历史；参数为唯一标识和动作，返回独立对象，无搜索副作用。
func budgetSearchItem(id, action string) map[string]any {
	return map[string]any{"id": id, "type": "web_search_call", "status": "completed", "action": map[string]any{"type": action}}
}

// budgetSearchDocument 根据场景生成上游终态；参数控制搜索数量或矛盾用量，返回完整响应，供同步与流夹具共用。
func budgetSearchDocument(mode string) map[string]any {
	output := []any{map[string]any{"id": "msg_search", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "SEARCH_OK", "annotations": []any{}}}}}
	if mode != "zero" && mode != "history" {
		output = append(output, budgetSearchItem("ws_search", "search"))
	}
	if mode == "open-page" || mode == "over-calls" {
		output = append(output, budgetSearchItem("ws_open", "open_page"))
	}
	usage := map[string]any{"input_tokens": 8, "output_tokens": 4}
	if mode == "conflict" {
		usage["searches"] = 0
	}
	return map[string]any{"id": "resp_search", "object": "response", "status": "completed", "output": output, "usage": usage}
}

// budgetSearchRequest 构造普通文本带可选搜索的实际请求；参数为场景和流模式，返回正文，历史场景不再执行旧搜索。
func budgetSearchRequest(mode string, stream bool) map[string]any {
	body := map[string]any{"model": "search-budget", "input": "Reply briefly using search only if needed.", "tools": []any{map[string]any{"type": "web_search"}}, "max_output_tokens": 32, "stream": stream}
	if mode == "open-page" {
		body["max_tool_calls"] = 2
	}
	if mode == "history" {
		body["input"] = []any{budgetSearchItem("ws_previous", "search"), map[string]any{"role": "user", "content": "Continue using the existing result."}}
	}
	return body
}

// TestHardBudgetSearchSettlement 验证标准及旁路 JSON/SSE 的搜索、零搜索、打开网页与公开历史续轮真实结算。
// 前置本地 HTTP 和私有 PostgreSQL；断言出站 cap、实测账单及占用清零，harness 与 httptest 自动回收资源。
func TestHardBudgetSearchSettlement(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, path := range []string{"/v1/responses", "/bypass/openai/v1/responses"} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"search", "zero", "open-page", "history"} {
				t.Run(fmt.Sprintf("%s/stream=%v/%s", path, stream, mode), func(t *testing.T) {
					var calls atomic.Int32
					received := make(chan map[string]any, 1)
					terminal, _ := json.Marshal(budgetSearchDocument(mode))
					raw := string(terminal)
					if stream {
						raw = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + raw + "}\n\n"
					}
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						var body map[string]any
						json.NewDecoder(r.Body).Decode(&body)
						received <- body
						w.Header().Set("Content-Type", "application/json")
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
						}
						io.WriteString(w, raw)
					}))
					t.Cleanup(up.Close)
					dep := budgetSearchDeployment(up.URL)
					h := newHarness(t, dep)
					admin := h.adminSession()
					owner := h.openScope(t, admin, dep.ModelName)
					body := budgetSearchRequest(mode, stream)
					encoded, _ := json.Marshal(body)
					plan, _, err := billing.StrictBudgetPlan(dep, encoded, "openai-responses", time.Now())
					if err != nil {
						t.Fatal(err)
					}
					h.setKeyBudget(t, admin, owner.key, plan.Amount)
					got := h.ok("POST", path, owner.key, body)
					holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
					if err != nil {
						t.Fatal(err)
					}
					key, err := h.db.GetKey(t.Context(), owner.keyID)
					if err != nil {
						t.Fatal(err)
					}
					want := 8*testInputRate + 4*testOutputRate
					if mode == "search" || mode == "open-page" {
						want += .01
					}
					if len(holds) != 0 || !nearlyEqual(key.Spend, want) {
						t.Fatalf("搜索账单不正确 mode=%s holds=%+v spend=%g want=%g body=%s", mode, holds, key.Spend, want, got.text())
					}
					if calls.Load() != 1 {
						t.Fatalf("搜索不应重复外发 calls=%d", calls.Load())
					}
					sent := <-received
					maxCalls := float64(1)
					if mode == "open-page" {
						maxCalls = 2
					}
					if sent["max_tool_calls"] != maxCalls || sent["max_output_tokens"] != float64(32) {
						t.Fatalf("搜索实际出站缺少预算上限: %+v", sent)
					}
					if !strings.Contains(got.text(), "SEARCH_OK") {
						t.Fatalf("客户端丢失搜索结果: %s", got.text())
					}
					if (mode == "search" || mode == "open-page") && !strings.Contains(got.text(), "web_search_call") {
						t.Fatalf("客户端丢失公开搜索调用，无法续轮: %s", got.text())
					}
					if strings.Contains(path, "/bypass/") && got.text() != raw {
						t.Fatal("原生旁路改写了搜索响应")
					}
				})
			}
		}
	}
}

// TestHardBudgetSearchUncertain 验证超次数和搜索计数矛盾不退款，后续请求耗尽预算时不再外发。
// 前置私有 PostgreSQL、本地同步或流上游；真实两种路由均应保留 uncertain 占用，结束清理服务与 schema。
func TestHardBudgetSearchUncertain(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, path := range []string{"/v1/responses", "/bypass/openai/v1/responses"} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"over-calls", "conflict"} {
				t.Run(fmt.Sprintf("%s/stream=%v/%s", path, stream, mode), func(t *testing.T) {
					var calls atomic.Int32
					terminal, _ := json.Marshal(budgetSearchDocument(mode))
					raw := string(terminal)
					if stream {
						raw = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + raw + "}\n\n"
					}
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						io.Copy(io.Discard, r.Body)
						w.Header().Set("Content-Type", "application/json")
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
						}
						io.WriteString(w, raw)
					}))
					t.Cleanup(up.Close)
					dep := budgetSearchDeployment(up.URL)
					h := newHarness(t, dep)
					admin := h.adminSession()
					owner := h.openScope(t, admin, dep.ModelName)
					body := budgetSearchRequest(mode, stream)
					encoded, _ := json.Marshal(body)
					plan, _, err := billing.StrictBudgetPlan(dep, encoded, "openai-responses", time.Now())
					if err != nil {
						t.Fatal(err)
					}
					h.setKeyBudget(t, admin, owner.key, plan.Amount)
					h.do("POST", path, owner.key, body)
					holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
					if err != nil {
						t.Fatal(err)
					}
					key, err := h.db.GetKey(t.Context(), owner.keyID)
					if err != nil {
						t.Fatal(err)
					}
					if len(holds) != 1 || holds[0].State != "uncertain" || !nearlyEqual(holds[0].Amount, plan.Amount) || key.Spend != 0 {
						t.Fatalf("搜索异常被错误结算或退款 holds=%+v spend=%g", holds, key.Spend)
					}
					body["input"] = "Try another request after uncertain search billing."
					refused := h.do("POST", path, owner.key, body)
					if refused.status != 429 || calls.Load() != 1 {
						t.Fatalf("未知占用允许继续外发 status=%d calls=%d", refused.status, calls.Load())
					}
				})
			}
		}
	}
}

// TestHardBudgetSearchRejectBeforeUpstream 验证余额不足和未声明契约在真实路由拒绝且不调用供应商。
// 前置私有数据库和计数 HTTP 夹具；断言错误类别、零调用与零占用，测试自动清理独立账号/schema。
func TestHardBudgetSearchRejectBeforeUpstream(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, mode := range []string{"insufficient", "unconfigured"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			t.Cleanup(up.Close)
			dep := budgetSearchDeployment(up.URL)
			if mode == "unconfigured" {
				delete(dep.ModelInfo, "web_search_budget_profile")
			}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, dep.ModelName)
			budget := .000001
			if mode == "unconfigured" {
				budget = 1
			}
			h.setKeyBudget(t, admin, owner.key, budget)
			got := h.do("POST", "/v1/responses", owner.key, budgetSearchRequest("search", false))
			want := 429
			if mode == "unconfigured" {
				want = 400
			}
			if got.status != want || calls.Load() != 0 {
				t.Fatalf("应在上游前拒绝 mode=%s status=%d calls=%d body=%s", mode, got.status, calls.Load(), got.text())
			}
			if mode == "unconfigured" && !strings.Contains(got.text(), "unsupported_budget") {
				t.Fatalf("未声明搜索契约没有明确预算错误: %s", got.text())
			}
			holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
			if err != nil || len(holds) != 0 {
				t.Fatalf("拒绝请求不应留下占用 holds=%+v err=%v", holds, err)
			}
		})
	}
}
