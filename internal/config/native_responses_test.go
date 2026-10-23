package config

import "testing"

// TestNativeResponsesAllowed 验证总开关与可信用户名单同时命中才开启，非法/缺失均关闭；无外部资源。
func TestNativeResponsesAllowed(t *testing.T) {
	for _, tc := range []struct {
		policy any
		user   string
		want   bool
	}{
		{nil, "u", false},
		{map[string]any{"enabled": true, "all_users": true}, "u", true},
		{map[string]any{"enabled": false, "all_users": true}, "u", false},
		{map[string]any{"enabled": true, "all_users": "true"}, "u", false},
		{map[string]any{"enabled": true, "all_users": true}, "", false}, {map[string]any{"enabled": true}, "u", false},
		{map[string]any{"enabled": "true", "user_ids": []any{"u"}}, "u", false},
		{map[string]any{"enabled": false, "user_ids": []any{"u"}}, "u", false},
		{map[string]any{"enabled": true, "user_ids": []any{"u"}}, "u", true},
		{map[string]any{"enabled": true, "user_ids": []string{"u"}}, "u", true},
		{map[string]any{"enabled": true, "user_ids": []any{7, "u"}}, "other", false},
		{map[string]any{"enabled": true, "user_ids": []any{""}}, "", false},
	} {
		if got := (Config{GeneralRaw: map[string]any{"native_responses": tc.policy}}).NativeResponsesAllowed(tc.user); got != tc.want {
			t.Fatalf("灰度开关错误: %+v got=%v", tc, got)
		}
	}
}
