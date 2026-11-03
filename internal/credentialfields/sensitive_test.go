package credentialfields

import "testing"

// TestSensitive 验证共享识别规则的大小写、子串和非秘密边界；纯函数测试，无持久化和清理。
func TestSensitive(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{"Authorization", true}, {"access_TOKEN", true}, {"api_key", true}, {"client_secret", true}, {"password", true}, {"passwd", true}, {"credential_name", true}, {"monkey", true}, {"", false}, {"api_base", false}, {"timeout", false},
	} {
		if got := Sensitive(tc.key); got != tc.want {
			t.Errorf("字段 %q 识别=%v want=%v", tc.key, got, tc.want)
		}
	}
}
