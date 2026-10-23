package deployment

import (
	"github.com/sunqirui1987/xhub/internal/config"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	"testing"
)

// TestNativeResponsesSelection 验证默认/观察/开启/非法模式及跨协议边界，关闭后回到原有校验；纯内存无清理。
func TestNativeResponsesSelection(t *testing.T) {
	d, err := llmdialogue.ParseNativeResponses(map[string]any{"input": []any{map[string]any{"type": "future_item", "data": "keep"}}})
	if err != nil {
		t.Fatal(err)
	}
	d.DeferredResponses = true
	for _, tc := range []struct {
		mode      any
		transport string
		want      bool
	}{
		{nil, "bypass_openai_responses", false}, {"legacy", "bypass_openai_responses", false}, {"observe", "bypass_openai_responses", false},
		{"enabled", "bypass_openai_responses", true}, {true, "bypass_openai_responses", false}, {"typo", "bypass_openai_responses", false}, {"enabled", "bypass_openai_chat", false},
	} {
		dep := config.ModelEntry{ModelInfo: map[string]any{"transport": tc.transport}, LiteLLMParams: map[string]any{"model": "actual"}}
		if tc.mode != nil {
			dep.ModelInfo["native_responses_mode"] = tc.mode
		}
		out, _, err := EncodeDialogue(dep, d)
		if (err == nil) != tc.want {
			t.Fatalf("候选模式错误 %+v: %v", tc, err)
		}
		if tc.want && (out["model"] != "actual" || out["store"] != false) {
			t.Fatal("原生出站契约丢失")
		}
	}
	// 未被可信入口标记的请求不能仅靠部署开关改变原有对话。
	legacy, _ := llmdialogue.ParseDialogue("openai-responses", map[string]any{"input": "Hello"})
	dep := config.ModelEntry{ModelInfo: map[string]any{"transport": "bypass_openai_responses", "native_responses_mode": "enabled"}}
	out, err := PrepareDialogue(dep, legacy)
	if err != nil || out.NativeResponses {
		t.Fatal("部署开关绕过了总开关/名单")
	}
}

// TestNativeResponsesConfigured 验证未配置的目录不触发灰度查询，合法原生开关才命中；纯内存无需清理。
func TestNativeResponsesConfigured(t *testing.T) {
	if NativeResponsesConfigured(nil) {
		t.Fatal("空目录误开启")
	}
	for _, tc := range []struct {
		mode, transport string
		want            bool
	}{{"legacy", "bypass_openai_responses", false}, {"enabled", "bypass_openai_responses", true}, {"observe", "bypass_openai_responses", true}, {"bad", "bypass_openai_responses", false}, {"enabled", "bypass_openai_chat", false}} {
		if NativeResponsesConfigured([]config.ModelEntry{{ModelInfo: map[string]any{"native_responses_mode": tc.mode, "transport": tc.transport}}}) != tc.want {
			t.Fatalf("目录灰度检测错误: %+v", tc)
		}
	}
}
