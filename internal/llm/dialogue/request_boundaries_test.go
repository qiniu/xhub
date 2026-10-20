package dialogue

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestRequestValidationBoundaries 验证请求字段、内容块和工具定义的失败契约，正常输入由协议矩阵覆盖。
// 参数 t 为单测上下文；前置内存 JSON，错误必须说明对应字段，不调用上游，无数据需要清理。
func TestRequestValidationBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, protocol, raw, want string }{
		{"unknown", "unknown", "{}", "unregistered"},
		{"empty", "openai-chat", "{}", "nonempty"},
		{"item_type", "openai-chat", "{\"messages\":[42]}", "object"},
		{"role", "openai-chat", "{\"messages\":[{\"role\":\"invalid\",\"content\":\"x\"}]}", "role"},
		{"empty_content", "openai-chat", "{\"messages\":[{\"role\":\"user\",\"content\":null}]}", "no supported content"},
		{"stream_options_type", "openai-chat", "{\"stream_options\":42}", "object"},
		{"stream_options_field", "openai-chat", "{\"stream_options\":{\"extra\":true}}", "extra"},
		{"stream_options_boolean", "openai-chat", "{\"stream_options\":{\"include_usage\":42}}", "boolean"},
		{"limits", "openai-chat", "{\"max_tokens\":1,\"max_output_tokens\":2}", "mutually exclusive"},
		{"system", "anthropic-messages", "{\"system\":42}", "content"},
		{"response_kind", "openai-responses", "{\"input\":[{\"type\":\"image\"}]}", "input item"},
		{"response_output", "openai-responses", "{\"input\":[{\"type\":\"function_call_output\",\"output\":42}]}", "content"},
		{"content_type", "anthropic-messages", "{\"messages\":[{\"role\":\"user\",\"content\":42}]}", "content"},
		{"block_type", "anthropic-messages", "{\"messages\":[{\"role\":\"user\",\"content\":[42]}]}", "object"},
		{"missing_image_source", "anthropic-messages", "{\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"image\"}]}]}", "source"},
		{"tool_result_error", "anthropic-messages", "{\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"is_error\":true}]}]}", "is_error"},
		{"tool_result_content", "anthropic-messages", "{\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"content\":42}]}]}", "content"},
		{"call_array", "openai-chat", "{\"messages\":[{\"role\":\"assistant\",\"tool_calls\":42}]}", "array"},
		{"call_type", "openai-chat", "{\"messages\":[{\"role\":\"assistant\",\"tool_calls\":[{\"type\":\"custom\"}]}]}", "function"},
		{"call_field", "openai-chat", "{\"messages\":[{\"role\":\"assistant\",\"tool_calls\":[{\"type\":\"function\",\"extra\":true}]}]}", "extra"},
		{"call_function", "openai-chat", "{\"messages\":[{\"role\":\"assistant\",\"tool_calls\":[{\"type\":\"function\",\"function\":{\"extra\":true}}]}]}", "extra"},
		{"result_id", "openai-chat", "{\"messages\":[{\"role\":\"tool\",\"content\":\"x\"}]}", "ID"},
		{"tool_type", "openai-chat", "{\"messages\":[{\"role\":\"user\",\"content\":\"x\"}],\"tools\":[{\"type\":\"custom\"}]}", "function"},
		{"tool_field", "openai-chat", "{\"messages\":[{\"role\":\"user\",\"content\":\"x\"}],\"tools\":[{\"type\":\"function\",\"extra\":true}]}", "extra"},
		{"response_tool_type", "openai-responses", "{\"input\":\"x\",\"tools\":[{\"type\":\"custom\"}]}", "name"},
		{"tool_definition_field", "openai-responses", "{\"input\":\"x\",\"tools\":[{\"type\":\"function\",\"name\":\"f\",\"parameters\":{},\"extra\":true}]}", "extra"},
		{"tool_schema", "openai-responses", "{\"input\":\"x\",\"tools\":[{\"type\":\"function\",\"name\":\"f\",\"parameters\":null}]}", "schema"},
		{"tool_strict", "openai-responses", "{\"input\":\"x\",\"tools\":[{\"type\":\"function\",\"name\":\"f\",\"parameters\":{},\"strict\":42}]}", "strict"},
		{"tool_name", "openai-responses", "{\"input\":\"x\",\"tools\":[{\"type\":\"function\",\"parameters\":{}}]}", "name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal([]byte(tc.raw), &body); err != nil {
				t.Fatal(err)
			}
			_, err := ParseDialogue(tc.protocol, body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("未按契约拒绝 %s: %v", tc.name, err)
			}
		})
	}
	// 非 JSON 值不能从 HTTP 进入，但公共解析函数仍需明确拒绝无法编码的工具输入。
	_, err := ParseDialogue("anthropic-messages", map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "input": make(chan int)}}}}})
	if err == nil {
		t.Fatal("不可编码的工具输入被接受")
	}
}

// TestRequestOptionsAndTextBlocks 验证 Responses 指令、文本块、数值配置及 strict 定义保持；前置内存有效请求。
// 参数 t 为测试上下文；返回对话字段应等价于原输入，Anthropic 字符串内容亦支持，无外部副作用或清理。
func TestRequestOptionsAndTextBlocks(t *testing.T) {
	body := map[string]any{"input": "hello", "instructions": "rules", "temperature": 0.5, "top_p": 0.8, "stream": true, "stream_options": map[string]any{"include_usage": true}, "tool_choice": "auto", "tools": []any{map[string]any{"type": "function", "name": "f", "parameters": map[string]any{}, "description": "Find", "strict": true}}}
	d, err := ParseDialogue("openai-responses", body)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Stream || !reflect.DeepEqual(d.Turns, []Turn{{Role: "system", Text: "rules"}, {Role: "user", Text: "hello"}}) || d.Options["temperature"] != 0.5 || d.Options["tool_choice"] != "auto" || d.Tools[0]["description"] != "Find" || d.Tools[0]["strict"] != true {
		t.Fatalf("请求信息丢失: %+v", d)
	}
	for _, protocol := range []string{"openai-chat", "anthropic-messages"} {
		for _, content := range []any{"hello", []any{map[string]any{"type": "text", "text": "he"}, map[string]any{"type": "text", "text": "llo"}}} {
			d, err := ParseDialogue(protocol, map[string]any{"messages": []any{map[string]any{"role": "user", "content": content}}})
			if err != nil || len(d.Turns) != 1 || d.Turns[0].Text != "hello" {
				t.Fatalf("%s 文本块丢失: %+v %v", protocol, d, err)
			}
		}
	}
}

// TestToolChoiceConversion 验证强制工具和 auto/any/none 在五种上游间往返，并明确拒绝无效选择。
// 参数 t 为上下文；有效共享对话无外部依赖，断言解析后的选择语义和模型保持，无数据需清理。
func TestToolChoiceConversion(t *testing.T) {
	choices := []struct {
		name  string
		value any
		want  string
	}{
		{"auto", "auto", "auto"}, {"required", "required", "required"}, {"none", "none", "none"},
		{"object_auto", map[string]any{"type": "auto"}, "auto"}, {"object_any", map[string]any{"type": "any"}, "required"}, {"object_none", map[string]any{"type": "none"}, "none"},
		{"named", map[string]any{"type": "tool", "name": "find"}, "find"}, {"nested", map[string]any{"type": "function", "function": map[string]any{"name": "find"}}, "find"},
	}
	for _, protocol := range []string{"openai-chat", "openai-responses", "anthropic-messages", "gemini", "vertex"} {
		for _, tc := range choices {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				d := Dialogue{Stream: true, Turns: []Turn{{Role: "user", Text: "hello"}}, Options: map[string]any{"temperature": 0.5, "top_p": 0.8, "tool_choice": tc.value}}
				out, err := EncodeDialogue(d, protocol, "namespace/model")
				if err != nil {
					t.Fatal(err)
				}
				if protocol == "openai-chat" && dialogueMap(out["stream_options"])["include_usage"] != true {
					t.Fatal("流缺少实测用量请求")
				}
				if protocol == "anthropic-messages" && out["max_tokens"] != 4096 {
					t.Fatal("默认输出限制丢失")
				}
				delete(out, "store")
				back, err := ParseDialogue(protocol, out)
				if err != nil {
					t.Fatal(err)
				}
				normalized, err := EncodeDialogue(back, "openai-responses", "namespace/model")
				if err != nil {
					t.Fatal(err)
				}
				choice := normalized["tool_choice"]
				if tc.want == "find" {
					choice = dialogueMap(choice)["name"]
				}
				if choice != tc.want || back.Options["temperature"] != 0.5 || back.Options["top_p"] != 0.8 || normalized["model"] != "namespace/model" {
					t.Fatalf("选择或配置改变: %+v", normalized)
				}
			})
		}
		if _, err := EncodeDialogue(Dialogue{Turns: []Turn{{Role: "user", Text: "x"}}, Options: map[string]any{"tool_choice": "invalid"}}, protocol, "m"); err == nil {
			t.Fatalf("%s 接受无效选择", protocol)
		}
	}
}

// TestRequestEncodingBoundaries 验证 Messages 相邻角色合并、Google 系统拼接和纯文本工具结果封装，并拒绝不可表达能力。
// 参数 t 为上下文；前置纯内存对话，编码不能修改工具定义；无持久化或外部清理。
func TestRequestEncodingBoundaries(t *testing.T) {
	d := Dialogue{Turns: []Turn{{Role: "system", Text: "one"}, {Role: "developer", Text: "two"}, {Role: "user", Text: "hello"}, {Role: "user", Text: "again"}, {Role: "assistant", Calls: []Turn{{ID: "c", Name: "f", Arguments: "{}"}}}, {Role: "tool", ID: "c", Text: "plain"}}}
	out, err := EncodeDialogue(d, "anthropic-messages", "m")
	if err != nil || out["system"] != "one\ntwo" || len(dialogueList(out["messages"])) != 3 || len(dialogueList(dialogueMap(dialogueList(out["messages"])[0])["content"])) != 2 {
		t.Fatalf("Messages 合并不正确: %v %v", out, err)
	}
	out, err = EncodeDialogue(d, "gemini", "m")
	if err != nil {
		t.Fatal(err)
	}
	parts := dialogueList(dialogueMap(dialogueList(out["contents"])[3])["parts"])
	if dialogueMap(dialogueMap(parts[0])["functionResponse"])["name"] != "f" || dialogueMap(dialogueMap(dialogueMap(parts[0])["functionResponse"])["response"])["result"] != "plain" {
		t.Fatalf("Google 纯文本结果未封装: %+v", out)
	}
	if sys, err := googleText(dialogueMap(out["systemInstruction"])["parts"]); err != nil || sys != "one\ntwo" {
		t.Fatalf("系统指令改变: %q %v", sys, err)
	}
	for _, tc := range []struct {
		name, protocol string
		d              Dialogue
	}{
		{"unknown", "unknown", d},
		{"bad_messages_args", "anthropic-messages", Dialogue{Turns: []Turn{{Role: "assistant", Calls: []Turn{{Arguments: "broken"}}}}}},
		{"orphan_google", "gemini", Dialogue{Turns: []Turn{{Role: "tool", ID: "missing"}}}},
		{"bad_google_args", "gemini", Dialogue{Turns: []Turn{{Role: "assistant", Calls: []Turn{{Arguments: "null"}}}}}},
		{"strict_messages", "anthropic-messages", Dialogue{Tools: []map[string]any{{"strict": true}}}},
		{"strict_google", "gemini", Dialogue{Tools: []map[string]any{{"strict": false}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := EncodeDialogue(tc.d, tc.protocol, "m"); err == nil {
				t.Fatal("不可表达的能力被接受")
			}
		})
	}
}
