package requestlog

import (
	"strings"
	"testing"
)

// TestReasoningEffort 前置内存请求正文；验证三协议/原生前缀、未知强度、缺省和非法输入，原文保持且不产生持久化数据。
func TestReasoningEffort(t *testing.T) {
	for _, tc := range []struct{ name, path, body, want string }{
		{"Chat", "/v1/chat/completions", `{"reasoning_effort":"high","messages":[{"content":"private prompt"}]}`, "high"},
		{"Responses", "/v1/responses", `{"reasoning":{"effort":"xhigh"}}`, "xhigh"},
		{"Messages", "/v1/messages", `{"output_config":{"effort":"max"}}`, "max"},
		{"原生入口", "/bypass/openai/v1/responses/", `{"reasoning":{"effort":"low"}}`, "low"},
		{"明确不推理", "/v1/chat/completions", `{"reasoning_effort":"none"}`, "none"},
		{"未来厂商值", "/v1/messages", `{"output_config":{"effort":"future_effort-2"}}`, "future_effort-2"},
		{"未指定", "/v1/responses", `{"model":"reasoning-model"}`, ""},
		{"不推断预算", "/v1/messages", `{"thinking":{"type":"enabled","budget_tokens":16000}}`, ""},
		{"不推断自适应", "/v1/messages", `{"thinking":{"type":"adaptive"}}`, ""},
		{"不跨字段猜测", "/v1/responses", `{"reasoning_effort":"high"}`, ""},
		{"不读取提示词", "/v1/chat/completions", `{"messages":[{"content":"reasoning_effort=high"}]}`, ""},
		{"非文本端点", "/v1/images/generations", `{"reasoning_effort":"high"}`, ""},
		{"非法JSON", "/v1/chat/completions", `{"reasoning_effort":`, ""},
		{"根数组", "/v1/chat/completions", `[]`, ""},
		{"嵌套类型错误", "/v1/responses", `{"reasoning":"high"}`, ""},
		{"数值", "/v1/chat/completions", `{"reasoning_effort":123}`, ""},
		{"空值", "/v1/chat/completions", `{"reasoning_effort":null}`, ""},
		{"对象", "/v1/chat/completions", `{"reasoning_effort":{"effort":"high"}}`, ""},
		{"空串", "/v1/chat/completions", `{"reasoning_effort":""}`, ""},
		{"含私密文本", "/v1/chat/completions", `{"reasoning_effort":"private prompt text"}`, ""},
		{"长度上限", "/v1/chat/completions", `{"reasoning_effort":"` + strings.Repeat("a", 32) + `"}`, strings.Repeat("a", 32)},
		{"超长", "/v1/chat/completions", `{"reasoning_effort":"` + strings.Repeat("a", 33) + `"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			if got := ReasoningEffort(tc.path, body); got != tc.want {
				t.Fatalf("推理强度=%q，期望=%q", got, tc.want)
			}
			if string(body) != tc.body {
				t.Fatal("日志提取修改了用户请求")
			}
		})
	}
}
