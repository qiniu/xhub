package regression

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestBypassClientWriteFailure 验证透传对话流的客户端写入错误和短写不能记成功或扣费。
// 使用真实透传路由、隔离 PG、本地 HTTP 上游和注入写入器；核验错误分类、零费用及上游后续可用，harness 清理资源。
func TestBypassClientWriteFailure(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(fmt.Sprint(short), func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(up.Close)
			dep := chatDeployment("bypass-write")
			dep.LiteLLMParams["api_base"] = up.URL + "/v1"
			dep.ModelInfo["transport"] = "bypass_openai_chat"
			dep.ModelInfo["endpoint_types"] = []string{"chat", "bypass:openai-chat"}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "bypass-write")
			template := routeTemplate(t, h, admin, "bypass-write", routeTemplateBody([]any{}, 3, 10, 1, 60))
			bindTemplate(t, h, admin, "key", owner.keyID, template)
			path := "/bypass/openai/v1/chat/completions"
			body := `{"model":"bypass-write","messages":[{"role":"user","content":"Hello"}],"stream":true}`
			req := httptest.NewRequest("POST", path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+owner.key)
			req.Header.Set("Content-Type", "application/json")
			w := &dialogueWriteProbe{header: make(http.Header), short: short}
			h.gw.Handler().ServeHTTP(w, req)
			if calls.Load() != 1 {
				t.Fatalf("客户端写入失败后重试: calls=%d", calls.Load())
			}
			id := w.header.Get("x-litellm-call-id")
			if id == "" {
				t.Fatal("响应缺少调用 ID")
			}
			log := logDetail(t, h, admin, id)
			if log["status"] != "error" {
				t.Fatalf("透传流写入失败被记录为成功: %v", log)
			}
			if spend, ok := log["spend"].(float64); !ok || spend != 0 {
				t.Fatalf("失败请求不应扣费: %v", log["spend"])
			}
			upstream := log["metadata"].(map[string]any)["upstream_response"].(map[string]any)
			failure := upstream["failure"].(map[string]any)
			if w.writes != 1 {
				t.Fatalf("写入失败后不应继续写客户端: %d", w.writes)
			}
			if failure["phase"] != "client_write" || failure["kind"] != "client_write_error" {
				t.Fatalf("写入失败诊断错误: %v", failure)
			}
			if !strings.Contains(fmt.Sprint(log["error"]), "client_write_error") {
				t.Fatalf("错误摘要未标识客户端写入失败: %v", log["error"])
			}
			recovered := h.do("POST", path, owner.key, map[string]any{"model": "bypass-write", "messages": []any{map[string]any{"role": "user", "content": "Recover."}}, "stream": true})
			if recovered.status != 200 || calls.Load() != 2 {
				t.Fatalf("客户端写失败导致健康上游不可用: calls=%d %s", calls.Load(), recovered.describe())
			}
		})
	}
}
