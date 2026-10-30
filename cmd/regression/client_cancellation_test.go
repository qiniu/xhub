package regression

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/router"
)

// cancellationRows 读取指定模型的真实持久化用量行；用于取消/重试完成后的同步点，不将未入库当作零费用成功。
func cancellationRows(t *testing.T, h *harness, admin, model string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.flushSpend()
		r := h.ok("GET", "/spend/logs/ui?page=1&page_size=200", admin, nil)
		rows := []map[string]any{}
		for _, row := range rowsOf(r, "data", "logs") {
			if stringField(row, "model") == model {
				rows = append(rows, row)
			}
		}
		if len(rows) >= want {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("model %s only has %d logs, want %d", model, len(rows), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestClientCancellationDoesNotCoolDeployment 验证用户主动取消不会成为部署健康失败，且不应影响另一租户访问健康模型。
// 参数t为测试上下文；每例使用真实HTTP、私有PostgreSQL和专用随机端口Redis；覆盖首帧前/后与禁用冷却控制。
// 所有上游均为本地夹具，取消由真实request context传播；日志落库后再观察冷却，不依赖固定等待；cleanup回收全部资源。
func TestClientCancellationDoesNotCoolDeployment(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, phase := range []string{"before_headers", "after_frame"} {
			for _, enabled := range []bool{true, false} {
				name := fmt.Sprintf("%s_cooldown_%v_native_%v", phase, enabled, native)
				path := "/v1/chat/completions"
				if native {
					path = "/bypass/openai/v1/chat/completions"
				}
				t.Run(name, func(t *testing.T) {
					rdb := isolatedSpendRedis(t)
					var mode, calls atomic.Int32
					started := make(chan struct{}, 10)
					stopped := make(chan struct{}, 10)
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						_, _ = io.Copy(io.Discard, r.Body) // 消费请求体后HTTP服务才能可靠检测客户端断连；这是夹具前置条件。
						if mode.Load() == 1 {
							started <- struct{}{}
							if phase == "after_frame" {
								w.Header().Set("Content-Type", "text/event-stream")
								io.WriteString(w, "data: {\"id\":\"chatcmpl_cancel\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2}}\n\n")
								w.(http.Flusher).Flush()
							}
							select {
							case <-r.Context().Done():
							case <-time.After(4 * time.Second):
							}
							stopped <- struct{}{}
							return
						}
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, `{"id":"chatcmpl_ok","choices":[{"index":0,"message":{"role":"assistant","content":"HEALTHY_OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`)
					}))
					t.Cleanup(upstream.Close)
					model := "cancel-health-" + name
					dep := chatDeployment(model)
					dep.LiteLLMParams["api_base"] = upstream.URL
					dep.ModelInfo["id"] = model + "-dep"
					dep.ModelInfo["endpoint_types"] = []string{"chat", "bypass:openai-chat"}
					h := newHarness(t, dep)
					admin := h.adminSession()
					a := h.openScope(t, admin, "cancel-owner")
					b := h.openScope(t, admin, "unrelated-owner")
					retries, failures, cancels := 3, 3, 3
					if phase == "before_headers" {
						retries, cancels = 3, 3
					}
					if !enabled {
						failures = 0
					}
					template := routeTemplate(t, h, admin, "cancel policy", routeTemplateBody([]any{}, retries, 10, failures, 60))
					bindTemplate(t, h, admin, "key", a.keyID, template)
					h.ok("POST", "/v1/chat/completions", b.key, chatRequest(model, "Before cancellation"))
					cancellationRows(t, h, admin, model, 1)
					mode.Store(1)
					for n := 0; n < cancels; n++ {
						ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
						body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Generate a long answer"}],"stream":true}`, model)
						req, _ := http.NewRequestWithContext(ctx, "POST", h.server.URL+path, strings.NewReader(body))
						req.Header.Set("Authorization", "Bearer "+a.key)
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("X-Session-Id", "cancel-health-session")
						type outcome struct {
							response *http.Response
							err      error
						}
						done := make(chan outcome, 1)
						go func() { r, e := h.server.Client().Do(req); done <- outcome{r, e} }()
						select {
						case <-started:
						case <-time.After(3 * time.Second):
							cancel()
							t.Fatal("local upstream not reached")
						}
						if phase == "after_frame" {
							o := <-done
							if o.err != nil {
								cancel()
								t.Fatal(o.err)
							}
							buffer := make([]byte, 1)
							if !strings.Contains(o.response.Header.Get("Cache-Control"), "no-transform") {
								cancel()
								t.Fatal("真实标准/bypass流缺少代理防缓冲头")
							}
							if _, err := o.response.Body.Read(buffer); err != nil {
								cancel()
								t.Fatal(err)
							}
							cancel()
							_ = o.response.Body.Close()
						} else {
							cancel()
							o := <-done
							if o.err == nil {
								if o.response != nil {
									_ = o.response.Body.Close()
								}
								t.Error("cancel before headers unexpectedly succeeded")
							}
						}
						select {
						case <-stopped:
						case <-time.After(2 * time.Second):
							t.Fatal("client cancel did not cancel upstream")
						}
						cancellationRows(t, h, admin, model, n+2)
					}
					mode.Store(0)
					id := router.CooldownID(h.gw.Cfg.ModelList[0])
					failureCount, _ := rdb.Get(t.Context(), "xhub:fails:"+id).Int()
					cooldown := rdb.Exists(t.Context(), "xhub:cooldown:"+id).Val()
					before := calls.Load()
					probe := h.do("POST", path, b.key, chatRequest(model, "After another user cancelled"))
					after := calls.Load()
					wantRows := cancels + 1
					if probe.status == 200 {
						wantRows++
					}
					rows := cancellationRows(t, h, admin, model, wantRows)
					cancelled := 0
					for _, row := range rows {
						if stringField(row, "status") == "error" {
							cancelled++
							detail := logDetail(t, h, a.session, stringField(row, "request_id"))
							diagnostic := detail["metadata"].(map[string]any)["upstream_response"].(map[string]any)
							failure, ok := diagnostic["failure"].(map[string]any)
							if !ok || failure["kind"] != "request_canceled" || failure["request_context_error"] != "context canceled" || failure["cancellation_source"] != "unknown" || failure["transport_error"] == "" {
								t.Fatalf("取消证据缺失或擅自归因用户: %v", diagnostic)
							}
							if strings.Contains(fmt.Sprint(detail["error"]), "data:") || !strings.Contains(fmt.Sprint(detail["error"]), `"upstream_error":null`) {
								t.Fatalf("部分流被当成错误或虚构供应商错误: %v", detail["error"])
							}
							if spend, ok := floatField(row, "spend"); !ok || spend != 0 {
								t.Errorf("canceled call charged: %v", row)
							}
						}
					}
					if cancelled != cancels || failureCount != 0 || cooldown != 0 {
						t.Errorf("cancel facts: rows=%d failures=%d cooldown=%d", cancelled, failureCount, cooldown)
					}
					if after-before != 1 || probe.status != 200 {
						t.Errorf("client cancellation blocked unrelated tenant: HTTP %d external=%d failure_count=%d", probe.status, after-before, failureCount)
					}
					if calls.Load() != int32(1+cancels)+after-before {
						t.Errorf("cancelled requests contacted upstream more than once: %d", calls.Load())
					}
				})
			}
		}
	}
}
