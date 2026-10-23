package gateway

import "github.com/sunqirui1987/xhub/internal/config"

// NativeResponsesAllowed 读取本次请求的灰度快照，数据库配置优先于启动配置。
// 参数 userID 为认证身份；返回是否允许候选级灰度。执行器调用，读取失败关闭，不改共享配置或在途请求。
func (s *Server) NativeResponsesAllowed(userID string) bool {
	if s.Cfg == nil || s.Store == nil {
		return false
	}
	policy := s.Cfg.GeneralRaw["native_responses"]
	stored, exists, err := s.Store.FreshConfigValue("general_settings", "native_responses")
	if err != nil {
		return false
	}
	if exists {
		policy = stored
	}
	cfg := config.Config{GeneralRaw: map[string]any{"native_responses": policy}}
	return cfg.NativeResponsesAllowed(userID)
}
