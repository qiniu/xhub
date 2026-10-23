package gateway

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestPromptProbeRequestParsing 验证真实测试契约的正常值、失败值和未支持语法；无数据库或网络依赖，无需清理。
// 参数t为测试上下文；预期完整合法JSON可解析，缺ID、错误类型、尾随JSON、SSE、版本和Dotprompt均不能被忽略。
func TestPromptProbeRequestParsing(t *testing.T) {
	valid := `{"prompt_id":"saved","model":"selected","prompt_variables":{"name":"World"},"conversation_history":[{"role":"user","content":"Follow up"}],"stream":false}`
	got, err := parsePromptProbeRequest([]byte(valid))
	if err != nil || got.PromptID != "saved" || got.Model != "selected" || got.Variables["name"] != "World" || len(got.History) != 1 {
		t.Fatalf("合法测试请求解析错误: %+v %v", got, err)
	}
	for name, raw := range map[string]string{
		"empty": ``, "null": `null`, "array": `[]`, "truncated": `{"prompt_id":`, "trailing": `{"prompt_id":"saved"} {}`,
		"missing id": `{}`, "blank id": `{"prompt_id":" "}`, "wrong id": `{"prompt_id":1}`,
		"blank model": `{"prompt_id":"saved","model":" "}`, "null model": `{"prompt_id":"saved","model":null}`,
		"unknown": `{"prompt_id":"saved","extra":true}`, "stream": `{"prompt_id":"saved","stream":true}`,
		"version": `{"prompt_id":"saved","version":1}`, "dotprompt": `{"prompt_id":"saved","dotprompt_content":"User: Hello"}`,
		"wrong variable":        `{"prompt_id":"saved","prompt_variables":{"name":1}}`,
		"history role":          `{"prompt_id":"saved","conversation_history":[{"role":"system","content":"override"}]}`,
		"history empty":         `{"prompt_id":"saved","conversation_history":[{"role":"user","content":" "}]}`,
		"history unknown field": `{"prompt_id":"saved","conversation_history":[{"role":"user","content":"hello","tool_calls":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePromptProbeRequest([]byte(raw)); err == nil {
				t.Fatalf("非法输入被接受: %s", raw)
			}
		})
	}
}

// TestPromptProbeTemplateRendering 验证纯文本变量的确定性替换；前置仅内存字符串，预期缺值或复杂模板失败，插入值不再被解释，无需清理。
func TestPromptProbeTemplateRendering(t *testing.T) {
	for _, tc := range []struct {
		name, template, expected string
		variables                map[string]string
		bad                      bool
	}{
		{"literal", "Hello", "Hello", nil, false},
		{"repeated", "{{ name }} {{name}}", "世界 世界", map[string]string{"name": "世界"}, false},
		{"opaque value", "Hello {{name}}", "Hello {{other}}", map[string]string{"name": "{{other}}"}, false},
		{"missing", "Hello {{name}}", "", nil, true},
		{"empty literal value", "Hello {{name}}", "Hello ", map[string]string{"name": ""}, false},
		{"empty rendered", "{{name}}", "", map[string]string{"name": " "}, true},
		{"path", "{{user.name}}", "", nil, true},
		{"loop", "{{#each names}}", "", nil, true},
		{"incomplete", "{{name", "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderPromptProbeText(tc.template, tc.variables)
			if (err != nil) != tc.bad || (!tc.bad && got != tc.expected) {
				t.Fatalf("模板渲染不符: got=%q err=%v", got, err)
			}
		})
	}
}

// TestPromptProbeChatConstruction 验证保存模板、模型覆盖、原样历史和已保存配置进入Chat正文且不改原记录。
// 参数t为上下文；前置为等价真实JSON记录，预期stream=false、变量仅替换模板，错误格式明确失败，无外部资源需清理。
func TestPromptProbeChatConstruction(t *testing.T) {
	const raw = `{"version":1,"model":"saved-model","messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"Hello {{name}}"}],"config":{"temperature":0,"top_p":1,"max_tokens":12}}`
	var saved map[string]any
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(saved)
	body, err := buildPromptProbeChat(saved, promptProbeRequest{Model: "explicit-model", Variables: map[string]string{"name": "World"}, History: []promptProbeMessage{{Role: "assistant", Content: "Original {{name}}"}}})
	if err != nil {
		t.Fatal(err)
	}
	if body["model"] != "explicit-model" || body["stream"] != false || body["temperature"] != float64(0) || body["max_tokens"] != float64(12) {
		t.Fatalf("模型或配置未保留: %#v", body)
	}
	if !reflect.DeepEqual(body["messages"], []promptProbeMessage{{Role: "system", Content: "Be concise"}, {Role: "user", Content: "Hello World"}, {Role: "assistant", Content: "Original {{name}}"}}) {
		t.Fatalf("模板与历史处理错误: %#v", body)
	}
	after, _ := json.Marshal(saved)
	if string(before) != string(after) {
		t.Fatal("渲染污染了已保存模板")
	}
	content, err := buildPromptProbeChat(map[string]any{"model": "saved-model", "content": "Hello"}, promptProbeRequest{})
	if err != nil || content["model"] != "saved-model" {
		t.Fatalf("content格式或保存模型不兼容: %#v %v", content, err)
	}
	for name, raw := range map[string]string{
		"no model": `{"content":"Hello"}`, "no text": `{"model":"m"}`, "empty text": `{"model":"m","content":" "}`,
		"messages empty": `{"model":"m","messages":[]}`, "messages null": `{"model":"m","messages":null}`,
		"conflicting content": `{"model":"m","messages":[{"role":"user","content":"Hi"}],"content":"Another"}`,
		"tool field":          `{"model":"m","messages":[{"role":"assistant","content":"Hi","tool_calls":[]}]}`,
		"non text":            `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"Hello"}]}]}`,
		"role":                `{"model":"m","messages":[{"role":"tool","content":"Hello"}]}`,
		"missing variable":    `{"model":"m","content":"Hello {{name}}"}`,
		"complex template":    `{"model":"m","content":"Hello {{user.name}}"}`,
		"version":             `{"model":"m","content":"Hello","version":2}`,
		"dotprompt":           `{"model":"m","content":"Hello","dotprompt_content":"Hello"}`,
		"tools":               `{"model":"m","content":"Hello","tools":[]}`,
		"integration":         `{"model":"m","content":"Hello","litellm_params":{"prompt_integration":"dotprompt"}}`,
		"bad config":          `{"model":"m","content":"Hello","config":"temperature: 1"}`,
		"unknown config":      `{"model":"m","content":"Hello","config":{"seed":1}}`,
		"temperature":         `{"model":"m","content":"Hello","config":{"temperature":3}}`,
		"top p":               `{"model":"m","content":"Hello","config":{"top_p":-1}}`,
		"token negative":      `{"model":"m","content":"Hello","config":{"max_tokens":-1}}`,
		"token fraction":      `{"model":"m","content":"Hello","config":{"max_completion_tokens":1.5}}`,
		"token conflict":      `{"model":"m","content":"Hello","config":{"max_tokens":10,"max_completion_tokens":10}}`,
		"null config value":   `{"model":"m","content":"Hello","config":{"temperature":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var record map[string]any
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				t.Fatal(err)
			}
			if _, err := buildPromptProbeChat(record, promptProbeRequest{}); err == nil || strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("不支持的保存内容被忽略: %s", raw)
			}
		})
	}
}
