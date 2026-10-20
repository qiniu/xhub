package execution

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/gateway/affinity"
)

// publicReasoningHost 复用执行测试宿主并接入真实内存会话归属，实现跨模型历史回归而非 mock 路由结论。
// 只保存本例内存数据；身份和计费使用固定测试宿主，不访问生产数据库或密钥。
type publicReasoningHost struct {
	*logHost
	state affinity.State
}

// PlanRoute 将请求、模型、正文和身份交给真实亲和性实现，返回可执行计划或归属错误；本例只读内存缓存。
func (h *publicReasoningHost) PlanRoute(r *http.Request, alias string, body map[string]any, p *auth.Principal) dpcontract.RoutePlan {
	return h.state.PlanRoute(r, alias, body, p)
}

// CommitRoute 将成功部署及响应归属写入本例缓存，供后续请求验证原连接边界；无持久化或外部副作用。
func (h *publicReasoningHost) CommitRoute(plan dpcontract.RoutePlan, deploymentID, responseID string) {
	h.state.CommitRoute(plan, deploymentID, responseID)
}

// WriteChatJSON 输出执行器已经转换的同步响应，并记录测试完成状态；参数沿用宿主契约，供后续轮次读取真实编码结果。
// 本例不重新转换正文或模拟扣费，只写内存 HTTP 记录器；忽略缓存和授权范围参数，无外部副作用。
func (h *publicReasoningHost) WriteChatJSON(w http.ResponseWriter, p *auth.Principal, callID, alias, _ string, op, _ string, body []byte, status int, start time.Time, deployment string, _ ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	h.RecordSpend(w, p, callID, alias, op, nil, start, false, status, deployment)
}

// TestChatPublicReasoningSwitchRegression 验证真实 Serve 接口接收自己的公开推理输出后，可带工具历史切换到 Messages/Responses 上游。
// 参数 t 为测试上下文；本地 HTTP 上游返回协议固定夹具，检查请求型号、摘要、工具结果与接口错误；密文及非法扩展不得出站。
// HTTP 服务在测试结束关闭，配置、归属和日志均在内存，未使用外部凭据或生产数据；本例不覆盖真实认证与扣费。
func TestChatPublicReasoningSwitchRegression(t *testing.T) {
	for _, tc := range []struct {
		name, transport, mutation string
		want                      int
	}{
		{"public-to-messages", "bypass_anthropic_messages", "", 200},
		{"public-to-responses", "bypass_openai_responses", "", 200},
		{"cipher-blocked", "bypass_anthropic_messages", "cipher", 400},
		{"malformed-blocked", "bypass_anthropic_messages", "invalid", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if body["model"] == "grok-real" {
					io.WriteString(w, `{"id":"resp_grok","status":"completed","output":[{"id":"rs_grok","type":"reasoning","summary":[{"type":"summary_text","text":"Public plan"}]},{"type":"function_call","call_id":"call_read","name":"read","arguments":"{}"}],"usage":{"input_tokens":8,"output_tokens":2}}`)
					return
				}
				if body["model"] != "target-real" {
					t.Errorf("模型选择被旧历史修改: %v", body["model"])
				}
				encoded, _ := json.Marshal(body)
				raw := string(encoded)
				if strings.Count(raw, "Public plan") != 1 || !strings.Contains(raw, "unique-value") || !strings.Contains(raw, "call_read") || strings.Contains(raw, "rs_grok") || strings.Contains(raw, "reasoning_items") {
					t.Errorf("跨协议摘要/工具历史错误: %s", raw)
				}
				if tc.transport == "bypass_anthropic_messages" {
					io.WriteString(w, `{"id":"msg_target","type":"message","role":"assistant","content":[{"type":"text","text":"unique-value"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`)
				} else {
					io.WriteString(w, `{"id":"resp_target","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"unique-value"}]}],"usage":{"input_tokens":8,"output_tokens":2}}`)
				}
			}))
			defer up.Close()
			grok := Deployment("grok", "grok-real", "test", up.URL, "bypass_openai_responses", nil)
			target := Deployment("target", "target-real", "test", up.URL, tc.transport, nil)
			grok.ModelInfo["id"], target.ModelInfo["id"] = "grok-dep", "target-dep"
			grok.ModelInfo["endpoint_types"], target.ModelInfo["endpoint_types"] = []string{"chat"}, []string{"chat"}
			h := &publicReasoningHost{logHost: newLogHost(&config.Config{ModelList: []config.ModelEntry{grok, target}}, up.Client())}
			call := func(body map[string]any) *httptest.ResponseRecorder {
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(encoded))
				req.Header.Set("X-Session-Id", "public-reasoning-session")
				w := httptest.NewRecorder()
				Serve(h, w, req, "chat")
				return w
			}
			initial := map[string]any{"role": "user", "content": "Read the value."}
			first := call(map[string]any{"model": "grok", "messages": []any{initial}})
			if first.Code != 200 {
				t.Fatalf("首轮失败: %d %s", first.Code, first.Body.String())
			}
			var response struct {
				Choices []struct {
					Message map[string]any `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil || len(response.Choices) != 1 {
				t.Fatalf("首轮 Chat 契约错误: %v %s", err, first.Body.String())
			}
			message := response.Choices[0].Message
			items, ok := message["reasoning_items"].([]any)
			if !ok || len(items) != 1 {
				t.Fatalf("首轮丢失结构化推理: %v", message)
			}
			item := items[0].(map[string]any)
			if tc.mutation == "cipher" {
				item["encrypted_content"] = "private-cipher"
			}
			if tc.mutation == "invalid" {
				item["summary"].([]any)[0].(map[string]any)["signature"] = "unknown-signature"
			}
			second := call(map[string]any{"model": "target", "messages": []any{initial, message, map[string]any{"role": "tool", "tool_call_id": "call_read", "content": "unique-value"}}})
			if second.Code != tc.want {
				t.Fatalf("切换接口状态错误: %d want=%d body=%s", second.Code, tc.want, second.Body.String())
			}
			mu.Lock()
			count := calls
			mu.Unlock()
			if tc.want == 200 {
				if count != 2 || !strings.Contains(second.Body.String(), "unique-value") {
					t.Fatalf("未完成跨协议闭环: calls=%d %s", count, second.Body.String())
				}
				if len(h.spends) != 2 || h.spends[1].status != 200 {
					t.Fatalf("成功切换未记录正确完成状态: %+v", h.spends)
				}
			} else if count != 1 || !strings.Contains(second.Body.String(), `"invalid_request"`) {
				t.Fatalf("危险历史出站或错误契约不明确: calls=%d %s", count, second.Body.String())
			}
		})
	}
}
