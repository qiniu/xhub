package dialogue

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// refusalStreamFrames 构造 Messages 拒绝流夹具；details 为原始 JSON，partial 控制先发文本，terminal 控制完整终态。
// 返回完整 SSE 字符串，测试调用；所有输入均为合成数据，无外部副作用。
func refusalStreamFrames(details string, partial, terminal bool) string {
	events := []string{`{"type":"message_start","message":{"id":"msg_refusal","type":"message","role":"assistant","model":"private","content":[],"usage":{"input_tokens":65,"output_tokens":0}}}`}
	if partial {
		events = append(events, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"Partial"}}`, `{"type":"content_block_stop","index":0}`)
	}
	events = append(events, `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":`+details+`},"usage":{"input_tokens":65,"output_tokens":0,"cache_creation_input_tokens":3033,"cache_creation":{"ephemeral_1h_input_tokens":3033,"ephemeral_5m_input_tokens":0}}}`)
	if terminal {
		events = append(events, `{"type":"message_stop"}`)
	}
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}

// TestMessagesRefusalStream 验证三协议对合法拒绝流的终态/用量，以及截断、缺说明、畸形详情和部分输出的失败边界。
// 参数 t 为上下文；使用内存 HTTP 流，检查完成回调只在完整且可表达的回复调用；无持久化数据，无需外部清理。
func TestMessagesRefusalStream(t *testing.T) {
	for _, target := range []string{"anthropic-messages", "openai-chat", "openai-responses"} {
		for _, tc := range []struct {
			name, details                     string
			partial, terminal, valid, bridged bool
		}{
			{"explained", `{"type":"refusal","category":"cyber","explanation":"Request declined."}`, false, true, true, true},
			{"null", `null`, false, true, true, false},
			{"partial", `{"type":"refusal","explanation":"Request declined."}`, true, true, true, false},
			{"bad_details", `{"type":"refusal","explanation":false}`, false, true, false, false},
			{"truncated", `{"type":"refusal","explanation":"Request declined."}`, false, false, false, false},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				commits := 0
				var result llmdialogue.DialogueResult
				raw := refusalStreamFrames(tc.details, tc.partial, tc.terminal)
				_, usage, _, capture, err := PipeDialogue(w, &http.Response{Body: io.NopCloser(strings.NewReader(raw))}, time.Now(), "public", "anthropic-messages", target, func(r llmdialogue.DialogueResult) {
					if strings.Contains(w.Body.String(), `"type":"response.completed"`) || strings.Contains(w.Body.String(), `"type":"message_stop"`) || strings.Contains(w.Body.String(), `"finish_reason":"stop"`) {
						t.Error("在成功终态之后才提交回调")
					}
					commits++
					result = r
				})
				want := tc.valid && (target == "anthropic-messages" || tc.bridged)
				if (err == nil) != want || (commits == 1) != want {
					t.Fatalf("拒绝流状态不符: commits=%d err=%v body=%s", commits, err, capture)
				}
				if !want {
					if strings.Contains(string(capture), `"type":"response.completed"`) || strings.Contains(string(capture), `"type":"message_stop"`) || strings.Contains(string(capture), "[DONE]") {
						t.Fatalf("失败流仍有完成事件: %s", capture)
					}
					return
				}
				if !result.Refused || usage["completion_tokens"] != float64(0) || usage["cache_creation_input_tokens"] != float64(3033) {
					t.Fatalf("拒绝事实或usage丢失: %+v %v", result, usage)
				}
				if target == "anthropic-messages" {
					if _, ok := result.Native["content"].([]any); !ok || !strings.Contains(string(capture), `"stop_reason":"refusal"`) || !strings.Contains(string(capture), `"type":"message_stop"`) {
						t.Fatalf("原生空拒绝未完成或被改写: %s %+v", capture, result)
					}
				} else if !strings.Contains(string(capture), `"refusal":"Request declined."`) {
					t.Fatalf("跨协议丢失拒绝说明: %s", capture)
				}
				if target == "openai-responses" {
					var types []string
					lastSequence := -1
					for _, line := range strings.Split(string(capture), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						var event map[string]any
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
							t.Fatal(err)
						}
						types = append(types, event["type"].(string))
						sequence := int(event["sequence_number"].(float64))
						if sequence != lastSequence+1 {
							t.Fatal("拒绝事件序号不连续")
						}
						lastSequence = sequence
						if event["type"] == "response.completed" {
							output := event["response"].(map[string]any)["output"].([]any)
							if len(output) != 1 || output[0].(map[string]any)["content"].([]any)[0].(map[string]any)["type"] != "refusal" {
								t.Fatalf("终态没有拒绝内容: %v", output)
							}
						}
					}
					wantTypes := "[response.created response.output_item.added response.content_part.added response.refusal.delta response.refusal.done response.content_part.done response.output_item.done response.completed]"
					if fmt.Sprint(types) != wantTypes {
						t.Fatalf("拒绝生命周期错误: %v", types)
					}
				}
			})
		}
	}
}

// TestRefusalHistoryPreservesExplanation 验证 Responses 续轮缓存保留公开拒绝说明而非空助手消息。
// 参数 t 为上下文；前置共享拒绝结果，断言原输入和拒绝说明保留；仅内存数据无需清理。
func TestRefusalHistoryPreservesExplanation(t *testing.T) {
	history := ResponseHistory(llmdialogue.Dialogue{Turns: []llmdialogue.Turn{{Role: "user", Text: "Hello"}}}, nil, llmdialogue.DialogueResult{ID: "msg_refusal", Refused: true, Refusal: "Request declined."})
	if len(history) != 2 || history[1].Native != nil || history[1].Role != "assistant" || history[1].Text != "Request declined." {
		t.Fatalf("续轮拒绝历史丢失: %+v", history)
	}
}

// TestRefusalStreamLifecycleBoundaries 验证空正常原生回复仍成功，拒绝流缺开始/冲突终态/未闭合空块不得发布成功。
// 参数 t 为上下文；纯内存 HTTP 合成流，正常场景保留空数组，错误场景不提交历史；无外部清理。
func TestRefusalStreamLifecycleBoundaries(t *testing.T) {
	base := refusalStreamFrames(`{"type":"refusal","explanation":"Request declined."}`, false, true)
	startEnd := strings.Index(base, "\n\n") + 2
	for _, target := range []string{"anthropic-messages", "openai-chat", "openai-responses"} {
		for _, tc := range []struct{ name, raw string }{
			{"no_start", base[startEnd:]},
			{"conflicting_stop", base[:startEnd] + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}` + "\n\n" + base[startEnd:]},
			{"open_empty_block", base[:startEnd] + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" + base[startEnd:]},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				commits := 0
				_, _, _, raw, err := PipeDialogue(httptest.NewRecorder(), &http.Response{Body: io.NopCloser(strings.NewReader(tc.raw))}, time.Now(), "public", "anthropic-messages", target, func(llmdialogue.DialogueResult) { commits++ })
				if err == nil || commits != 0 || strings.Contains(string(raw), `"type":"response.completed"`) || strings.Contains(string(raw), `"type":"message_stop"`) || strings.Contains(string(raw), "[DONE]") {
					t.Fatalf("畸形拒绝流被接受: commits=%d err=%v body=%s", commits, err, raw)
				}
			})
		}
	}
	t.Run("native_empty_normal", func(t *testing.T) {
		raw := strings.Replace(base, `"stop_reason":"refusal","stop_details":{"type":"refusal","explanation":"Request declined."}`, `"stop_reason":"end_turn","stop_details":null`, 1)
		var result llmdialogue.DialogueResult
		_, _, _, capture, err := PipeDialogue(httptest.NewRecorder(), &http.Response{Body: io.NopCloser(strings.NewReader(raw))}, time.Now(), "public", "anthropic-messages", "anthropic-messages", func(r llmdialogue.DialogueResult) { result = r })
		content, ok := result.Native["content"].([]any)
		if err != nil || result.Refused || !ok || len(content) != 0 || !strings.Contains(string(capture), `"type":"message_stop"`) {
			t.Fatalf("合法空正常流受拒绝修复影响: %+v err=%v body=%s", result, err, capture)
		}
	})
}
