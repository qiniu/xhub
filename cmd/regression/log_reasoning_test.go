package regression

import (
	"fmt"
	"strings"
	"testing"
)

// TestLogReasoningEffort 验证三种入站协议经真实 Chat 上游桥接后，关闭提示词仍能读取推理强度与精确费用。
// 覆盖同步/流式、未指定、失败、日志列表/详情及跨用户拒绝；前置私有数据库和本地上游，harness 回收全部资源。
func TestLogReasoningEffort(t *testing.T) {
	dep := chatDeployment("log-reasoning")
	h := newHarness(t, dep)
	h.gw.Cfg.GeneralSettings.StorePromptsInSpendLogs = false
	admin := h.personalBillingSession()
	for _, protocol := range []string{"chat", "responses", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				path := "/v1/chat/completions"
				body := map[string]any{"model": dep.ModelName, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "Verify private reasoning metadata."}}, "reasoning_effort": "high"}
				if protocol == "responses" {
					path = "/v1/responses"
					delete(body, "messages")
					delete(body, "reasoning_effort")
					body["input"], body["reasoning"], body["store"] = "Verify private reasoning metadata.", map[string]any{"effort": "high"}, false
				}
				if protocol == "messages" {
					path = "/v1/messages"
					delete(body, "reasoning_effort")
					body["max_tokens"], body["output_config"] = 32, map[string]any{"effort": "high"}
				}
				response := h.ok("POST", path, admin, body)
				h.flushSpend()
				callID := response.headers.Get("x-litellm-call-id")
				row := h.ok("GET", "/spend/logs/ui/"+callID, admin, nil).json()
				if row["reasoning_effort"] != "high" || row["spend"].(float64) <= 0 {
					t.Fatal("真实调用丢失推理强度或费用", row)
				}
				stored, err := h.db.GetRequestLog(t.Context(), callID)
				if err != nil || stored == nil || stored.RequestBody != "" || stored.ProxyRequest != "" || stored.ResponseBody != "" {
					t.Fatalf("关闭提示词保存后正文仍被落库: %+v err=%v", stored, err)
				}
				listed := listField(h.ok("GET", "/spend/logs/ui?request_id="+callID, admin, nil).json(), "data")
				if len(listed) != 1 || listed[0]["reasoning_effort"] != "high" || listed[0]["spend"] != row["spend"] {
					t.Fatal("列表与详情元数据或费用不一致", listed)
				}
			})
		}
	}
	response := h.ok("POST", "/v1/chat/completions", admin, map[string]any{"model": dep.ModelName, "messages": []any{map[string]any{"role": "user", "content": "Verify unspecified effort."}}})
	h.flushSpend()
	plain := h.ok("GET", "/spend/logs/ui/"+response.headers.Get("x-litellm-call-id"), admin, nil).json()
	if plain["reasoning_effort"] != "" {
		t.Fatal("没有指定强度时不能推断默认值", plain)
	}
	h.mu.Lock()
	h.failing = true
	h.mu.Unlock()
	failed := h.do("POST", "/v1/chat/completions", admin, map[string]any{"model": dep.ModelName, "reasoning_effort": "none", "messages": []any{map[string]any{"role": "user", "content": "Verify failed call metadata."}}})
	if failed.status < 400 {
		t.Fatal("失败夹具未生效", failed.describe())
	}
	h.flushSpend()
	failedID := failed.headers.Get("x-litellm-call-id")
	row := h.ok("GET", "/spend/logs/ui/"+failedID, admin, nil).json()
	if row["reasoning_effort"] != "none" || row["spend"] != float64(0) {
		t.Fatal("失败调用应保留明确强度但不能收费", row)
	}
	user := h.provision(t, admin, "reasoning-foreign")
	denied := h.do("GET", "/spend/logs/ui/"+failedID, user.session, nil)
	if denied.status != 404 || strings.Contains(denied.text(), "reasoning_effort") {
		t.Fatal("其他账号能读取推理元数据", denied.describe())
	}
}
