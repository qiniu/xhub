package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestModelViewPricingConnection 验证已出站请求固定价格和连接，更新后新请求使用新地址、型号、密钥和费率。
// 前置真实 HTTP/隔离 PostgreSQL；覆盖标准与 Bypass 的 Chat/Responses，以及有限和不限密钥预算。
// 屏障确保管理 API 在旧请求执行期间更新；检查真实出站、各级消费、日志、旧游标拒绝和零额外调用。
// 不修改权限/预算规则；Cleanup 释放屏障、关闭两个本地上游，harness 删除所有测试数据。
func TestModelViewPricingConnection(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		for _, bypass := range []bool{false, true} {
			for _, limited := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/bypass=%t/limited=%t", protocol, bypass, limited), func(t *testing.T) {
					entered, release := make(chan struct{}), make(chan struct{})
					var unblockOnce sync.Once
					unblock := func() { unblockOnce.Do(func() { close(release) }) }
					var oldCalls, newCalls atomic.Int32
					// handler 按 old 选择旧/新上游处理器，返回可阻塞的 HTTP 供应商夹具；不模拟网关业务。
					handler := func(old bool) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							var body map[string]any
							if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
								t.Error(err)
								w.WriteHeader(http.StatusBadRequest)
								return
							}
							side, key := "after", "rotated-key"
							var n int32
							if old {
								side, key, n = "before", "local-key", oldCalls.Add(1)
							} else {
								n = newCalls.Add(1)
							}
							if body["model"] != "wire-"+side || r.Header.Get("Authorization") != "Bearer "+key {
								t.Errorf("%s 连接收到错误型号或密钥: model=%v", side, body["model"])
							}
							if old && n == 1 {
								close(entered)
								select {
								case <-release:
								case <-r.Context().Done():
									return
								}
							}
							if protocol == "responses" {
								writeAgentResponse(w, nativeAgentResponse(fmt.Sprintf("resp_price_%s_%d", side, n), "Price accepted", true, nil), false)
							} else {
								writeJSON(w, map[string]any{"id": fmt.Sprintf("chat_price_%s_%d", side, n), "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Price accepted"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10}})
							}
						})
					}
					h, owner, admin, id := historyHarness(t, "chat", handler(true))
					t.Cleanup(unblock)
					next := httptest.NewServer(handler(false))
					t.Cleanup(next.Close)
					path, transport, endpoint := "/v1/chat/completions", "bypass_openai_chat", "openai-chat"
					body := chatRequest("history-model", "Keep the original price while this request runs.")
					if protocol == "responses" {
						path, transport, endpoint = "/v1/responses", "bypass_openai_responses", "openai-responses"
						body = map[string]any{"model": "history-model", "input": "Keep the original price while this request runs."}
					}
					if bypass {
						path = "/bypass/openai" + path
					}
					h.ok("POST", "/model/update", admin, map[string]any{"id": id, "litellm_params": map[string]any{"model": "wire-before"}, "model_info": map[string]any{"transport": transport, "endpoint_types": []string{protocol, "bypass:" + endpoint}}})
					budget := any(nil)
					if limited {
						budget = 1.0
					}
					h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "max_budget": budget})
					key, err := h.db.GetKey(t.Context(), owner.keyID)
					if err != nil || key == nil {
						t.Fatalf("无法核实测试预算前置条件: %v", err)
					}
					if (limited && (key.MaxBudget == nil || *key.MaxBudget != 1)) || (!limited && key.MaxBudget != nil) {
						t.Fatalf("有限/不限密钥预算未实际保存: limited=%t budget=%v", limited, key.MaxBudget)
					}
					before := h.moneyOf(t, owner)
					headers := map[string]string{"X-Session-Id": "price-update-session"}
					done := make(chan reply, 1)
					go func() { done <- h.doHeaders("POST", path, owner.key, body, headers) }()
					select {
					case <-entered:
					case <-time.After(10 * time.Second):
						t.Fatal("旧连接没有收到在途请求")
					}
					// 两次管理操作各自提交，不假装跨模型和凭据库存在原子事务。
					h.ok("POST", "/model/update", admin, map[string]any{"id": id, "litellm_params": map[string]any{"model": "wire-after", "input_cost_per_token": 10 * testInputRate, "output_cost_per_token": 10 * testOutputRate}})
					h.ok("PATCH", "/credentials/history-credential", admin, map[string]any{"credential_values": map[string]any{"api_base": next.URL + "/v1", "api_key": "rotated-key"}})
					// 新请求在旧请求结束前完成，可发现全局价格/连接状态被并发覆盖的问题。
					fresh := h.doHeaders("POST", path, owner.key, body, headers)
					if fresh.status != 200 || !strings.Contains(fresh.text(), "Price accepted") || oldCalls.Load() != 1 || newCalls.Load() != 1 {
						t.Fatalf("更新后请求未使用新连接: %s old=%d new=%d", fresh.describe(), oldCalls.Load(), newCalls.Load())
					}
					unit := 8*testInputRate + 2*testOutputRate
					h.flushSpend()
					if !h.moneyOf(t, owner).grewBy(before, 10*unit) {
						t.Fatal("新请求没有按新价格结算，或未结束的请求被提前扣费")
					}
					unblock()
					var old reply
					select {
					case old = <-done:
					case <-time.After(10 * time.Second):
						t.Fatal("释放屏障后旧请求未完成")
					}
					if old.status != 200 || !strings.Contains(old.text(), "Price accepted") {
						t.Fatalf("更新破坏了旧请求: %s", old.describe())
					}
					h.flushSpend()
					if !h.moneyOf(t, owner).grewBy(before, 11*unit) {
						t.Fatal("价格热更新改变旧账单或并发请求串账")
					}
					for i, result := range []reply{old, fresh} {
						callID := result.header("x-litellm-call-id")
						if callID == "" {
							t.Fatal("真实响应缺少日志 ID")
						}
						log := h.ok("GET", "/spend/logs/ui/"+callID, admin, nil).json()
						spend, ok := log["spend"].(float64)
						want := unit
						if i == 1 {
							want *= 10
						}
						if log["status"] != "success" || !ok || !nearlyEqual(spend, want) {
							t.Fatalf("第 %d 个请求日志与实际价格不一致: status=%v spend=%v want=%g", i, log["status"], log["spend"], want)
						}
					}
					if protocol == "responses" {
						denied := h.doHeaders("POST", path, owner.key, map[string]any{"model": "history-model", "previous_response_id": old.json()["id"], "input": "Continue on the old connection."}, headers)
						if denied.status != 409 || !strings.Contains(denied.text(), "error") || oldCalls.Load() != 1 || newCalls.Load() != 1 {
							t.Fatalf("迟到旧响应绕过连接归属校验: %s old=%d new=%d", denied.describe(), oldCalls.Load(), newCalls.Load())
						}
						h.flushSpend()
						if !h.moneyOf(t, owner).grewBy(before, 11*unit) {
							t.Fatal("拒绝旧连接历史仍产生费用")
						}
					}
				})
			}
		}
	}
}
