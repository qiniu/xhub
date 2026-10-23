package deployment

import (
	"github.com/sunqirui1987/xhub/internal/config"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	"testing"
)

// TestNativeResponsesSelection 验证同协议原生、跨协议转换及未知传输边界，关闭后回到原有校验；纯内存无清理。
func TestNativeResponsesSelection(t *testing.T) {
	d, err := llmdialogue.ParseNativeResponses(map[string]any{"input": []any{map[string]any{"type": "future_item", "data": "keep"}}})
	if err != nil {
		t.Fatal(err)
	}
	d.DeferredResponses = true
	for _, tc := range []struct {
		transport string
		want      bool
	}{
		{"bypass_openai_responses", true}, {"bypass_openai_chat", false}, {"bypass_anthropic_messages", false}, {"unknown", false},
	} {
		dep := config.ModelEntry{ModelInfo: map[string]any{"transport": tc.transport}, LiteLLMParams: map[string]any{"model": "actual"}}
		out, _, err := EncodeDialogue(dep, d)
		if (err == nil) != tc.want {
			t.Fatalf("候选模式错误 %+v: %v", tc, err)
		}
		if tc.want && (out["model"] != "actual" || out["store"] != false) {
			t.Fatal("原生出站契约丢失")
		}
	}
	// 未被可信入口标记的请求不能仅靠原生部署改变原有对话。
	legacy, _ := llmdialogue.ParseDialogue("openai-responses", map[string]any{"input": "Hello"})
	dep := config.ModelEntry{ModelInfo: map[string]any{"transport": "bypass_openai_responses"}}
	out, err := PrepareDialogue(dep, legacy)
	if err != nil || out.NativeResponses {
		t.Fatal("原生部署绕过了账号灰度")
	}
}

// TestNativeResponsesConfigured 验证仅原生 Responses 目录需要查询灰度开关；纯内存无需清理。
func TestNativeResponsesConfigured(t *testing.T) {
	if NativeResponsesConfigured(nil) {
		t.Fatal("空目录误开启")
	}
	for _, tc := range []struct {
		transport string
		want      bool
	}{{"bypass_openai_responses", true}, {"bypass_openai_chat", false}, {"unknown", false}} {
		if NativeResponsesConfigured([]config.ModelEntry{{ModelInfo: map[string]any{"transport": tc.transport}}}) != tc.want {
			t.Fatalf("目录协议检测错误: %+v", tc)
		}
	}
}

// TestNativeResponsesCrossProtocol 验证已知文本跨协议转换成功，未支持扩展不被静默丢弃；纯内存无清理。
func TestNativeResponsesCrossProtocol(t *testing.T) {
	d, err := llmdialogue.ParseNativeResponses(map[string]any{"input": "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	d.DeferredResponses = true
	dep := config.ModelEntry{ModelInfo: map[string]any{"transport": "bypass_openai_chat"}, LiteLLMParams: map[string]any{"model": "actual"}}
	out, _, err := EncodeDialogue(dep, d)
	if err != nil || out["messages"] == nil || out["input"] != nil {
		t.Fatalf("Responses 未转换成 Chat: %v %v", out, err)
	}
}
