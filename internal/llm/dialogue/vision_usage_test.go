package dialogue

import "testing"

// TestVisionUsageNormalization验证图片细分在别名转换、缓存补充和部分帧更新中保留，矛盾不能由后续好帧清除；纯内存fixture无需外部清理。
func TestVisionUsageNormalization(t *testing.T) {
	t.Run("partial_and_cache", func(t *testing.T) {
		raw := map[string]any{"input_tokens": 80, "output_tokens": 5, "cache_read_input_tokens": 10, "input_tokens_details": map[string]any{"image_tokens": 60}, "prompt_tokens_details": map[string]any{"text_tokens": 20}}
		got := NormalizeDialogueUsage(raw)
		d := dialogueMap(got["prompt_tokens_details"])
		if d["image_tokens"] != 60 || d["text_tokens"] != 20 || d["cached_tokens"] != 10 {
			t.Fatalf("转换丢失输入明细: %+v", got)
		}
		if dialogueMap(raw["input_tokens_details"])["cached_tokens"] != nil {
			t.Fatal("规范化修改原输入")
		}
	})
	t.Run("malformed_details_sticky", func(t *testing.T) {
		raw := map[string]any{"input_tokens": 80, "output_tokens": 5, "input_tokens_details": "invalid", "cache_read_input_tokens": 10}
		if got := NormalizeDialogueUsage(raw); got["pricing_blocked"] != "upstream_usage_invalid" {
			t.Fatalf("缓存补充掩盖了明细格式错误: %+v", got)
		}
	})

	t.Run("null_alias_does_not_erase", func(t *testing.T) {
		raw := map[string]any{"input_tokens": 80, "output_tokens": 5, "input_tokens_details": nil, "prompt_tokens_details": map[string]any{"image_tokens": 60}}
		if got := NormalizeDialogueUsage(raw); dialogueMap(got["prompt_tokens_details"])["image_tokens"] != 60 {
			t.Fatalf("null别名抹掉图片计量: %+v", got)
		}
	})
	t.Run("partial_frame_preserves_image", func(t *testing.T) {
		first := map[string]any{"input_tokens": 80, "output_tokens": 1, "input_tokens_details": map[string]any{"image_tokens": 60, "text_tokens": 20}}
		next := MergeDialogueUsage(first, map[string]any{"output_tokens": 5, "input_tokens_details": map[string]any{"cached_tokens": 10}})
		got := NormalizeDialogueUsage(next)
		d := dialogueMap(got["prompt_tokens_details"])
		if d["image_tokens"] != 60 || d["text_tokens"] != 20 || d["cached_tokens"] != 10 {
			t.Fatalf("局部帧更新抹掉计量: %+v", got)
		}
	})

	for _, field := range []string{"image_tokens", "text_tokens"} {
		t.Run(field, func(t *testing.T) {
			bad := map[string]any{"input_tokens": 80, "output_tokens": 5, "input_tokens_details": map[string]any{field: 30}, "prompt_tokens_details": map[string]any{field: 20}}
			got := NormalizeDialogueUsage(bad)
			if got["pricing_blocked"] != "upstream_usage_conflict" {
				t.Fatalf("别名矛盾未阻断: %+v", got)
			}
			next := MergeDialogueUsage(got, map[string]any{"input_tokens": 80, "output_tokens": 5, "input_tokens_details": map[string]any{field: 30}})
			if next["pricing_blocked"] != "upstream_usage_conflict" {
				t.Fatal("后续帧清除了矛盾证据")
			}
		})
	}
	t.Run("valid_snapshot_replaces_alias", func(t *testing.T) {
		first := map[string]any{"input_tokens": 80, "output_tokens": 1, "input_tokens_details": map[string]any{"image_tokens": 30}}
		next := MergeDialogueUsage(first, map[string]any{"prompt_tokens_details": map[string]any{"image_tokens": 60}})
		if DialogueUsageProblem(next) != "" || dialogueMap(next["input_tokens_details"])["image_tokens"] != nil {
			t.Fatalf("累计更新应替换旧别名: %+v", next)
		}
	})
}

// TestValidateBudgetInputImage验证预算复用协议图片校验且拒绝文件引用/坏MIME/坏Base64；不下载图片，全部数据内存回收。
func TestValidateBudgetInputImage(t *testing.T) {
	for _, tc := range []struct {
		name, protocol string
		block          map[string]any
		ok             bool
	}{
		{"messages", "anthropic-messages", map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AQ=="}, "cache_control": map[string]any{"type": "ephemeral"}}, true},
		{"url", "openai-responses", map[string]any{"type": "input_image", "image_url": "https://example.invalid/a.png"}, true},
		{"file_id", "openai-responses", map[string]any{"type": "input_image", "file_id": "file-private"}, false},
		{"bad_mime", "anthropic-messages", map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": "AQ=="}}, false},
		{"bad_base64", "openai-chat", map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,!"}}, false},
		{"wrong_protocol", "openai-chat", map[string]any{"type": "input_image", "image_url": "https://example.invalid/a.png"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBudgetInputImage(tc.block, tc.protocol)
			if (err == nil) != tc.ok {
				t.Fatalf("图片预算格式边界错误: %v", err)
			}
			if tc.name == "messages" && tc.block["cache_control"] == nil {
				t.Fatal("移除了原缓存提示")
			}
		})
	}
}
