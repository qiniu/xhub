package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestUpstream4xxPrereadFallback 验证完整的上下文超限错误允许回退，传输截断时不应凭片段回退。
// 使用真实网关、隔离 PostgreSQL、路由配置及本机 HTTP 上游；检查返回状态、实际调用、日志和账务，harness 清理数据。
func TestUpstream4xxPrereadFallback(t *testing.T) {
	var truncated atomic.Bool
	var primary, backup atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if body["model"] == "fail-context" {
			primary.Add(1)
			data := []byte(`{"error":{"code":"context_length_exceeded","message":"Input is too long"}}`)
			if truncated.Load() {
				w.Header().Set("Content-Length", fmt.Sprint(len(data)+100))
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(data)
			return
		}
		backup.Add(1)
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"chat-fallback\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Fallback works\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat-fallback\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		writeJSON(w, map[string]any{"id": "chat-fallback", "object": "chat.completion", "model": "good-backup", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Fallback works"}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}})
	}))
	t.Cleanup(up.Close)
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "4xx-preread")
	template := routeTemplate(t, h, admin, "4xx preread", routeTemplateBody([]any{}, 1, 60, 0, 0))
	bindTemplate(t, h, admin, "key", owner.keyID, template)
	for _, m := range []struct{ name, upstream string }{{"preread-primary", "fail-context"}, {"preread-backup", "good-backup"}} {
		h.ok("POST", "/model/new", admin, map[string]any{"model_name": m.name, "litellm_params": map[string]any{"model": m.upstream, "api_base": up.URL, "api_key": "local-fixture", "custom_llm_provider": "custom", "input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}, "model_info": map[string]any{"id": m.name, "transport": "bypass_openai_chat", "endpoint_types": []string{"chat"}, "pricing_source": "manual"}})
	}
	h.ok("PUT", "/model/fallback", admin, map[string]any{"model_name": "preread-primary", "policy": map[string]any{"context_window_fallbacks": []string{"preread-backup"}}})
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			for _, cut := range []bool{false, true} {
				truncated.Store(cut)
				p, b := primary.Load(), backup.Load()
				before := h.moneyOf(t, owner)
				request := chatRequest("preread-primary", fmt.Sprintf("Check error boundary %v %v.", stream, cut))
				request["stream"] = stream
				result := h.do("POST", "/chat/completions", owner.key, request)
				if primary.Load()-p != 1 {
					t.Fatalf("主模型调用次数不正确: %d", primary.Load()-p)
				}
				if cut {
					if result.status != 400 || backup.Load()-b != 0 {
						t.Fatalf("截断的上游错误不应触发回退: %s backup=%d", result.describe(), backup.Load()-b)
					}
					log := logDetail(t, h, admin, result.header("x-litellm-call-id"))
					diag := log["metadata"].(map[string]any)["upstream_response"].(map[string]any)
					if log["status"] != "error" || diag["read_error"] != "unexpected EOF" {
						t.Fatalf("截断的上游错误日志不完整: %v", log)
					}
					h.flushSpend()
					if after := h.moneyOf(t, owner); after.key != before.key {
						t.Fatalf("失败请求改变密钥费用: before=%v after=%v", before, after)
					}
				} else if result.status != 200 || backup.Load()-b != 1 {
					t.Fatalf("完整的上下文超限错误应回退: %s backup=%d", result.describe(), backup.Load()-b)
				}
			}
		})
	}
}
