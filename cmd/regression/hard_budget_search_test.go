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
)

// searchTokenDeployment 创建无搜索认证配置的本地部署；参数为上游地址，返回完整token价格，不访问外部供应商。
func searchTokenDeployment(base string) config.ModelEntry {
	dep := hardBudgetDeployment("search-token-only")
	dep.LiteLLMParams["api_base"] = base
	dep.LiteLLMParams["search_context_cost_per_query"] = 1.0 // 故意有工具价格，证明软预算不会额外收费。
	dep.ModelInfo["transport"] = "bypass_openai_responses"
	dep.ModelInfo["endpoint_types"] = []string{"responses", "bypass:openai-responses"}
	return dep
}

// searchTokenItem 构造可继续原生对话的公开搜索项；参数为ID与动作，返回完整已完成对象，不实际联网。
func searchTokenItem(id, action string) map[string]any {
	return map[string]any{"id": id, "type": "web_search_call", "status": "completed", "action": map[string]any{"type": action}}
}

// TestSearchTokenOnlySettlement 验证标准/旁路同步流及modern/preview搜索不被硬预占阻断，完整实测token落账，工具费用排除。
// 前置独立PG和本地上游；含正常多工具与超过请求cap的供应商实际输出，结束自动清理schema/服务器，不调用付费模型。
func TestSearchTokenOnlySettlement(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, path := range []string{"/v1/responses", "/bypass/openai/v1/responses"} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"modern", "preview", "zero", "over-cap", "history"} {
				t.Run(fmt.Sprintf("%s/stream=%v/%s", path, stream, mode), func(t *testing.T) {
					var calls atomic.Int32
					received := make(chan map[string]any, 1)
					output := []any{map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "SEARCH_OK", "annotations": []any{}}}}}
					searches := 0
					if mode != "zero" && mode != "history" {
						output = append(output, searchTokenItem("ws_1", "search"), searchTokenItem("ws_2", "open_page"))
						searches = 2
					}
					ct := 4
					if mode == "over-cap" {
						ct = 9000
					}
					terminal, _ := json.Marshal(map[string]any{"id": "resp_test", "object": "response", "status": "completed", "output": output, "usage": map[string]any{"input_tokens": 8, "output_tokens": ct, "searches": searches, "web_search_calls": searches, "num_server_side_tools_used": searches, "server_side_tool_usage_details": map[string]any{"web_search_calls": searches}, "context_details": map[string]any{"fixture": true}}})
					raw := string(terminal)
					if stream {
						raw = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + raw + "}\n\n"
					}
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						var request map[string]any
						json.NewDecoder(r.Body).Decode(&request)
						received <- request
						w.Header().Set("Content-Type", "application/json")
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
						}
						io.WriteString(w, raw)
					}))
					t.Cleanup(up.Close)
					dep := searchTokenDeployment(up.URL)
					h := newHarness(t, dep)
					admin := h.adminSession()
					owner := h.openScope(t, admin, dep.ModelName)
					// 余额足以准入但远小于旧整窗预占；真实token消费可能超过剩余余额，明确属于软预算。
					h.setKeyBudget(t, admin, owner.key, .000001)
					kind := "web_search"
					if mode == "preview" {
						kind = "web_search_preview"
					}
					body := map[string]any{"model": dep.ModelName, "input": "Reply briefly using search only if needed.", "tools": []any{map[string]any{"type": kind}}, "stream": stream}
					if mode == "over-cap" {
						body["max_output_tokens"] = 32
						body["max_tool_calls"] = 1
					}
					if mode == "history" {
						body["input"] = []any{searchTokenItem("ws_previous", "search"), map[string]any{"role": "user", "content": "Continue the existing result."}}
					}
					got := h.ok("POST", path, owner.key, body)
					sent := <-received
					if mode == "over-cap" {
						if sent["max_output_tokens"] != float64(32) || sent["max_tool_calls"] != float64(1) {
							t.Fatal("客户端明确cap被改写")
						}
					} else if sent["max_output_tokens"] != nil || sent["max_tool_calls"] != nil {
						t.Fatalf("搜索请求被注入内部cap %+v", sent)
					}
					if got.header("X-XHub-Budget-State") != "token_only" || got.header("X-XHub-Budget-Guarantee") != "soft" || got.header("X-XHub-Tool-Costs") != "excluded" {
						t.Fatal("客户端未获知token-only软预算")
					}
					if got.header("X-XHub-Budget-Reservation-Id") != "" {
						t.Fatal("搜索不应创建硬预占")
					}
					holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
					if err != nil {
						t.Fatal(err)
					}
					key, err := h.db.GetKey(t.Context(), owner.keyID)
					if err != nil {
						t.Fatal(err)
					}
					want := 8*testInputRate + float64(ct)*testOutputRate
					if len(holds) != 0 || !nearlyEqual(key.Spend, want) {
						t.Fatalf("token-only账单或占用错误 spend=%g want=%g holds=%+v", key.Spend, want, holds)
					}
					log := h.ok("GET", "/spend/logs/ui/"+got.header("X-Litellm-Call-Id"), admin, nil).text()
					if !strings.Contains(log, "tokens_only") || !strings.Contains(log, "excluded") {
						t.Fatalf("日志未记录费用范围 %s", log)
					}
					if searches > 0 && (!strings.Contains(got.text(), "web_search_calls") || !strings.Contains(log, "web_search_calls")) {
						t.Fatal("搜索原始usage在响应或日志中丢失")
					}
					if !strings.Contains(got.text(), "SEARCH_OK") {
						t.Fatal("客户端未获得文本")
					}
					if strings.Contains(path, "/bypass/") && got.text() != raw {
						t.Fatal("原生搜索响应被改写")
					}
					body["input"] = "Try another request after the budget is exhausted."
					denied := h.do("POST", path, owner.key, body)
					if denied.status != 429 || calls.Load() != 1 {
						t.Fatalf("已耗尽余额仍放行 status=%d calls=%d", denied.status, calls.Load())
					}
				})
			}
		}
	}
}

// TestSearchTokenOnlyRejectOtherTools 验证仅搜索例外不扩大到image/code/server-tool-search；真实准入拒绝且零上游，无遗留占用。
// 前置独立PG/本地计数上游，退出自动回收所有测试资源。
func TestSearchTokenOnlyRejectOtherTools(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, kind := range []string{"image_generation", "code_interpreter", "tool_search"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			t.Cleanup(up.Close)
			dep := searchTokenDeployment(up.URL)
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, dep.ModelName)
			h.setKeyBudget(t, admin, owner.key, 1)
			got := h.do("POST", "/bypass/openai/v1/responses", owner.key, map[string]any{"model": dep.ModelName, "input": "Hello", "tools": []any{map[string]any{"type": "web_search"}, map[string]any{"type": kind}}})
			if got.status != 400 || calls.Load() != 0 {
				t.Fatalf("其他收费工具错误放行 status=%d calls=%d", got.status, calls.Load())
			}
		})
	}
}

// TestSearchTokenOnlyAdmission 验证搜索软预算仍受过期、撤销、模型权限、余额及既有硬预占约束。
// 前置独立schema中的测试身份；直接制造过期/旧uncertain状态后通过真实HTTP验证零上游，历史占用不改动，harness清理。
func TestSearchTokenOnlyAdmission(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, mode := range []string{"expired", "revoked", "model-denied", "exhausted", "reserved", "uncertain", "project-hold"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			t.Cleanup(up.Close)
			dep := searchTokenDeployment(up.URL)
			other := searchTokenDeployment(up.URL)
			other.ModelName = "search-other"
			other.LiteLLMParams["model"] = "search-other"
			h := newHarness(t, dep, other)
			admin := h.adminSession()
			owner := h.openScope(t, admin, dep.ModelName)
			h.setKeyBudget(t, admin, owner.key, 1)
			model := dep.ModelName
			expected := 401
			switch mode {
			case "expired":
				if _, err := h.db.Engine.Exec("UPDATE api_keys SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Hour), owner.keyID); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				h.ok("POST", "/key/delete", admin, map[string]any{"keys": []string{owner.keyID}})
			case "model-denied":
				h.ok("POST", "/key/update", admin, map[string]any{"key": owner.keyID, "models": []string{dep.ModelName}})
				model = other.ModelName
			case "exhausted":
				h.setKeyBudget(t, admin, owner.key, 0)
				expected = 429
			case "project-hold":
				h.setKeyBudget(t, admin, owner.key, 2)
				h.setProjectBudget(t, admin, owner.projectID, 1)
				if _, err := h.db.ReserveBudget(t.Context(), "preexisting-project-hold", "key", owner.keyID, 1); err != nil {
					t.Fatal(err)
				}
				expected = 429
			case "reserved", "uncertain":
				if _, err := h.db.ReserveBudget(t.Context(), "preexisting-hold", "key", owner.keyID, 1); err != nil {
					t.Fatal(err)
				}
				if mode == "uncertain" {
					if err := h.db.SetBudgetReservationState(t.Context(), "preexisting-hold", "uncertain", "Historical fixture must remain unchanged"); err != nil {
						t.Fatal(err)
					}
				}
				expected = 429
			}
			got := h.do("POST", "/v1/responses", owner.key, map[string]any{"model": model, "input": "Search for Go.", "tools": []any{map[string]any{"type": "web_search"}}})
			if got.status != expected || calls.Load() != 0 {
				t.Fatalf("搜索绕过准入 mode=%s status=%d calls=%d body=%s", mode, got.status, calls.Load(), got.text())
			}
			if mode == "reserved" || mode == "uncertain" {
				holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
				if err != nil || len(holds) != 1 || holds[0].State != mode || holds[0].Amount != 1 {
					t.Fatalf("既有占用被修改 %+v err=%v", holds, err)
				}
			}
		})
	}
}

// TestSearchTokenOnlyUnknownUsage 验证缺失或冲突token用量不能伪报免费，也不产生新硬预算占用。
// 标准和旁路同步/流走真实后台，本地上游返回不完整账单；独立数据库及服务器由harness清理。
func TestSearchTokenOnlyUnknownUsage(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, path := range []string{"/v1/responses", "/bypass/openai/v1/responses"} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"missing", "conflict"} {
				t.Run(fmt.Sprintf("%s/%v/%s", path, stream, mode), func(t *testing.T) {
					usage := map[string]any{"input_tokens": 8}
					if mode == "conflict" {
						usage["output_tokens"] = 4
						usage["prompt_tokens"] = 9
					}
					terminal, _ := json.Marshal(map[string]any{"id": "resp_unknown", "status": "completed", "output": []any{}, "usage": usage})
					raw := string(terminal)
					if stream {
						raw = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + raw + "}\n\n"
					}
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
						}
						io.WriteString(w, raw)
					}))
					t.Cleanup(up.Close)
					dep := searchTokenDeployment(up.URL)
					h := newHarness(t, dep)
					admin := h.adminSession()
					owner := h.openScope(t, admin, dep.ModelName)
					h.setKeyBudget(t, admin, owner.key, 1)
					got := h.ok("POST", path, owner.key, map[string]any{"model": dep.ModelName, "input": "Search for Go.", "stream": stream, "tools": []any{map[string]any{"type": "web_search"}}})
					log := h.ok("GET", "/spend/logs/ui/"+got.header("X-Litellm-Call-Id"), admin, nil).text()
					if !strings.Contains(log, "unpriced") {
						t.Fatalf("缺失/冲突token被伪装已定价 %s", log)
					}
					holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
					if err != nil || len(holds) != 0 {
						t.Fatalf("软预算不应新增hold %+v %v", holds, err)
					}
				})
			}
		}
	}
}
