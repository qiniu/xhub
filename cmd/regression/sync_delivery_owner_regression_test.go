package regression

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSyncBypassDeliveryModelWhitespace 验证透传模型名首尾空格不会使成功账单与预先登记的归属冲突。
// 使用隔离 PostgreSQL 和本机模拟上游，核对真实响应、日志与费用；测试结束由 harness 清理。
func TestSyncBypassDeliveryModelWhitespace(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", "")
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := chatDeployment("sync-trim")
	dep.ModelInfo["transport"] = "bypass_openai_chat"
	dep.ModelInfo["endpoint_types"] = []string{"bypass:openai-chat"}
	h := newHarness(t, dep)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "sync-trim")
	before := h.moneyOf(t, owner)
	response := h.ok("POST", "/bypass/openai/v1/chat/completions", owner.key, map[string]any{
		"model": "sync-trim ", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}, "stream": false,
	})
	id := response.header("x-litellm-call-id")
	h.flushSpend()
	after := h.moneyOf(t, owner)
	if !after.grewBy(before, expectedCost()) {
		t.Fatalf("带空格模型名的成功请求未正确结算: before=%+v after=%+v", before, after)
	}
	log := logDetail(t, h, admin, id)
	if log["status"] != "success" {
		t.Fatalf("成功调用应有对应的成功账单: status=%v", log["status"])
	}
}

// flushFailureWriter 模拟正文已进入服务器缓冲区，但将其刷新至客户端时失败的响应写入器。
type flushFailureWriter struct{ *httptest.ResponseRecorder }

// FlushError 注入连接刷新失败；不访问真实客户端，供同步交付回归使用。
func (w *flushFailureWriter) FlushError() error { return errors.New("test client flush failure") }

// TestSyncFlushFailureCostsZero 验证小响应写入缓冲成功、实际刷新失败时不能记成功及扣费。
// 使用隔离 PostgreSQL、本机上游和注入式刷新错误，核对日志及密钥余额；夹具自动清理。
func TestSyncFlushFailureCostsZero(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", "")
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	h := newHarness(t, chatDeployment("sync-flush-fail"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "sync-flush-fail")
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"sync-flush-fail","messages":[{"role":"user","content":"Hello"}]}`))
	r.Header.Set("Authorization", "Bearer "+owner.key)
	r.Header.Set("Content-Type", "application/json")
	w := &flushFailureWriter{httptest.NewRecorder()}
	h.gw.Handler().ServeHTTP(w, r)
	log := logDetail(t, h, admin, w.Header().Get("x-litellm-call-id"))
	if log["status"] != "error" || log["spend"] != float64(0) || len(h.upstreamCalls()) != 1 {
		t.Fatalf("刷新失败应记失败和零费用: status=%v spend=%v calls=%d", log["status"], log["spend"], len(h.upstreamCalls()))
	}
	key, err := h.db.GetKey(t.Context(), owner.keyID)
	if err != nil || key.Spend != 0 {
		t.Fatalf("刷新失败不应扣费: err=%v", err)
	}
}

// TestBufferedBypassUnexpectedSSE 验证请求未声明布尔流式但上游返回 SSE 时，预先登记的账单仍正确确认。
// 使用独立 PostgreSQL、临时磁盘队列与本机上游，检查成功账单和后续请求可用性，资源由夹具清理。
func TestBufferedBypassUnexpectedSSE(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", filepath.Join(t.TempDir(), "spool"))
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chat-1\",\"object\":\"chat.completion.chunk\",\"model\":\"sync-sse\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2,\"total_tokens\":10}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(up.Close)
	dep := chatDeployment("sync-sse")
	dep.LiteLLMParams["api_base"] = up.URL + "/v1"
	dep.ModelInfo["transport"] = "bypass_openai_chat"
	dep.ModelInfo["endpoint_types"] = []string{"bypass:openai-chat"}
	h := newHarness(t, dep)
	t.Cleanup(func() { _ = h.gw.CloseUsageSpool() })
	admin := h.adminSession()
	owner := h.openScope(t, admin, "sync-sse")
	reply := h.do("POST", "/bypass/openai/v1/chat/completions", owner.key, map[string]any{
		"model": "sync-sse", "messages": []any{map[string]any{"role": "user", "content": "Hello"}}, "stream": "true",
	})
	if reply.status != 200 || !strings.Contains(string(reply.body), "Hello") || calls.Load() != 1 {
		t.Fatalf("上游 SSE 未正常交付: status=%d calls=%d", reply.status, calls.Load())
	}
	log := logDetail(t, h, admin, reply.header("x-litellm-call-id"))
	if log["status"] != "success" {
		t.Fatalf("意外流式响应应正常结算: status=%v", log["status"])
	}
	second := h.do("POST", "/bypass/openai/v1/chat/completions", owner.key, map[string]any{
		"model": "sync-sse", "messages": []any{map[string]any{"role": "user", "content": "Again"}}, "stream": "true",
	})
	if second.status != 200 || calls.Load() != 2 {
		t.Fatalf("意外 SSE 不应污染磁盘队列: status=%d calls=%d", second.status, calls.Load())
	}
}
