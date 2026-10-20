package dialogue

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/catalog"
)

// TestDialogueCacheUsageAliases 验证缓存读取/写入同帧别名必须一致，包括显式零、嵌套字段和非法计数。
// 前置纯内存用量对象；同步归一化必须保留异常，流后续合法帧不得清除，输入对象不变，无外部清理。
func TestDialogueCacheUsageAliases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extra   map[string]any
		blocked bool
	}{
		{"write_conflict", map[string]any{"cache_creation_input_tokens": 0, "cache_write_tokens": 50}, true},
		{"read_top_conflict", map[string]any{"cache_read_input_tokens": 0, "cache_read_tokens": 8}, true},
		{"read_nested_conflict", map[string]any{"cached_tokens": 0, "prompt_tokens_details": map[string]any{"cached_tokens": 8}}, true},
		{"read_both_details_conflict", map[string]any{"input_tokens_details": map[string]any{"cached_tokens": 0}, "prompt_tokens_details": map[string]any{"cached_tokens": 8}}, true},
		{"read_separate_nested_conflict", map[string]any{"cache_read_input_tokens": 0, "input_tokens_details": map[string]any{"cached_tokens": 8}}, true},
		{"write_equal_zero", map[string]any{"cache_creation_input_tokens": 0, "cache_write_tokens": 0}, false},
		{"write_equal", map[string]any{"cache_creation_input_tokens": 8, "cache_write_tokens": json.Number("8")}, false},
		{"read_equal", map[string]any{"cached_tokens": 8, "prompt_tokens_details": map[string]any{"cached_tokens": 8}, "input_tokens_details": map[string]any{"cached_tokens": 8}}, false},
		{"null_alias", map[string]any{"cached_tokens": nil, "prompt_tokens_details": map[string]any{"cached_tokens": 8}}, false},
		{"fraction", map[string]any{"cache_write_tokens": 0.5}, true},
		{"string", map[string]any{"cache_read_tokens": "8"}, true},
		{"negative", map[string]any{"cached_tokens": -1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"prompt_tokens": 10, "completion_tokens": 2}
			for key, value := range tc.extra {
				raw[key] = value
			}
			before, _ := json.Marshal(raw)
			sync := NormalizeDialogueUsage(raw)
			merged := MergeDialogueUsage(nil, raw)
			merged = NormalizeDialogueUsage(MergeDialogueUsage(merged, map[string]any{
				"prompt_tokens": 10, "completion_tokens": 2, "cache_write_tokens": 9,
				"cached_tokens": 9, "pricing_blocked": "",
			}))
			for name, got := range map[string]map[string]any{"sync": sync, "stream": merged} {
				blocked, _ := got["pricing_blocked"].(string)
				if (blocked != "") != tc.blocked {
					t.Fatalf("%s 缓存别名信任状态错误: %+v", name, got)
				}
			}
			after, _ := json.Marshal(raw)
			if string(before) != string(after) {
				t.Fatalf("缓存别名检查修改原始帧: before=%s after=%s", before, after)
			}
		})
	}
}

// TestDialogueCacheUsageCumulativeAliases 验证只更新一个别名的合法累计增长不会与旧快照冲突。
// 每组先报告相等别名，再分别选择一个路径增长；归一化后保持可信，且不修改嵌套输入，纯内存无需清理。
func TestDialogueCacheUsageCumulativeAliases(t *testing.T) {
	for _, group := range dialogueUsageAliases[2:] {
		for _, updated := range group {
			t.Run(updated, func(t *testing.T) {
				first := map[string]any{"prompt_tokens": 10, "completion_tokens": 2}
				for _, path := range group {
					setCacheUsageAlias(first, path, 0)
				}
				before, _ := json.Marshal(first)
				state := MergeDialogueUsage(nil, first)
				next := map[string]any{}
				setCacheUsageAlias(next, updated, 8)
				state = MergeDialogueUsage(state, next)
				if got := DialogueUsageProblem(state); got != "" {
					t.Fatalf("合法累计更新误判为冲突: field=%s problem=%s usage=%+v", updated, got, state)
				}
				if got := dialogueUsageAlias(state, updated); got != 8 {
					t.Fatalf("累计更新没有保留: field=%s value=%v", updated, got)
				}
				normalized := NormalizeDialogueUsage(state)
				if normalized["pricing_blocked"] != nil {
					t.Fatalf("归一化重新引入旧别名冲突: %+v", normalized)
				}
				after, _ := json.Marshal(first)
				if string(before) != string(after) {
					t.Fatalf("清理旧别名修改前帧: before=%s after=%s", before, after)
				}
			})
		}
	}
}

// TestDialogueCacheUsagePreservesOtherDetails 验证清除嵌套旧别名只删除目标计数，保留其它明细且不别名写入原图。
// 前置同帧相等缓存计量，后帧只有顶层增长；返回可信新快照，纯内存无需清理。
func TestDialogueCacheUsagePreservesOtherDetails(t *testing.T) {
	details := map[string]any{"cached_tokens": 1, "text_tokens": 10}
	first := map[string]any{"prompt_tokens_details": details, "cached_tokens": 1}
	state := MergeDialogueUsage(nil, first)
	state = MergeDialogueUsage(state, map[string]any{"cached_tokens": 2})
	if !reflect.DeepEqual(state["prompt_tokens_details"], map[string]any{"text_tokens": 10}) ||
		!reflect.DeepEqual(details, map[string]any{"cached_tokens": 1, "text_tokens": 10}) {
		t.Fatalf("清除缓存旧别名破坏其它计量或原帧: state=%+v original=%+v", state, first)
	}
}

// TestDialogueCacheUsageMessagesProjection 验证 Messages 输出转换保持输入总量、缓存子集和独立缓存计数的含义。
// 前置合法/零值/缺失/非法缓存明细；有效用量再次归一化后计费事实不变，非法值不生成负输入，纯内存无需清理。
func TestDialogueCacheUsageMessagesProjection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       map[string]any
		wantInput any
		valid     bool
	}{
		{"subset", map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "prompt_tokens_details": map[string]any{"cached_tokens": 8}}, float64(2), true},
		{"zero", map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "prompt_tokens_details": map[string]any{"cached_tokens": 0}}, float64(10), true},
		{"separate", map[string]any{"input_tokens": 10, "output_tokens": 1, "cache_read_input_tokens": 8}, 10, true},
		{"separate_alias", map[string]any{"input_tokens": 10, "output_tokens": 1, "cache_read_tokens": 8, "prompt_tokens_details": map[string]any{"cached_tokens": 8}}, 10, true},
		{"missing", map[string]any{"prompt_tokens": 10, "completion_tokens": 1}, 10, true},
		{"excess", map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "prompt_tokens_details": map[string]any{"cached_tokens": 11}}, 10, false},
		{"invalid", map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "prompt_tokens_details": map[string]any{"cached_tokens": "8"}}, 10, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := NormalizeDialogueUsage(tc.raw)
			out := DialogueOutputUsage(u, "anthropic-messages")
			if out["input_tokens"] != tc.wantInput {
				t.Fatalf("Messages 输入计量错误: got=%+v want=%v", out, tc.wantInput)
			}
			if tc.valid && catalog.NormalizeUsage(u) != catalog.NormalizeUsage(NormalizeDialogueUsage(out)) {
				t.Fatalf("Messages 转换改变计费事实: before=%+v after=%+v", catalog.NormalizeUsage(u), catalog.NormalizeUsage(NormalizeDialogueUsage(out)))
			}
		})
	}
}

// TestDialogueGoogleCacheUsage 验证 Google 缓存数量是输入子集，零值正常，非法值阻断计费，缺失缓存不补造。
// 仅解析本地 JSON；断言 Gemini/Vertex 返回的计费输入始终为10而不是10加缓存，无服务或数据清理。
func TestDialogueGoogleCacheUsage(t *testing.T) {
	for _, protocol := range []string{"gemini", "vertex"} {
		for _, tc := range []struct {
			name    string
			cache   any
			cached  int
			blocked bool
		}{{"hit", 8, 8, false}, {"zero", 0, 0, false}, {"absent", nil, 0, false}, {"invalid", "8", 0, true}, {"fraction", 0.5, 0, true}} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				usage := map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 1}
				if tc.cache != nil {
					usage["cachedContentTokenCount"] = tc.cache
				}
				raw, _ := json.Marshal(map[string]any{"responseId": "google-cache", "candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": "Cache result."}}}, "finishReason": "STOP"}}, "usageMetadata": usage})
				result, err := ParseDialogueResult(protocol, raw)
				if err != nil {
					if tc.blocked {
						return // 官方 SDK 在投影前拒绝非法计量，同样不能进入结算。
					}
					t.Fatal(err)
				}
				counts := catalog.NormalizeUsage(result.Usage)
				if counts.PromptTokens != 10 || counts.CompletionTokens != 1 || counts.CachedTokens != tc.cached || (counts.PricingBlocked != "") != tc.blocked {
					t.Fatalf("Google 缓存子集被重复计数或非法值未阻断: usage=%+v facts=%+v", result.Usage, counts)
				}
			})
		}
	}
}

// setCacheUsageAlias构建平级或嵌套缓存/模态计数；参数为私有对象、已知路径和值，无返回，无外部副作用。
func setCacheUsageAlias(usage map[string]any, path string, value int) {
	parent, child, nested := strings.Cut(path, ".")
	if nested {
		if usage[parent] == nil {
			usage[parent] = map[string]any{}
		}
		usage[parent].(map[string]any)[child] = value
		return
	}
	usage[path] = value
}
