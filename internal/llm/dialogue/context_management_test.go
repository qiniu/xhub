package dialogue

import (
	"encoding/json"
	"testing"
)

// TestKeepAllContextManagement 覆盖无操作上下文配置、空数组及错误结构；内存 JSON 不变，无外部资源需要清理。
func TestKeepAllContextManagement(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"keep_all", `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`, true},
		{"empty_edits", `{"edits":[]}`, true},
		{"repeated_noop", `{"edits":[{"type":"clear_thinking_20251015","keep":"all"},{"type":"clear_thinking_20251015","keep":"all"}]}`, true},
		{"null", `null`, false}, {"scalar", `"bad"`, false}, {"missing", `{}`, false},
		{"null_edits", `{"edits":null}`, false}, {"scalar_edits", `{"edits":"bad"}`, false},
		{"null_edit", `{"edits":[null]}`, false},
		{"clear", `{"edits":[{"type":"clear_thinking_20251015","keep":"none"}]}`, false},
		{"unknown_edit", `{"edits":[{"type":"compact_20260112","keep":"all"}]}`, false},
		{"extra", `{"edits":[],"secret_option":true}`, false},
		{"edit_extra", `{"edits":[{"type":"clear_thinking_20251015","keep":"all","trigger":1}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(value)
			if err := ValidateKeepAllContextManagement(value); (err == nil) != tc.valid {
				t.Fatalf("无操作上下文校验错误: valid=%v err=%v", tc.valid, err)
			}
			after, _ := json.Marshal(value)
			if string(before) != string(after) {
				t.Fatal("上下文校验修改了用户正文")
			}
		})
	}
}
