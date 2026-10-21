package regression

import (
	"encoding/json"
	"fmt"
	"github.com/sunqirui1987/xhub/internal/gateway/affinity"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// signedMessagesContent 生成有序多块推理与工具内容；参数round为工具轮次，返回合成Messages块。
// 回归夹具用来精确验证签名、redacted字节和工具关联，所有内容均人工构造且无需外部清理。
func signedMessagesContent(round int) []any {
	return []any{map[string]any{"type": "thinking", "thinking": fmt.Sprintf("Public thought %d", round), "signature": fmt.Sprintf("PRIVATE_SIGNATURE_%d", round)}, map[string]any{"type": "redacted_thinking", "data": fmt.Sprintf("PRIVATE_REDACTED_%d", round)}, map[string]any{"type": "text", "text": "Working."}, map[string]any{"type": "tool_use", "id": fmt.Sprintf("call_%d", round), "name": "lookup", "input": map[string]any{"step": float64(round)}}}
}

// writeSignedMessagesFixture 输出真实HTTP JSON或分片Messages SSE；参数是已构建的人工响应及stream模式。
// 对thinking签名故意分两段，验证累积后原字节回放；写入只作用本地测试连接，无付费调用。
func writeSignedMessagesFixture(w http.ResponseWriter, message map[string]any, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(message)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(m map[string]any) {
		raw, _ := json.Marshal(m)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m["type"], raw)
	}
	emit(map[string]any{"type": "message_start", "message": map[string]any{"id": message["id"], "type": "message", "role": "assistant", "content": []any{}, "usage": map[string]any{"input_tokens": 8, "output_tokens": 0}}})
	for i, v := range message["content"].([]any) {
		block := v.(map[string]any)
		initial := block
		if block["type"] == "thinking" {
			initial = map[string]any{"type": "thinking", "thinking": "", "signature": ""}
		}
		emit(map[string]any{"type": "content_block_start", "index": i, "content_block": initial})
		if block["type"] == "thinking" {
			emit(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "thinking_delta", "thinking": block["thinking"]}})
			sig := block["signature"].(string)
			for _, part := range []string{sig[:5], sig[5:]} {
				emit(map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "signature_delta", "signature": part}})
			}
		}
		emit(map[string]any{"type": "content_block_stop", "index": i})
	}
	emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": message["stop_reason"]}, "usage": map[string]any{"output_tokens": 4}})
	emit(map[string]any{"type": "message_stop"})
}

// signedResponsesOutput 读取真实网关JSON或SSE终态，返回完整响应；参数raw为合成测试响应。
// SSE必须含completed且done中引用与终态一致，不能仅凭HTTP200通过；错误由t定位，无持久副作用。
func signedResponsesOutput(t *testing.T, raw string, stream bool) map[string]any {
	t.Helper()
	var result map[string]any
	if !stream {
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	done := map[string]any{}
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			continue
		}
		if event["type"] == "response.output_item.done" {
			item, _ := event["item"].(map[string]any)
			if item["type"] == "reasoning" {
				done = item
			}
		}
		if event["type"] == "response.completed" {
			result, _ = event["response"].(map[string]any)
		}
	}
	if result == nil {
		t.Fatalf("缺少完整终态: %s", raw)
	}
	output := result["output"].([]any)
	if len(output) > 0 {
		first, _ := output[0].(map[string]any)
		if first["encrypted_content"] != nil && !reflect.DeepEqual(first, done) {
			t.Fatal("item.done与终态引用不一致")
		}
	}
	return result
}

// TestMessagesSignedHistoryRoundTrip 验证默认Codex字段形状的low在认证部署上完成签名工具链及普通回复后的第四轮。
// 前置独立PG/有限额Key/严格本地Messages上游；覆盖JSON/SSE、完整历史/previous_response_id及非法引用零外发。
// 检查原始签名块顺序、工具结果、输出cap、唯一账单与预占释放；harness清理身份/schema，httptest清理服务。
func TestMessagesSignedHistoryRoundTrip(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, stream := range []bool{false, true} {
		for _, parent := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/parent=%t", stream, parent), func(t *testing.T) {
				var count atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := int(count.Add(1))
					var body map[string]any
					raw, _ := io.ReadAll(r.Body)
					if err := json.Unmarshal(raw, &body); err != nil {
						t.Error(err)
					}
					if body["thinking"].(map[string]any)["type"] != "adaptive" || body["output_config"].(map[string]any)["effort"] != "low" || body["max_tokens"] != float64(64) {
						t.Errorf("出站推理/cap错误: %s", raw)
					}
					messages := body["messages"].([]any)
					var assistants []any
					for _, v := range messages {
						m := v.(map[string]any)
						if m["role"] == "assistant" {
							assistants = append(assistants, m["content"])
						}
					}
					if len(assistants) != n-1 {
						t.Errorf("助手分组数错误: %d/%d", len(assistants), n-1)
					}
					for i, got := range assistants {
						expected := signedMessagesContent(i + 1)
						if i >= 2 {
							expected = []any{map[string]any{"type": "text", "text": "VALUE_1|VALUE_2"}}
						}
						if !reflect.DeepEqual(got, expected) {
							t.Errorf("签名、块顺序或工具内容被修改: %v", got)
						}
					}
					if n > 1 && n <= 3 && !strings.Contains(string(raw), fmt.Sprintf("VALUE_%d", n-1)) {
						t.Error("工具结果未回传")
					}
					content, stop := signedMessagesContent(n), "tool_use"
					if n >= 3 {
						content = []any{map[string]any{"type": "text", "text": "VALUE_1|VALUE_2"}}
						stop = "end_turn"
					}
					writeSignedMessagesFixture(w, map[string]any{"id": fmt.Sprintf("msg_round_%d", n), "type": "message", "role": "assistant", "content": content, "stop_reason": stop, "usage": map[string]any{"input_tokens": 8, "output_tokens": 4}}, stream)
				}))
				t.Cleanup(up.Close)
				dep := hardBudgetDeployment("signed-history")
				dep.ModelInfo["id"] = "signed-history-deployment"
				dep.LiteLLMParams["api_base"] = up.URL
				dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
				dep.ModelInfo["transport"] = "bypass_anthropic_messages"
				dep.ModelInfo["endpoint_types"] = []string{"responses", "messages"}
				dep.ModelInfo["messages_reasoning_mode"] = "adaptive"
				h := newHarness(t, dep)
				admin := h.adminSession()
				owner := h.openScope(t, admin, dep.ModelName)
				h.setKeyBudget(t, admin, owner.key, 1)
				history := []any{map[string]any{"role": "user", "content": "Read both synthetic values using lookup."}}
				body := map[string]any{"model": dep.ModelName, "input": history, "stream": stream, "max_output_tokens": 64, "reasoning": map[string]any{"effort": "low", "summary": "auto"}, "parallel_tool_calls": true, "prompt_cache_key": "signed-test-session", "text": map[string]any{"format": map[string]any{"type": "text"}}, "tools": []any{map[string]any{"type": "function", "name": "lookup", "strict": false, "parameters": map[string]any{"type": "object", "properties": map[string]any{"step": map[string]any{"type": "integer"}}}}}}
				for n := 1; n <= 4; n++ {
					// 每轮丢弃整个内存状态，等价于缓存消失/网关重启；只允许PG恢复原历史。
					h.gw.State = *affinity.NewDurable(nil, h.store)
					response := h.ok(http.MethodPost, "/v1/responses", owner.key, body)
					if strings.Contains(response.text(), "PRIVATE_SIGNATURE_") || strings.Contains(response.text(), "PRIVATE_REDACTED_") {
						t.Fatal("原始私有签名泄露给客户端")
					}
					result := signedResponsesOutput(t, response.text(), stream)
					if n == 3 {
						if !strings.Contains(response.text(), "VALUE_1|VALUE_2") {
							t.Fatal("工具链未完成")
						}
						output := result["output"].([]any)
						next := map[string]any{"role": "user", "content": "Repeat both values."}
						history = append(append(history, output...), next)
						if parent {
							body["previous_response_id"] = result["id"]
							body["input"] = []any{next}
						} else {
							body["input"] = history
						}
						continue
					}
					if n == 4 {
						if !strings.Contains(response.text(), "VALUE_1|VALUE_2") {
							t.Fatal("普通回复后第四轮未续接")
						}
						break
					}
					output := result["output"].([]any)
					output[0].(map[string]any)["content"] = nil // Codex SDK 实际回传字段，不能误判摘要篡改。
					if output[0].(map[string]any)["encrypted_content"] == nil {
						t.Fatal("签名引用丢失")
					}
					tool := map[string]any{"type": "function_call_output", "call_id": fmt.Sprintf("call_%d", n), "output": fmt.Sprintf("VALUE_%d", n)}
					history = append(append(history, output...), tool)
					if parent {
						body["previous_response_id"] = result["id"]
						body["input"] = []any{tool}
					} else {
						body["input"] = history
					}
				}
				delete(body, "previous_response_id")
				body["input"] = []any{map[string]any{"type": "reasoning", "encrypted_content": "xhub_messages_v1:" + strings.Repeat("a", 64), "summary": []any{}}, map[string]any{"role": "user", "content": "Continue."}}
				denied := h.do(http.MethodPost, "/v1/responses", owner.key, body)
				if denied.status != 400 || count.Load() != 4 {
					t.Fatalf("伪造引用被外发: %s calls=%d", denied.describe(), count.Load())
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
				if err != nil || len(holds) != 0 {
					t.Fatalf("四轮结算遗留占用: %v %v", holds, err)
				}
				key, err := h.db.GetKey(t.Context(), owner.keyID)
				if err != nil || !nearlyEqual(key.Spend, 0.000192) {
					t.Fatalf("四轮token计费不符: %+v %v", key, err)
				}
			})
		}
	}
}
