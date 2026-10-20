package dialogue

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// codexMessagesFixture 为参数桥接测试提供无推理的 Codex 请求；返回独立正文，无网络与清理需求。
// 工具名和英文正文固定，调用方只修改待测字段，避免用真实客户端凭据或会话内容。
func codexMessagesFixture(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(`{"model":"alias","input":"Call echo with the supplied value.","tools":[{"type":"function","name":"echo","parameters":{"type":"object","properties":{"value":{"type":"string"}}},"strict":false}],"reasoning":{"effort":"none","summary":"auto"},"parallel_tool_calls":true,"prompt_cache_key":"synthetic-session","include":["reasoning.encrypted_content"],"text":{"format":{"type":"text"}},"store":false}`), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestCodexMessagesSafeOptions 验证明确禁用推理时 Codex 默认非推理参数可映射至 Messages。
// 参数 t 为测试上下文；前置真实字段形状，断言正文、schema、工具并行语义及原生往返不变；纯内存无需清理。
func TestCodexMessagesSafeOptions(t *testing.T) {
	body := codexMessagesFixture(t)
	before, _ := cloneObject(body)
	d, err := ParseDialogue("openai-responses", body)
	if err != nil {
		t.Fatal(err)
	}
	out, err := EncodeDialogue(d, "anthropic-messages", "claude-test")
	if err != nil {
		t.Fatalf("显式 none 的 Codex 参数被错误拒绝: %v", err)
	}
	if dialogueMap(out["thinking"])["type"] != "disabled" || dialogueMap(out["tool_choice"])["disable_parallel_tool_use"] != false {
		t.Fatalf("推理或工具语义丢失: %#v", out)
	}
	tool := dialogueMap(dialogueList(out["tools"])[0])
	if _, ok := tool["strict"]; ok {
		t.Fatal("不支持的 strict=false 未规范化")
	}
	if !reflect.DeepEqual(tool["input_schema"], dialogueMap(dialogueList(body["tools"])[0])["parameters"]) {
		t.Fatal("工具 schema 改变")
	}
	for _, k := range []string{"prompt_cache_key", "cache_control", "include", "text", "reasoning", "parallel_tool_calls"} {
		if _, ok := out[k]; ok {
			t.Fatalf("Messages 出现未映射字段 %s", k)
		}
	}
	if !reflect.DeepEqual(body, before) {
		t.Fatal("跨协议编码修改了源请求")
	}
	native, err := EncodeDialogue(d, "openai-responses", "alias")
	if err != nil || !reflect.DeepEqual(native, before) {
		t.Fatalf("原生请求字段丢失: %v %#v", err, native)
	}
}

// TestCodexMessagesParallelChoice 验证自动、指定、强制与禁止工具在两种并行设置下的等价映射。
// 参数 t 为测试上下文；前置有效 Codex 工具请求，检查 Messages 的选择类型和禁用并行开关；内存数据无需清理。
func TestCodexMessagesParallelChoice(t *testing.T) {
	for _, choice := range []struct {
		name     string
		value    any
		expected string
	}{
		{"default", nil, "auto"}, {"auto", "auto", "auto"}, {"required", "required", "any"}, {"none", "none", "none"}, {"named", map[string]any{"type": "function", "name": "echo"}, "tool"},
	} {
		for _, parallel := range []bool{false, true} {
			t.Run(choice.name+map[bool]string{true: "_parallel", false: "_serial"}[parallel], func(t *testing.T) {
				body := codexMessagesFixture(t)
				body["parallel_tool_calls"] = parallel
				if choice.value != nil {
					body["tool_choice"] = choice.value
				}
				d, err := ParseDialogue("openai-responses", body)
				if err != nil {
					t.Fatal(err)
				}
				out, err := EncodeDialogue(d, "anthropic-messages", "claude-test")
				if err != nil {
					t.Fatal(err)
				}
				actual := dialogueMap(out["tool_choice"])
				if actual["type"] != choice.expected {
					t.Fatalf("工具选择变为 %v", actual)
				}
				if choice.expected == "none" {
					if _, exists := actual["disable_parallel_tool_use"]; exists {
						t.Fatal("none 出现非法并行字段")
					}
				} else if actual["disable_parallel_tool_use"] != !parallel {
					t.Fatalf("并行语义改变: %v", actual)
				}
			})
		}
	}
	t.Run("no_tools", func(t *testing.T) {
		body := codexMessagesFixture(t)
		delete(body, "tools")
		d, err := ParseDialogue("openai-responses", body)
		if err != nil {
			t.Fatal(err)
		}
		out, err := EncodeDialogue(d, "anthropic-messages", "claude-test")
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := out["tool_choice"]; exists {
			t.Fatal("没有工具却注入工具选择")
		}
	})
}

// TestCodexMessagesUnsupportedOptions 验证非等价参数、错误类型与签名推理在发送前明确拒绝。
// 参数 t 为测试上下文；使用独立内存请求替换目标字段，期待能力错误且不创建原生 thinking；无外部数据清理。
func TestCodexMessagesUnsupportedOptions(t *testing.T) {
	cases := map[string]func(map[string]any){
		"strict_true":         func(b map[string]any) { dialogueMap(dialogueList(b["tools"])[0])["strict"] = true },
		"strict_wrong_type":   func(b map[string]any) { dialogueMap(dialogueList(b["tools"])[0])["strict"] = "false" },
		"low_signed_thinking": func(b map[string]any) { b["reasoning"] = map[string]any{"effort": "low", "summary": "auto"} },
		"unknown_reasoning":   func(b map[string]any) { b["reasoning"] = map[string]any{"effort": "none", "extra": true} },
		"bad_summary":         func(b map[string]any) { b["reasoning"] = map[string]any{"effort": "none", "summary": false} },
		"bad_cache_type":      func(b map[string]any) { b["prompt_cache_key"] = 42 },
		"retention":           func(b map[string]any) { b["prompt_cache_retention"] = "24h" },
		"verbosity":           func(b map[string]any) { b["text"] = map[string]any{"verbosity": "low"} },
		"json_schema": func(b map[string]any) {
			b["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": map[string]any{"type": "object"}}}
		},
		"invalid_format":    func(b map[string]any) { b["text"] = map[string]any{"format": false} },
		"bad_parallel_type": func(b map[string]any) { b["parallel_tool_calls"] = "true" },
		"unknown_include":   func(b map[string]any) { b["include"] = []any{"web_search_call.action.sources"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			body := codexMessagesFixture(t)
			mutate(body)
			d, err := ParseDialogue("openai-responses", body)
			if err == nil {
				_, err = EncodeDialogue(d, "anthropic-messages", "claude-test")
			}
			if err == nil {
				t.Fatal("非等价参数被静默接受")
			}
			if name == "low_signed_thinking" && !strings.Contains(err.Error(), "signed thinking") {
				t.Fatalf("未说明签名推理边界: %v", err)
			}
		})
	}
}

// TestMessagesReasoningNoneAndDefault 验证 Chat 的 none 与未指定 effort 的边界。
// 参数 t 为上下文；合法 none 输出 disabled，缺省不注入，非 none 失败且原文不修改；纯内存无需清理。
func TestMessagesReasoningNoneAndDefault(t *testing.T) {
	for _, effort := range []any{nil, "none", "low", true} {
		out := map[string]any{}
		err := bridgeMessagesReasoning(effort, out)
		if effort == nil {
			if err != nil || len(out) != 0 {
				t.Fatalf("缺省 effort 被改写: %v %v", out, err)
			}
		} else if effort == "none" {
			if err != nil || dialogueMap(out["thinking"])["type"] != "disabled" {
				t.Fatalf("none 未映射: %v %v", out, err)
			}
		} else if err == nil || len(out) != 0 {
			t.Fatalf("不支持 effort 被启用: %v %v", out, err)
		}
	}
	for _, effort := range []string{"none", "low"} {
		d, err := ParseDialogue("openai-chat", map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Hello"}}, "reasoning_effort": effort})
		if err != nil {
			t.Fatal(err)
		}
		out, err := EncodeDialogue(d, "anthropic-messages", "claude-test")
		if effort == "none" {
			if err != nil || dialogueMap(out["thinking"])["type"] != "disabled" {
				t.Fatalf("Chat none 未映射: %v %v", out, err)
			}
		} else if err == nil {
			t.Fatal("Chat 推理错误放行")
		}
	}
}
