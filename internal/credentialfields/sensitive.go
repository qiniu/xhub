// Package credentialfields 共享管理面凭据字段识别规则，不处理存储或协议。
package credentialfields

import "strings"

// Sensitive 判断 key 是否包含既有秘密字段片段，供供应商和搜索配置脱敏使用。
// 返回是否敏感；大小写不敏感，空字符串返回 false，不改变调用方字段。
func Sensitive(key string) bool {
	key = strings.ToLower(key)
	for _, part := range []string{"authorization", "token", "key", "secret", "password", "passwd", "credential"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}
