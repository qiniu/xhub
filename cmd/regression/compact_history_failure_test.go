package regression

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCompactHistoryWriteFailureFailsResponse 验证真实历史存储失败不会返回成功终态。
// 在隔离 PostgreSQL 中移除测试专用表，覆盖非流式及流式转换；夹具自动清理 schema 和本地上游。
func TestCompactHistoryWriteFailureFailsResponse(t *testing.T) {
	t.Setenv("XHUB_COMPACT_HISTORY", "1")
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			dep := chatDeployment("compact-failure")
			dep.LiteLLMParams["deployment_id"] = "compact-failure-deployment"
			h := newHarness(t, dep)
			token := h.personalBillingSession()
			if _, err := h.store.DB.Exec(`DROP TABLE conversation_history_blocks`); err != nil {
				t.Fatal(err)
			}
			got := h.do("POST", "/responses", token, map[string]any{"model": dep.ModelName, "input": "Hello", "stream": stream})
			if stream {
				if strings.Contains(string(got.body), "response.completed") || !strings.Contains(string(got.body), "response.failed") {
					t.Fatalf("历史写失败仍发布成功流: %s", got.describe())
				}
			} else if got.status != 502 || !strings.Contains(string(got.body), "history_unavailable") {
				t.Fatalf("历史写失败未明确报错: %s", got.describe())
			}
		})
	}
}

// TestCompactHistoryNativeWriteFailure 验证原生 Responses 上游在本地历史保存失败时不发布成功终态。
// 隔离 PostgreSQL 和本机上游分别覆盖 JSON/SSE；测试结束清理数据库 schema 与上游。
func TestCompactHistoryNativeWriteFailure(t *testing.T) {
	t.Setenv("XHUB_COMPACT_HISTORY", "1")
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := `{"id":"resp_storage_failure","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2}}`
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, response)
				}
			}))
			t.Cleanup(upstream.Close)
			dep := chatDeployment("native-compact-failure")
			dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
			dep.LiteLLMParams["deployment_id"] = "native-compact-failure-id"
			dep.ModelInfo = map[string]any{"id": "native-compact-failure-id", "transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}
			h := newHarness(t, dep)
			token := h.personalBillingSession()
			if _, err := h.store.DB.Exec(`DROP TABLE conversation_history_blocks`); err != nil {
				t.Fatal(err)
			}
			got := h.do("POST", "/responses", token, map[string]any{"model": dep.ModelName, "input": "Hello", "stream": stream})
			if stream {
				if strings.Contains(string(got.body), "response.completed") || !strings.Contains(string(got.body), "response.failed") {
					t.Fatalf("原生流错误被标记成功: %s", got.describe())
				}
			} else if got.status != 502 || !strings.Contains(string(got.body), "history_unavailable") {
				t.Fatalf("原生请求错误未暴露: %s", got.describe())
			}
		})
	}
}
