package regression

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStreamReadFailureDiagnostics 真实上游在首帧后截断chunked连接，两种执行入口应记录502和原始读取原因。
// 前置本地HTTP供应商、隔离PG；不使用供应商密钥；服务、连接和schema由Cleanup回收。
func TestStreamReadFailureDiagnostics(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"partial\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Partial answer\"}}]}\n\n")
				w.(http.Flusher).Flush()
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
			}))
			defer up.Close()
			dep := chatDeployment("stream-diagnostic")
			dep.LiteLLMParams["api_base"] = up.URL
			dep.ModelInfo["endpoint_types"] = []string{"chat", "bypass:openai-chat"}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "stream-owner")
			path := "/v1/chat/completions"
			if native {
				path = "/bypass/openai/v1/chat/completions"
			}
			body := chatRequest(dep.ModelName, "Verify incomplete stream.")
			body["stream"] = true
			response := h.do("POST", path, owner.key, body)
			d := logDetail(t, h, owner.session, response.header("x-litellm-call-id"))
			meta := d["metadata"].(map[string]any)
			if meta["http_status"] != float64(502) {
				t.Fatalf("中断流被记成功: %v", d)
			}
			upstream := meta["upstream_response"].(map[string]any)
			failure := upstream["failure"].(map[string]any)
			if upstream["read_error"] != "unexpected EOF" || failure["phase"] != "response_transfer" || failure["transport_error"] == "" || failure["cancellation_source"] != nil {
				t.Fatalf("上游流截断证据缺失或错误归因为用户取消: %v", upstream)
			}
		})
	}
}
