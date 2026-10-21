package regression

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/gateway/billing"
)

// budgetLengthResponse 构造带完整用量的正常长度终态或指定错误边界；返回独立对象供本地 HTTP 夹具使用，无外部副作用。
func budgetLengthResponse(mode string) map[string]any {
	d := map[string]any{"id": "resp_length", "object": "response", "status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"}, "output": []any{map[string]any{"id": "msg_length", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "LENGTH_OK"}}}}, "usage": map[string]any{"input_tokens": 8, "output_tokens": 8}}
	u := d["usage"].(map[string]any)
	switch mode {
	case "completed":
		d["status"] = "completed"
		delete(d, "incomplete_details")
	case "partial-tool":
		d["output"] = []any{map[string]any{"type": "function_call", "id": "fc_length", "call_id": "call_length", "name": "lookup", "arguments": "{\"value\":"}}
	case "bad-tool-type":
		d["output"] = []any{map[string]any{"type": "function_call", "id": "fc_length", "call_id": "call_length", "name": "lookup", "arguments": 42}}
	case "unknown-reason":
		d["incomplete_details"] = map[string]any{"reason": "other"}
	case "missing-reason":
		delete(d, "incomplete_details")
	case "content-filter":
		d["incomplete_details"] = map[string]any{"reason": "content_filter"}
	case "missing-usage":
		delete(d, "usage")
	case "missing-output":
		delete(u, "output_tokens")
	case "conflict":
		u["completion_tokens"] = 7
	case "over-cap":
		u["output_tokens"] = 9
	case "error":
		d["error"] = map[string]any{"code": "failed"}
	}
	return d
}

// TestHardBudgetLengthTerminal 验证 cap 正常结束的标准/旁路 JSON/SSE 和 Chat→Responses 均按实测结算。
// 前置私有 PG、无 Redis、本地 HTTP；旁路额外验证截断工具原文、终态后保持连接的及时关闭与唯一外发；harness/httptest 回收全部资源。
func TestHardBudgetLengthTerminal(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	cases := []struct {
		source, path, mode string
		stream, keep       bool
	}{}
	for _, source := range []string{"responses", "chat"} {
		for _, stream := range []bool{false, true} {
			cases = append(cases, struct {
				source, path, mode string
				stream, keep       bool
			}{source, "/v1/responses", "text", stream, false})
		}
	}
	for _, mode := range []string{"text", "partial-tool"} {
		for _, stream := range []bool{false, true} {
			cases = append(cases, struct {
				source, path, mode string
				stream, keep       bool
			}{"responses", "/bypass/openai/v1/responses", mode, stream, false})
		}
	}
	for _, mode := range []string{"text", "completed"} {
		cases = append(cases, struct {
			source, path, mode string
			stream, keep       bool
		}{"responses", "/bypass/openai/v1/responses", mode, true, true})
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s%s/%s/stream=%v/keep=%v", tc.source, tc.path, tc.mode, tc.stream, tc.keep), func(t *testing.T) {
			var calls atomic.Int32
			closed := make(chan struct{}, 1)
			terminal, _ := json.Marshal(budgetLengthResponse(tc.mode))
			raw := string(terminal)
			if tc.source == "chat" {
				raw = `{"id":"chatcmpl_length","choices":[{"index":0,"message":{"role":"assistant","content":"LENGTH_OK"},"finish_reason":"length"}],"usage":{"prompt_tokens":8,"completion_tokens":8}}`
			}
			if tc.stream {
				event := "response.incomplete"
				if tc.mode == "completed" {
					event = "response.completed"
				}
				raw = "event: " + event + "\ndata: {\"type\":\"" + event + "\",\"response\":" + string(terminal) + "}\n\n"
				if tc.source == "chat" {
					raw = "data: {\"id\":\"chatcmpl_length\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"LENGTH_OK\"},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":8}}\n\ndata: [DONE]\n\n"
				}
			}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				io.WriteString(w, raw)
				w.(http.Flusher).Flush()
				if tc.keep {
					select {
					case <-r.Context().Done():
						closed <- struct{}{}
					case <-time.After(3 * time.Second):
					}
				}
			}))
			t.Cleanup(up.Close)
			dep := hardBudgetDeployment("length-budget")
			dep.LiteLLMParams["api_base"] = up.URL
			protocol := "openai-" + tc.source
			dep.ModelInfo["transport"] = "bypass_" + strings.ReplaceAll(protocol, "-", "_")
			dep.ModelInfo["endpoint_types"] = []string{"chat", "responses", "bypass:" + protocol}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "length-budget")
			planRaw := []byte(`{"input":"Verify normal output limit.","max_output_tokens":8}`)
			if tc.source == "chat" {
				planRaw = []byte(`{"messages":[{"role":"user","content":"Verify normal output limit."}],"max_tokens":8}`)
			}
			plan, _, err := billing.StrictBudgetPlan(dep, planRaw, protocol, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			h.setKeyBudget(t, admin, owner.key, plan.Amount)
			if tc.keep {
				template := routeTemplate(t, h, admin, "length short deadline", routeTemplateBody([]any{}, 3, 1.5, 0, 0))
				bindTemplate(t, h, admin, "key", owner.keyID, template)
			}
			start := time.Now()
			got := h.ok(http.MethodPost, tc.path, owner.key, map[string]any{"model": dep.ModelName, "input": "Verify normal output limit.", "max_output_tokens": 8, "stream": tc.stream})
			elapsed := time.Since(start)
			holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
			if err != nil {
				t.Fatal(err)
			}
			key, err := h.db.GetKey(t.Context(), owner.keyID)
			if err != nil {
				t.Fatal(err)
			}
			if len(holds) != 0 || !nearlyEqual(key.Spend, .00008) {
				t.Fatalf("正常长度终态未按实测结算 holds=%+v spend=%g body=%s", holds, key.Spend, got.text())
			}
			log := h.ok("GET", "/spend/logs/ui/"+got.header("X-Litellm-Call-Id"), admin, nil).json()
			if log["status"] != "success" {
				t.Fatalf("正常终态被记为失败 %+v", log)
			}
			if strings.Contains(tc.path, "/bypass/") {
				if got.text() != raw {
					t.Fatalf("旁路改写原始截断语义 got=%s want=%s", got.text(), raw)
				}
			} else if !strings.Contains(got.text(), `"status":"incomplete"`) {
				t.Fatalf("标准入口丢失incomplete %s", got.text())
			}
			if calls.Load() != 1 {
				t.Fatalf("正常终态重试外发 %d", calls.Load())
			}
			if tc.keep {
				if elapsed > time.Second {
					t.Fatalf("完整终态仍等EOF: %v", elapsed)
				}
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("网关未关闭已结束上游连接")
				}
			}
			t.Logf("client=%d status=%v spend=%g hold=%d upstream=%d elapsed=%v", got.status, log["status"], key.Spend, len(holds), calls.Load(), elapsed)
		})
	}
}

// TestHardBudgetLengthUncertain 验证未知失败、错误工具类型和不可靠最终用量不生成预占，不可靠用量标记未知，下一请求仍可放行。
// JSON/SSE 均使用私有 PG；SSE 先带完整中间用量，防止终态缺字段从旧帧补齐。服务/schema 由测试清理，无付费模型。
func TestHardBudgetLengthUncertain(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, mode := range []string{"bad-tool-type", "unknown-reason", "missing-reason", "content-filter", "missing-usage", "missing-output", "conflict", "over-cap", "error"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", mode, stream), func(t *testing.T) {
				var calls atomic.Int32
				terminal, _ := json.Marshal(budgetLengthResponse(mode))
				raw := string(terminal)
				if stream {
					raw = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"LENGTH_OK\",\"usage\":{\"input_tokens\":8,\"output_tokens\":2}}\n\nevent: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":" + raw + "}\n\n"
				}
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					io.WriteString(w, raw)
				}))
				t.Cleanup(up.Close)
				dep := hardBudgetDeployment("length-uncertain")
				dep.LiteLLMParams["api_base"] = up.URL
				dep.ModelInfo["transport"] = "bypass_openai_responses"
				dep.ModelInfo["endpoint_types"] = []string{"responses", "bypass:openai-responses"}
				h := newHarness(t, dep)
				admin := h.adminSession()
				owner := h.openScope(t, admin, "length-uncertain")
				plan, _, err := billing.StrictBudgetPlan(dep, []byte(`{"input":"Verify uncertain cap.","max_output_tokens":8}`), "openai-responses", time.Now())
				if err != nil {
					t.Fatal(err)
				}
				h.setKeyBudget(t, admin, owner.key, plan.Amount)
				body := map[string]any{"model": dep.ModelName, "input": "Verify uncertain cap.", "max_output_tokens": 8, "stream": stream}
				got := h.ok("POST", "/bypass/openai/v1/responses", owner.key, body)
				if got.text() != raw {
					t.Fatal("原生错误终态被改写")
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
				if err != nil {
					t.Fatal(err)
				}
				key, err := h.db.GetKey(t.Context(), owner.keyID)
				if err != nil {
					t.Fatal(err)
				}
				if len(holds) != 0 || (mode != "over-cap" && key.Spend != 0) {
					t.Fatalf("未知用量错误退款 holds=%+v spend=%g", holds, key.Spend)
				}
				body["input"] = "Try a new request after uncertain cap."
				refused := h.do("POST", "/bypass/openai/v1/responses", owner.key, body)
				if refused.status != 200 || calls.Load() != 2 {
					t.Fatalf("未知占用允许再次外发 status=%d calls=%d", refused.status, calls.Load())
				}
			})
		}
	}
}
