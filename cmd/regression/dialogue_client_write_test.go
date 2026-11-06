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

// dialogueWriteProbe 在真实网关入口注入短写或写入失败，上游和持久化仍使用真实隔离服务。
type dialogueWriteProbe struct {
	header http.Header
	short  bool
	writes int
}

// Header 返回单请求响应头，供网关设置调用 ID。
func (w *dialogueWriteProbe) Header() http.Header { return w.header }

// WriteHeader 接受响应头，不触及网络。
func (w *dialogueWriteProbe) WriteHeader(int) {}

// Write 拒绝正文或返回短写，避免把模拟故障误认为真实客户端行为。
func (w *dialogueWriteProbe) Write(p []byte) (int, error) {
	w.writes++
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("synthetic client write failure")
}

// Flush 满足流式写入器接口；夹具无缓存需要刷新。
func (w *dialogueWriteProbe) Flush() {}

// TestDialogueClientWriteFailure 验证真实标准 Chat 和转换 Responses 在写失败或短写后失败且零费用。
// 隔离 PG、本机上游和支持重试/冷却的真实路由，核验无重放、诊断分类及健康后续请求；harness 清理资源。
func TestDialogueClientWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		short            bool
	}{
		{"native-error", "/chat/completions", `{"model":"dialogue-write","messages":[{"role":"user","content":"Hello"}],"stream":true}`, false},
		{"native-short", "/chat/completions", `{"model":"dialogue-write","messages":[{"role":"user","content":"Hello"}],"stream":true}`, true},
		{"bridge-error", "/responses", `{"model":"dialogue-write","input":"Hello","stream":true}`, false},
		{"bridge-short", "/responses", `{"model":"dialogue-write","input":"Hello","stream":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 冷却状态只保存在 Redis；必须连接独立实例才能验证健康检查。
			isolatedSpendRedis(t)
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(up.Close)
			dep := chatDeployment("dialogue-write")
			dep.LiteLLMParams["api_base"] = up.URL + "/v1"
			dep.ModelInfo["transport"] = "bypass_openai_chat"
			dep.ModelInfo["endpoint_types"] = []string{"chat", "responses"}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "dialogue-write")
			template := routeTemplate(t, h, admin, "dialogue-write", routeTemplateBody([]any{}, 3, 10, 1, 60))
			bindTemplate(t, h, admin, "key", owner.keyID, template)
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+owner.key)
			req.Header.Set("Content-Type", "application/json")
			w := &dialogueWriteProbe{header: make(http.Header), short: tc.short}
			h.gw.Handler().ServeHTTP(w, req)
			if calls.Load() != 1 || w.writes != 1 {
				t.Fatalf("客户端写入失败不应重放上游或再次写出错误: calls=%d writes=%d", calls.Load(), w.writes)
			}
			id := w.header.Get("x-litellm-call-id")
			if id == "" {
				t.Fatal("响应缺少调用 ID")
			}
			log := logDetail(t, h, admin, id)
			if log["status"] != "error" {
				t.Fatalf("写入失败被记录为成功: %v", log)
			}
			if spend, ok := log["spend"].(float64); !ok || spend != 0 {
				t.Fatalf("失败请求不应扣费: %v", log["spend"])
			}
			upstream := log["metadata"].(map[string]any)["upstream_response"].(map[string]any)
			failure := upstream["failure"].(map[string]any)
			if failure["phase"] != "client_write" || failure["kind"] != "client_write_error" {
				t.Fatalf("写入失败诊断错误: %v", failure)
			}
			recovered := h.do("POST", "/chat/completions", owner.key, map[string]any{"model": "dialogue-write", "messages": []any{map[string]any{"role": "user", "content": "Recover."}}, "stream": true})
			if recovered.status != 200 || calls.Load() != 2 {
				t.Fatalf("客户端写失败导致健康上游不可用: calls=%d %s", calls.Load(), recovered.describe())
			}
		})
	}
}
