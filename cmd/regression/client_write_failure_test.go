package regression

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// rejectedClientWriter 模拟客户端响应正文无法写入；只在网关入口注入故障，上游仍走真实 HTTP。
type rejectedClientWriter struct {
	header http.Header
	status int
	writes int
}

// Header 返回当前请求独有响应头，供真实网关写入调用 ID。
func (w *rejectedClientWriter) Header() http.Header { return w.header }

// WriteHeader 记录已开始的 HTTP 响应；无网络或外部副作用。
func (w *rejectedClientWriter) WriteHeader(status int) { w.status = status }

// Write 模拟客户端连接拒绝写入；用于验证已开始响应后不得假报成功或重试。
func (w *rejectedClientWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("synthetic client write failure")
}

// Flush 实现流式响应接口；测试不需要真实套接字刷新。
func (w *rejectedClientWriter) Flush() {}

// TestCompletionClientWriteFailure 使用真实网关路由、隔离 PG 和本机 HTTP 上游验证客户端写失败。
// 注入拒绝正文的响应写入器；检查上游只调用一次、错误日志及非成功账单，夹具自动清理 schema 和服务。
func TestCompletionClientWriteFailure(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"completion-test\",\"choices\":[{\"text\":\"Hello\",\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(up.Close)
	dep := chatDeployment("client-write-completion")
	dep.LiteLLMParams["api_base"] = up.URL + "/v1"
	dep.ModelInfo["transport"] = "openai_completions"
	dep.ModelInfo["endpoint_types"] = []string{"completion"}
	h := newHarness(t, dep)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "client-write-owner")
	template := routeTemplate(t, h, admin, "client write cooling", routeTemplateBody([]any{}, 3, 10, 1, 60))
	bindTemplate(t, h, admin, "key", owner.keyID, template)
	req := httptest.NewRequest("POST", "/completions", strings.NewReader(`{"model":"client-write-completion","prompt":"Hello","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+owner.key)
	req.Header.Set("Content-Type", "application/json")
	w := &rejectedClientWriter{header: make(http.Header)}
	h.gw.Handler().ServeHTTP(w, req)
	if calls.Load() != 1 || w.writes != 1 || w.status != 200 {
		t.Fatalf("未执行真实流或发生重放: calls=%d writes=%d status=%d", calls.Load(), w.writes, w.status)
	}
	id := w.header.Get("x-litellm-call-id")
	if id == "" {
		t.Fatal("响应缺少调用 ID")
	}
	log := logDetail(t, h, admin, id)
	if log["status"] != "error" {
		t.Fatalf("客户端写入失败被当成成功: %v", log)
	}
	if parseFloatOrZero(fmt.Sprint(log["spend"])) != 0 {
		t.Fatalf("写入失败应按失败规则零计费: %v", log["spend"])
	}
	upstream := log["metadata"].(map[string]any)["upstream_response"].(map[string]any)
	failure := upstream["failure"].(map[string]any)
	if failure["phase"] != "client_write" || failure["kind"] != "client_write_error" {
		t.Fatalf("未区分客户端写入失败: %v", failure)
	}
	if !strings.Contains(fmt.Sprint(log["error"]), "client_write_error") {
		t.Fatalf("错误摘要缺少客户端写入失败类型: %v", log["error"])
	}
	if rows := h.spendLogs(t, admin); len(rows) != 1 {
		t.Fatalf("产生重复日志或账单: %v", rows)
	}
	// 同一部署之后仍应可用：客户端写失败不是供应商故障，不应触发一失败即冷却的配置。
	recovered := h.do("POST", "/completions", owner.key, map[string]any{"model": "client-write-completion", "prompt": "Retry after client failure.", "stream": true})
	if recovered.status != 200 || calls.Load() != 2 {
		t.Fatalf("客户端写失败影响健康部署: calls=%d response=%s", calls.Load(), recovered.describe())
	}
}
