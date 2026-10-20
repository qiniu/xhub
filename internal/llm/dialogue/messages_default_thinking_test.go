package dialogue

import (
	"reflect"
	"testing"
)

// TestMessagesBridgeDefaultThinking 验证跨协议的无推理请求不依赖 Messages 上游的隐式 thinking 默认值。
// 前置为独立 Chat/Responses 请求；缺省、空对象、none 和合法摘要输出 disabled，非法及显式推理提前失败；无外部资源需清理。
func TestMessagesBridgeDefaultThinking(t *testing.T) {
	for _, source := range []string{"openai-responses", "openai-chat"} {
		for _, tc := range []struct {
			name   string
			fields map[string]any
			denied bool
		}{
			{"missing", nil, false},
			{"null", map[string]any{"reasoning": nil}, false},
			{"empty_object", map[string]any{"reasoning": map[string]any{}}, false},
			{"null_effort", map[string]any{"reasoning": map[string]any{"effort": nil}}, false},
			{"none", map[string]any{"reasoning": map[string]any{"effort": "none"}}, false},
			{"summary_auto", map[string]any{"reasoning": map[string]any{"summary": "auto"}}, false},
			{"summary_none", map[string]any{"reasoning": map[string]any{"summary": "none"}}, false},
			{"summary_concise", map[string]any{"reasoning": map[string]any{"summary": "concise"}}, false},
			{"summary_detailed", map[string]any{"reasoning": map[string]any{"summary": "detailed"}}, false},
			{"null_context", map[string]any{"reasoning": map[string]any{"context": nil}}, false},
			{"low", map[string]any{"reasoning": map[string]any{"effort": "low"}}, true},
			{"empty_effort", map[string]any{"reasoning": map[string]any{"effort": ""}}, true},
			{"invalid_effort", map[string]any{"reasoning": map[string]any{"effort": map[string]any{}}}, true},
			{"invalid_reasoning", map[string]any{"reasoning": "none"}, true},
			{"summary_invalid", map[string]any{"reasoning": map[string]any{"summary": "unknown"}}, true},
			{"summary_empty", map[string]any{"reasoning": map[string]any{"summary": ""}}, true},
			{"summary_bool", map[string]any{"reasoning": map[string]any{"summary": false}}, true},
			{"context", map[string]any{"reasoning": map[string]any{"context": "all_turns"}}, true},
			{"context_invalid", map[string]any{"reasoning": map[string]any{"context": true}}, true},
			{"unknown", map[string]any{"reasoning": map[string]any{"extra": true}}, true},
			{"chat_null", map[string]any{"reasoning_effort": nil}, false},
			{"chat_none", map[string]any{"reasoning_effort": "none"}, false},
			{"chat_low", map[string]any{"reasoning_effort": "low"}, true},
			{"chat_invalid", map[string]any{"reasoning_effort": false}, true},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				body := map[string]any{"model": "alias"}
				if source == "openai-responses" {
					body["input"] = "Call echo with the supplied value."
				} else {
					body["messages"] = []any{map[string]any{"role": "user", "content": "Call echo with the supplied value."}}
				}
				for key, value := range tc.fields {
					body[key] = value
				}
				before, _ := cloneObject(body)
				d, err := ParseDialogue(source, body)
				var out map[string]any
				if err == nil {
					out, err = EncodeDialogue(d, "anthropic-messages", "native")
				}
				if tc.denied {
					if err == nil {
						t.Fatalf("非法或未支持推理被静默关闭: %#v", tc.fields)
					}
				} else if err != nil || dialogueMap(out["thinking"])["type"] != "disabled" {
					t.Fatalf("未请求推理却保留上游自动推理: out=%#v err=%v", out, err)
				}
				if !reflect.DeepEqual(body, before) {
					t.Fatal("桥接修改了原始请求")
				}
			})
		}
	}
}

// TestNativeMessagesThinkingUnchanged 验证仅跨协议规范化，不覆盖原生 Messages 的供应商推理语义。
// 前置缺省、disabled、adaptive 和带预算的 enabled；输出与源请求完全一致且源对象不变，纯内存无需清理。
func TestNativeMessagesThinkingUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name     string
		thinking any
	}{
		{"missing", nil},
		{"disabled", map[string]any{"type": "disabled"}},
		{"adaptive", map[string]any{"type": "adaptive"}},
		{"enabled", map[string]any{"type": "enabled", "budget_tokens": float64(1024)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model": "native", "max_tokens": float64(2048), "messages": []any{map[string]any{"role": "user", "content": "Hello"}}}
			if tc.thinking != nil {
				body["thinking"] = tc.thinking
			}
			before, _ := cloneObject(body)
			d, err := ParseDialogue("anthropic-messages", body)
			if err != nil {
				t.Fatal(err)
			}
			out, err := EncodeDialogue(d, "anthropic-messages", "native")
			if err != nil || !reflect.DeepEqual(before, out) || !reflect.DeepEqual(before, body) {
				t.Fatalf("原生 thinking 被跨协议默认值改写: out=%#v err=%v", out, err)
			}
		})
	}
}
