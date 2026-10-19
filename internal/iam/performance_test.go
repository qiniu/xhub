package iam

import "testing"

// TestOutputTPS 验证统一输出速度公式；前置固定计时，覆盖正常流、长输入、同步、缓存及非法计时，无外部清理。
func TestOutputTPS(t *testing.T) {
	ttft := 100
	base := UsageRecord{CallType: "responses", Status: "success", HTTPStatus: 200, Streamed: true, PromptTokens: 10000, CompletionTokens: 10, DurationMS: 1100, TTFTMs: &ttft}
	for _, tc := range []struct {
		name   string
		change func(*UsageRecord)
		valid  bool
	}{
		{"stream", func(r *UsageRecord) {}, true},
		{"large input does not inflate output TPS", func(r *UsageRecord) { r.PromptTokens = 1000000 }, true},
		{"completed", func(r *UsageRecord) { r.Status = "completed" }, true},
		{"empty legacy status", func(r *UsageRecord) { r.Status = "" }, true},
		{"synchronous", func(r *UsageRecord) { r.Streamed = false }, false},
		{"cached", func(r *UsageRecord) { r.CacheHit = true }, false},
		{"failed", func(r *UsageRecord) { r.Status = "failed" }, false},
		{"http error", func(r *UsageRecord) { r.HTTPStatus = 502 }, false},
		{"non text", func(r *UsageRecord) { r.CallType = "image" }, false},
		{"missing timing", func(r *UsageRecord) { r.TTFTMs = nil }, false},
		{"negative timing", func(r *UsageRecord) { n := -1; r.TTFTMs = &n }, false},
		{"zero interval", func(r *UsageRecord) { r.DurationMS = 100 }, false},
		{"inverted timing", func(r *UsageRecord) { r.DurationMS = 99 }, false},
		{"no output", func(r *UsageRecord) { r.CompletionTokens = 0 }, false},
		{"negative output", func(r *UsageRecord) { r.CompletionTokens = -1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.change(&r)
			got := OutputTPS(r)
			if (got != nil) != tc.valid || got != nil && *got != 10 {
				t.Fatalf("输出 TPS 错误: got=%v valid=%v record=%+v", got, tc.valid, r)
			}
		})
	}
	for _, call := range textTimingCalls {
		base.CallType = call
		if got := OutputTPS(base); got == nil || *got != 10 {
			t.Fatalf("文本操作 %q 丢失 TPS: %v", call, got)
		}
	}
}
