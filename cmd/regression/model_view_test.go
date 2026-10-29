package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestModelViewHTTPContract 验证标准/原生真实请求期间停用模型不会污染旧目录，后续请求立即拒绝，恢复及凭据轮换可见。
// 前置隔离 PostgreSQL、真实路由和本地 HTTP 供应商；通道确定请求已到上游后再更新，不用 sleep 制造竞态。
// 检查真实响应、出站型号/凭据、错误零调用、账单及日志；释放屏障后 harness 清理模型、用户、凭据和 schema。
func TestModelViewHTTPContract(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/bypass/openai/v1/chat/completions"} {
		t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var calls atomic.Int32
			h, owner, admin, id := historyHarness(t, "chat", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				expectedModel, expectedKey := "wire-before", "local-key"
				if n > 1 {
					expectedModel, expectedKey = "wire-after", "rotated-key"
				}
				if body["model"] != expectedModel || r.Header.Get("Authorization") != "Bearer "+expectedKey {
					t.Errorf("热更新未反映到真实出站: model=%v", body["model"])
				}
				if n == 1 {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				writeJSON(w, map[string]any{"id": fmt.Sprintf("chat_view_%d", n), "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "View accepted"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10}})
			}))
			t.Cleanup(unblock)
			h.ok("POST", "/model/update", admin, map[string]any{"id": id, "litellm_params": map[string]any{"model": "wire-before"}, "model_info": map[string]any{"endpoint_types": []string{"chat", "bypass:openai-chat"}}})
			before := h.moneyOf(t, owner)
			view := h.gw.Models()
			body := map[string]any{"model": "history-model", "messages": []any{map[string]any{"role": "user", "content": "Reply briefly."}}}
			done := make(chan reply, 1)
			go func() { defer close(done); done <- h.do("POST", path, owner.key, body) }()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("请求未进入本地上游")
			}
			h.ok("POST", "/model/disable", admin, map[string]any{"id": id})
			if len(view) != 1 || view[0].Disabled() {
				t.Error("真实管理停用污染已经取得的请求目录")
			}
			if !h.gw.Models()[0].Disabled() {
				t.Error("后续目录未看到停用")
			}
			rejected := h.do("POST", path, owner.key, body)
			if rejected.status < 400 || !strings.Contains(rejected.text(), "error") || calls.Load() != 1 {
				t.Fatalf("停用后仍调用上游: %s calls=%d", rejected.describe(), calls.Load())
			}
			unblock()
			var got reply
			select {
			case got = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("释放上游后旧请求未结束")
			}
			if got.status != 200 || !strings.Contains(got.text(), "View accepted") {
				t.Fatalf("已开始请求被目录更新破坏: %s", got.describe())
			}
			h.flushSpend()
			log := h.ok("GET", "/spend/logs/ui/"+got.header("x-litellm-call-id"), admin, nil).json()
			if log["status"] != "success" || !h.moneyOf(t, owner).grewBy(before, 8*testInputRate+2*testOutputRate) {
				t.Fatalf("停用期间已完成请求的费用/日志错误: %v", log)
			}
			h.ok("POST", "/model/enable", admin, map[string]any{"id": id})
			h.ok("POST", "/model/update", admin, map[string]any{"id": id, "litellm_params": map[string]any{"model": "wire-after"}})
			// PATCH 保留未提交的根地址，仅轮换密钥；实际供应商验证 Authorization。
			h.ok("PATCH", "/credentials/history-credential", admin, map[string]any{"credential_values": map[string]any{"api_key": "rotated-key"}})
			got = h.do("POST", path, owner.key, body)
			if got.status != 200 || calls.Load() != 2 || !strings.Contains(got.text(), "View accepted") {
				t.Fatalf("恢复/轮换后的真实请求失败: %s", got.describe())
			}
			if !h.moneyOf(t, owner).grewBy(before, 2*(8*testInputRate+2*testOutputRate)) {
				t.Fatal("恢复后消费不正确或拒绝请求被扣费")
			}
		})
	}
}

// TestModelViewRetryChecks 验证目录固定后仍在重试前读取真实密钥状态和预算，不把配置快照当作授权快照。
// 前置真实数据库、管理员接口和本地上游；首次失败回调撤销密钥或耗尽预算；断言无第二次出站及无新增费用。
// 两种入口分别测试；harness 最终回收模型、模板、身份、账单与 schema，不接外部供应商。
func TestModelViewRetryChecks(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/bypass/openai/v1/chat/completions"} {
		for _, change := range []string{"revoke", "budget"} {
			t.Run(strings.TrimPrefix(path, "/")+"/"+change, func(t *testing.T) {
				h := newHarness(t)
				admin := h.adminSession()
				owner := h.openScope(t, admin, "view-retry")
				h.addDBModel(t, admin, "view-retry", "view-wire", "view-dep", nil)
				h.ok("POST", "/model/update", admin, map[string]any{"id": "view-dep", "model_info": map[string]any{"endpoint_types": []string{"chat", "bypass:openai-chat"}}})
				template := routeTemplate(t, h, admin, "view retry checks", routeTemplateBody([]any{}, 3, 60, 0, 0))
				bindTemplate(t, h, admin, "key", owner.keyID, template)
				h.ok("POST", path, owner.key, chatRequest("view-retry", "Seed real usage."))
				h.flushSpend()
				before := h.moneyOf(t, owner)
				h.scriptStatus("view-wire", 500)
				h.onUpstream("view-wire", func() {
					if change == "revoke" {
						h.ok("POST", "/key/delete", admin, map[string]any{"keys": []string{owner.key}})
					} else {
						h.setProjectBudget(t, admin, owner.projectID, before.project)
					}
				})
				defer h.onUpstream("view-wire", nil)
				h.resetUpstream()
				got := h.do("POST", path, owner.key, chatRequest("view-retry", "Check live authorization."))
				want := 401
				if change == "budget" {
					want = 403
				}
				if got.status != want || !strings.Contains(got.text(), "error") {
					t.Fatalf("重试前未阻断 %s: %s", change, got.describe())
				}
				if len(h.upstreamCalls()) != 1 {
					t.Fatalf("%s 后仍执行重试: %v", change, h.upstreamCalls())
				}
				h.flushSpend()
				if change == "revoke" {
					// 密钥行已删除，不能再用要求五行都存在的 moneyOf；检查仍存在的用户消费。
					user, err := h.db.GetUser(t.Context(), owner.userID)
					if err != nil || user == nil {
						t.Fatalf("读取撤销后的用户失败: %v", err)
					}
					if !nearlyEqual(user.Spend, before.user) {
						t.Fatal("撤销拒绝产生额外用户费用")
					}
				} else if after := h.moneyOf(t, owner); !after.same(before) {
					t.Fatalf("预算拒绝产生额外费用: %+v -> %+v", before, after)
				}
			})
		}
	}
}
