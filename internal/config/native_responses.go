package config

// NativeResponsesAllowed 判断可信用户是否命中默认关闭的原生 Responses 灰度。
// 参数 userID 为认证结果；返回开关与名单同时命中。执行器调用，非法配置/空名单均关闭，不使用客户端字段或环境变量。
func (c Config) NativeResponsesAllowed(userID string) bool {
	policy, _ := c.GeneralRaw["native_responses"].(map[string]any)
	enabled, _ := policy["enabled"].(bool)
	if !enabled || userID == "" {
		return false
	}
	switch ids := policy["user_ids"].(type) {
	case []string:
		for _, id := range ids {
			if id == userID {
				return true
			}
		}
	case []any:
		for _, id := range ids {
			if text, ok := id.(string); ok && text == userID {
				return true
			}
		}
	}
	return false
}
