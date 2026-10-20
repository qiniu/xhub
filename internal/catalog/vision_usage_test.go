package catalog

import "testing"

// TestVisionUsageAliases验证两协议图片细分别名、部分桶合并及冲突保守阻断；只用内存数据，无清理副作用。
func TestVisionUsageAliases(t *testing.T) {
	for _, tc := range []struct {
		name          string
		input, prompt map[string]any
		blocked       bool
	}{
		{"responses", map[string]any{"text_tokens": 20, "image_tokens": 60}, nil, false},
		{"chat", nil, map[string]any{"text_tokens": 20, "image_tokens": 60}, false},
		{"partial", map[string]any{"text_tokens": 20}, map[string]any{"image_tokens": 60}, false},
		{"equal", map[string]any{"text_tokens": 20, "image_tokens": 60}, map[string]any{"text_tokens": 20, "image_tokens": 60}, false},
		{"conflict", map[string]any{"image_tokens": 60}, map[string]any{"image_tokens": 70}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"input_tokens": 80, "input_tokens_details": tc.input, "prompt_tokens_details": tc.prompt}
			u := NormalizeUsage(raw)
			if (u.PricingBlocked != "") != tc.blocked {
				t.Fatalf("图片别名冲突状态错误: %+v", u)
			}
			if !tc.blocked && (u.InputTextTokens != 20 || u.InputImageTokens != 60 || !u.InputTokenDetails) {
				t.Fatalf("图片别名分桶丢失: %+v", u)
			}
		})
	}
}
