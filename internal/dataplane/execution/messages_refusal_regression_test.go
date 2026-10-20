package execution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
)

// refusalRegressionHost 复用真实路由与执行宿主，在内存收集完整响应及结算调用用量；不连接账单数据库。
type refusalRegressionHost struct {
	*publicReasoningHost
	usage  map[string]any
	status int
}

// RecordSpend 保存真实执行链交给结算边界的用量和状态，参数遵循宿主接口；无返回、不模拟价格或扣款。
func (h *refusalRegressionHost) RecordSpend(_ http.ResponseWriter, _ *auth.Principal, _, _, _ string, usage map[string]any, _ time.Time, _ bool, status int, _ string) {
	h.usage, h.status = usage, status
}

// WriteChatJSON 输出真实转换结果并提取其 usage 供结算边界断言；参数遵循宿主接口，不访问数据库。
// 仅在同步测试路径调用，错误由测试断言定位，所有数据保留在内存请求中。
func (h *refusalRegressionHost) WriteChatJSON(w http.ResponseWriter, _ *auth.Principal, _, _, _, _, _ string, body []byte, status int, _ time.Time, _ string, _ ...string) {
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	h.usage, _ = doc["usage"].(map[string]any)
	h.status = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// refusalRegressionReply 发送合法拒绝的真实 HTTP 上游夹具，参数 stream 指定流模式、details 为原始拒绝详情、terminal 控制终态。
// 返回值无；仅用于本地 httptest，完整保留 cache usage，不使用外部凭据。
func refusalRegressionReply(w http.ResponseWriter, stream bool, details string, terminal bool) {
	usage := `{"input_tokens":65,"output_tokens":0,"cache_creation_input_tokens":3033,"cache_creation":{"ephemeral_1h_input_tokens":3033,"ephemeral_5m_input_tokens":0}}`
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"msg_refusal","type":"message","role":"assistant","content":[],"stop_reason":"refusal","stop_details":%s,"usage":%s}`, details, usage)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_refusal\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":65,\"output_tokens\":0}}}\n\n")
	fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\",\"stop_details\":%s},\"usage\":%s}\n\n", details, usage)
	if terminal {
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}
}

// refusalRegressionRequest 创建并执行统一网关请求，参数为本例宿主、入口和正文；返回 HTTP 记录器供契约断言。
// 只序列化合成数据，使用固定测试会话，没有外部资源；测试调用方负责 httptest 服务器关闭。
func refusalRegressionRequest(t *testing.T, h *refusalRegressionHost, op string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	path := map[string]string{"chat": "/v1/chat/completions", "responses": "/v1/responses", "messages": "/v1/messages"}[op]
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req.Header.Set("X-Session-Id", "refusal-regression")
	w := httptest.NewRecorder()
	Serve(h, w, req, op)
	return w
}

// refusalRegressionBody 返回给定入口的最小英文提示请求；stream 控制传输，公开模型固定为 claude。
// 测试辅助方法仅分配内存，不访问服务或修改调用方数据。
func refusalRegressionBody(op string, stream bool) map[string]any {
	body := map[string]any{"model": "claude", "stream": stream, "max_tokens": 256, "messages": []any{map[string]any{"role": "user", "content": "First question"}}}
	if op == "responses" {
		delete(body, "messages")
		delete(body, "max_tokens")
		body["input"], body["max_output_tokens"] = "First question", 256
	}
	return body
}

// TestMessagesRefusalRegression 验证真实 Serve→HTTP 上游→三协议 JSON/SSE 的拒绝、终态和结算用量契约。
// 参数 t 为上下文；完整拒绝记录成功传输及实测usage，缺说明跨协议/截断流明确失败，不允许空完成。
// 仅本地服务器、内存身份与结算宿主；服务器自动关闭，未验证真实数据库价格和身份鉴权。
func TestMessagesRefusalRegression(t *testing.T) {
	for _, op := range []string{"messages", "chat", "responses"} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"valid", "no_explanation", "truncated"} {
				if mode == "truncated" && !stream {
					continue
				}
				t.Run(fmt.Sprintf("%s/stream=%t/%s", op, stream, mode), func(t *testing.T) {
					details := `{"type":"refusal","category":"cyber","explanation":"Request declined."}`
					if mode == "no_explanation" {
						details = `{"type":"refusal","category":null,"explanation":null}`
					}
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						refusalRegressionReply(w, stream, details, mode != "truncated")
					}))
					defer up.Close()
					dep := Deployment("claude", "claude-real", "test", up.URL, "bypass_anthropic_messages", nil)
					dep.ModelInfo["endpoint_types"] = []string{"messages", "chat", "responses"}
					h := &refusalRegressionHost{publicReasoningHost: &publicReasoningHost{logHost: newLogHost(&config.Config{ModelList: []config.ModelEntry{dep}}, up.Client())}}
					w := refusalRegressionRequest(t, h, op, refusalRegressionBody(op, stream))
					valid := mode == "valid" || (mode == "no_explanation" && op == "messages")
					if valid {
						if w.Code != 200 || h.status != 200 || h.usage["cache_creation_input_tokens"] != float64(3033) {
							t.Fatalf("拒绝未完整到达结算边界: http=%d status=%d usage=%v body=%s", w.Code, h.status, h.usage, w.Body.String())
						}
						output := h.usage["output_tokens"]
						if output == nil {
							output = h.usage["completion_tokens"]
						}
						if output != float64(0) || !strings.Contains(w.Body.String(), `"refusal"`) {
							t.Fatalf("拒绝事实丢失: %s usage=%v", w.Body.String(), h.usage)
						}
						if stream && op == "messages" && !strings.Contains(w.Body.String(), `"type":"message_stop"`) {
							t.Fatal("原生拒绝流丢失终态")
						}
					} else {
						if h.status != 502 || strings.Contains(w.Body.String(), `"type":"response.completed"`) || strings.Contains(w.Body.String(), `"type":"message_stop"`) || strings.Contains(w.Body.String(), "[DONE]") {
							t.Fatalf("失败被伪装成成功: http=%d status=%d body=%s", w.Code, h.status, w.Body.String())
						}
					}
				})
			}
		}
	}
}

// TestRefusalContinuationRegression 验证拒绝后正常问题可继续同一 Claude 或切换 GPT，覆盖完整历史及 Responses previous_response_id。
// 参数 t 为上下文；首轮通过真实 HTTP 返回拒绝，第二轮上游验证说明只出现一次并返回正常文本。
// 服务、路由缓存和结算记录只存在本例内存并自动清理；不调用外部模型、不验证真实生产价格。
func TestRefusalContinuationRegression(t *testing.T) {
	for _, op := range []string{"chat", "responses"} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"same", "switch", "previous"} {
				if op == "chat" && mode == "previous" {
					continue
				}
				t.Run(fmt.Sprintf("%s/stream=%t/%s", op, stream, mode), func(t *testing.T) {
					var mu sync.Mutex
					calls := 0
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						calls++
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							return
						}
						if calls == 1 {
							refusalRegressionReply(w, stream, `{"type":"refusal","explanation":"Request declined."}`, true)
							return
						}
						raw, _ := json.Marshal(body)
						if strings.Count(string(raw), "Request declined.") != 1 || !strings.Contains(string(raw), "New question") {
							t.Errorf("续轮丢失或重复公开拒绝历史: %s", raw)
						}
						w.Header().Set("Content-Type", "application/json")
						if body["model"] == "gpt-real" {
							io.WriteString(w, `{"id":"resp_next","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"NEXT_OK"}]}],"usage":{"input_tokens":8,"output_tokens":2}}`)
						} else {
							io.WriteString(w, `{"id":"msg_next","type":"message","role":"assistant","content":[{"type":"text","text":"NEXT_OK"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`)
						}
					}))
					defer up.Close()
					claude := Deployment("claude", "claude-real", "test", up.URL, "bypass_anthropic_messages", nil)
					gpt := Deployment("gpt", "gpt-real", "test", up.URL, "bypass_openai_responses", nil)
					claude.ModelInfo["id"], gpt.ModelInfo["id"] = "dep-claude", "dep-gpt"
					claude.ModelInfo["endpoint_types"], gpt.ModelInfo["endpoint_types"] = []string{"chat", "responses"}, []string{"chat", "responses"}
					h := &refusalRegressionHost{publicReasoningHost: &publicReasoningHost{logHost: newLogHost(&config.Config{ModelList: []config.ModelEntry{claude, gpt}}, up.Client())}}
					first := refusalRegressionRequest(t, h, op, refusalRegressionBody(op, stream))
					if first.Code != 200 || h.status != 200 {
						t.Fatalf("首轮拒绝失败: %d %s", first.Code, first.Body.String())
					}
					var doc map[string]any
					if !stream {
						if err := json.Unmarshal(first.Body.Bytes(), &doc); err != nil {
							t.Fatal(err)
						}
					} else {
						var refusal string
						for _, line := range strings.Split(first.Body.String(), "\n") {
							if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
								continue
							}
							var event map[string]any
							if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
								t.Fatal(err)
							}
							if op == "responses" && event["type"] == "response.completed" {
								doc = event["response"].(map[string]any)
							}
							if op == "chat" {
								for _, choice := range event["choices"].([]any) {
									delta := choice.(map[string]any)["delta"].(map[string]any)
									if text, ok := delta["refusal"].(string); ok {
										refusal += text
									}
								}
							}
						}
						if op == "chat" {
							doc = map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": nil, "refusal": refusal}}}}
						}
					}
					secondBody := refusalRegressionBody(op, false)
					if mode == "switch" {
						secondBody["model"] = "gpt"
					}
					user := map[string]any{"role": "user", "content": "New question"}
					if op == "responses" {
						if mode == "previous" {
							secondBody["previous_response_id"], secondBody["input"] = doc["id"], "New question"
						} else {
							secondBody["input"] = []any{map[string]any{"role": "user", "content": "First question"}, doc["output"].([]any)[0], user}
						}
					} else {
						secondBody["messages"] = []any{map[string]any{"role": "user", "content": "First question"}, doc["choices"].([]any)[0].(map[string]any)["message"], user}
					}
					second := refusalRegressionRequest(t, h, op, secondBody)
					mu.Lock()
					count := calls
					mu.Unlock()
					if second.Code != 200 || !strings.Contains(second.Body.String(), "NEXT_OK") || count != 2 {
						t.Fatalf("拒绝后正常续轮被阻断: status=%d calls=%d body=%s", second.Code, count, second.Body.String())
					}
				})
			}
		}
	}
}

// TestMixedRefusalNativeReplayRegression 验证合法混合拒绝历史原生重放时真实 HTTP 上游收到完整原文，跨协议则在出站前明确拒绝。
// 参数 t 为上下文；分别覆盖 Chat content+refusal 和 Responses output_text+refusal，使用独立内存身份与本地 HTTP。
// 服务器在用例结束自动关闭，所有日志和配置只在内存，不调用外部模型、不变更生产数据。
func TestMixedRefusalNativeReplayRegression(t *testing.T) {
	for _, op := range []string{"chat", "responses"} {
		for _, native := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/native=%t", op, native), func(t *testing.T) {
				item := map[string]any{"role": "assistant", "content": "Partial", "refusal": "Request declined."}
				key, transport := "messages", "bypass_openai_chat"
				if op == "responses" {
					key, transport = "input", "bypass_openai_responses"
					item = map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Partial"}, map[string]any{"type": "refusal", "refusal": "Request declined."}}}
				}
				history := []any{map[string]any{"role": "user", "content": "First question"}, item, map[string]any{"role": "user", "content": "New question"}}
				var mu sync.Mutex
				calls := 0
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					calls++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					if !reflect.DeepEqual(body[key], history) {
						t.Errorf("原生混合拒绝历史被改写: %v", body[key])
					}
					w.Header().Set("Content-Type", "application/json")
					if op == "chat" {
						io.WriteString(w, `{"id":"chat_next","choices":[{"message":{"role":"assistant","content":"NEXT_OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`)
					} else {
						io.WriteString(w, `{"id":"resp_next","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"NEXT_OK"}]}],"usage":{"input_tokens":8,"output_tokens":2}}`)
					}
				}))
				defer up.Close()
				if !native {
					transport = "bypass_anthropic_messages"
				}
				dep := Deployment("claude", "target-real", "test", up.URL, transport, nil)
				dep.ModelInfo["endpoint_types"] = []string{"chat", "responses"}
				h := &refusalRegressionHost{publicReasoningHost: &publicReasoningHost{logHost: newLogHost(&config.Config{ModelList: []config.ModelEntry{dep}}, up.Client())}}
				body := refusalRegressionBody(op, false)
				body[key] = history
				w := refusalRegressionRequest(t, h, op, body)
				mu.Lock()
				count := calls
				mu.Unlock()
				if native {
					if w.Code != 200 || count != 1 || !strings.Contains(w.Body.String(), "NEXT_OK") {
						t.Fatalf("原生混合拒绝续轮失败: http=%d calls=%d body=%s", w.Code, count, w.Body.String())
					}
				} else if w.Code != 400 || count != 0 {
					t.Fatalf("不支持的混合拒绝跨协议出站: http=%d calls=%d body=%s", w.Code, count, w.Body.String())
				}
			})
		}
	}
}
