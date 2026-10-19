package iam

import "slices"

// textTimingCalls 列出可观测文本响应时序的操作；监控、日志和历史指标升级共用，防止三处能力口径漂移。
var textTimingCalls = []string{
	"chat", "chat.completions", "responses", "messages", "completions", "bypass_openai_chat:create", "bypass_openai_responses:create",
	"bypass_anthropic_messages:create", "openai_completions:create", "anthropic_messages", "gemini", "generateContent", "streamGenerateContent",
	"bypass_google_genai:create", "gemini:create", "bypass_gemini:create", "google_genai:create", "gemini_generate_content:create", "vertex_generate_content:create", "vertex",
}

// textTimingSample 判断 r 是否具有有效的成功非缓存文本时序；监控与 OutputTPS 调用，无持久化副作用。
// 同步首字节仍可作为响应延迟记录，但不能证明输出生成区间；是否允许计算 TPS 由 OutputTPS 再判断。
func textTimingSample(r UsageRecord) bool {
	return (status(r) == "success" || status(r) == "completed") && r.HTTPStatus < 400 && !r.CacheHit &&
		r.TTFTMs != nil && *r.TTFTMs >= 0 && r.DurationMS >= *r.TTFTMs && slices.Contains(textTimingCalls, r.CallType)
}

// OutputTPS 计算有效流式文本请求的输出 Token/秒；r 为已保存或即将保存的账单事实，返回可空速度。
// 监控聚合与日志列表/详情共用；输入 Token 不参与生成速度，成功同步响应也不能用读尾时间推测生成耗时。
// 缺失或倒置计时、零输出、失败、缓存及非文本操作返回 nil；不修改账单、计费或原始计时。
func OutputTPS(r UsageRecord) *float64 {
	if !r.Streamed || !textTimingSample(r) || r.CompletionTokens <= 0 || r.DurationMS <= *r.TTFTMs {
		return nil
	}
	value := float64(r.CompletionTokens) * 1000 / float64(r.DurationMS-*r.TTFTMs)
	return &value
}
