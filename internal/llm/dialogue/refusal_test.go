package dialogue

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestMessagesRefusalResult 验证空内容拒绝的原生保留、标准拒绝映射、缺说明/部分输出的明确失败与畸形详情拒绝。
// 参数 t 为测试上下文；仅使用合成响应，核对 usage 与 stop_details，不访问供应商或创建外部数据。
func TestMessagesRefusalResult(t *testing.T) {
	for _, tc := range []struct {
		name, details, content string
		parseOK, bridgeOK      bool
	}{
		{"explained", `{"type":"refusal","category":"cyber","explanation":"Request declined."}`, `[]`, true, true},
		{"null_details", `null`, `[]`, true, false},
		{"null_fields", `{"type":"refusal","category":null,"explanation":null}`, `[]`, true, false},
		{"empty_explanation", `{"type":"refusal","explanation":" "}`, `[]`, true, false},
		{"partial_text", `{"type":"refusal","explanation":"Request declined."}`, `[{"type":"text","text":"Partial"}]`, true, false},
		{"partial_tool", `{"type":"refusal","explanation":"Request declined."}`, `[{"type":"tool_use","id":"c","name":"read","input":{}}]`, true, false},
		{"array_details", `[]`, `[]`, false, false},
		{"bad_type", `{"type":"other","explanation":"Request declined."}`, `[]`, false, false},
		{"bad_explanation", `{"type":"refusal","explanation":42}`, `[]`, false, false},
		{"bad_category", `{"type":"refusal","category":[]}`, `[]`, false, false},
		{"null_content", `{"type":"refusal","explanation":"Request declined."}`, `null`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"id":"msg_refusal","type":"message","role":"assistant","model":"upstream","content":` + tc.content + `,"stop_reason":"refusal","stop_details":` + tc.details + `,"usage":{"input_tokens":65,"output_tokens":0,"cache_creation_input_tokens":3033,"cache_creation":{"ephemeral_1h_input_tokens":3033,"ephemeral_5m_input_tokens":0}}}`)
			result, err := ParseDialogueResult("anthropic-messages", raw)
			if (err == nil) != tc.parseOK {
				t.Fatalf("拒绝响应解析结果错误: %v", err)
			}
			if !tc.parseOK {
				return
			}
			if !result.Refused || result.Stop != "stop" || result.Usage["completion_tokens"] != float64(0) || result.Usage["cache_creation_input_tokens"] != float64(3033) {
				t.Fatalf("拒绝或实测用量丢失: %+v", result)
			}
			native, err := EncodeDialogueResult(result, "anthropic-messages", "public")
			if err != nil {
				t.Fatal(err)
			}
			var expected, actual map[string]any
			json.Unmarshal(raw, &expected)
			json.Unmarshal(native, &actual)
			expected["model"] = "public"
			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("原生拒绝被改变: %s", native)
			}
			for _, target := range []string{"openai-chat", "openai-responses", "gemini"} {
				encoded, err := EncodeDialogueResult(result, target, "public")
				want := tc.bridgeOK && target != "gemini"
				if (err == nil) != want {
					t.Fatalf("%s 桥接边界错误: %s %v", target, encoded, err)
				}
				if !want {
					continue
				}
				var output map[string]any
				json.Unmarshal(encoded, &output)
				if target == "openai-chat" {
					choice := output["choices"].([]any)[0].(map[string]any)
					message := choice["message"].(map[string]any)
					if message["refusal"] != "Request declined." || message["content"] != nil || choice["finish_reason"] != "stop" {
						t.Fatalf("Chat 拒绝契约错误: %s", encoded)
					}
				} else {
					item := output["output"].([]any)[0].(map[string]any)
					part := item["content"].([]any)[0].(map[string]any)
					if part["type"] != "refusal" || part["refusal"] != "Request declined." || !strings.HasPrefix(item["id"].(string), "msg_") {
						t.Fatalf("Responses 拒绝契约错误: %s", encoded)
					}
				}
			}
		})
	}
}

// TestPublicRefusalHistory 验证本次输出的 Chat/Responses 拒绝能作为公开助手历史续轮，而错误角色、未知字段和混合内容不得悄悄丢弃。
// 参数 t 为上下文；前置合成完整历史，检查 Messages 请求保留说明且原生输入未修改；无网络和外部清理。
func TestPublicRefusalHistory(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, item string
		valid                bool
	}{
		{"chat", "openai-chat", `{"role":"assistant","content":null,"refusal":"Request declined."}`, true},
		{"responses", "openai-responses", `{"type":"message","id":"msg_refusal","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"Request declined."}]}`, true},
		{"user", "openai-responses", `{"type":"message","role":"user","content":[{"type":"refusal","refusal":"Request declined."}]}`, false},
		{"signature", "openai-responses", `{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"Request declined.","signature":"opaque"}]}`, false},
		{"missing", "openai-responses", `{"type":"message","role":"assistant","content":[{"type":"refusal"}]}`, false},
		{"chat_bad", "openai-chat", `{"role":"assistant","content":null,"refusal":42}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := "messages"
			if tc.protocol == "openai-responses" {
				key = "input"
			}
			var item map[string]any
			if err := json.Unmarshal([]byte(tc.item), &item); err != nil {
				t.Fatal(err)
			}
			body := map[string]any{key: []any{map[string]any{"role": "user", "content": "First question"}, item, map[string]any{"role": "user", "content": "New question"}}, "model": "public"}
			d, err := ParseDialogue(tc.protocol, body)
			if (err == nil) != tc.valid {
				t.Fatalf("公开拒绝历史校验错误: %v", err)
			}
			if !tc.valid {
				return
			}
			encoded, err := EncodeDialogue(d, "anthropic-messages", "private")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(encoded)
			if strings.Count(string(raw), "Request declined.") != 1 || !strings.Contains(string(raw), "New question") || strings.Contains(string(raw), `"refusal"`) {
				t.Fatalf("公开拒绝未映射为真实assistant历史: %s", raw)
			}
			original, _ := json.Marshal(item)
			if !strings.Contains(string(original), `"refusal"`) {
				t.Fatal("原生请求被投影修改")
			}
		})
	}
}

// TestMixedRefusalHistoryKeepsNative 验证合法混合/空拒绝不会破坏已有同协议重放，但跨协议不能静默丢弃拒绝。
// 参数 t 为上下文；前置公开 Chat/Responses 历史，逐字段比较 Native 原文并断言桥接错误；仅内存数据无需清理。
func TestMixedRefusalHistoryKeepsNative(t *testing.T) {
	for _, tc := range []struct{ name, protocol, item string }{
		{"chat_mixed", "openai-chat", `{"role":"assistant","content":"Partial","refusal":"Request declined."}`},
		{"chat_empty", "openai-chat", `{"role":"assistant","content":null,"refusal":""}`},
		{"responses_mixed", "openai-responses", `{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"Request declined."},{"type":"output_text","text":"Partial"}]}`},
		{"responses_empty", "openai-responses", `{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":""}]}`},
		{"responses_two", "openai-responses", `{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"First refusal"},{"type":"refusal","refusal":"Second refusal"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var item map[string]any
			if err := json.Unmarshal([]byte(tc.item), &item); err != nil {
				t.Fatal(err)
			}
			key := "messages"
			if tc.protocol == "openai-responses" {
				key = "input"
			}
			body := map[string]any{"model": "public", key: []any{item, map[string]any{"role": "user", "content": "New question"}}}
			d, err := ParseDialogue(tc.protocol, body)
			if err != nil || d.BridgeError == "" {
				t.Fatalf("合法原生拒绝被拒绝或丢失桥接边界: %v %+v", err, d)
			}
			native, err := EncodeDialogue(d, tc.protocol, "public")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(native[key], body[key]) {
				t.Fatalf("原生拒绝历史被修改: %v", native)
			}
			if _, err = EncodeDialogue(d, "anthropic-messages", "private"); err == nil {
				t.Fatal("混合/空拒绝历史静默跨协议")
			}
		})
	}
}
