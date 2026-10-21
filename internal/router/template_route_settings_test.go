package router

import "testing"

// templateDocument 创建一份完整的新模板文档；模型规则为空时只配置执行参数。
func templateDocument(attempts int) map[string]any {
	return map[string]any{
		"model_routes": []any{},
		"retry_policy": map[string]any{"max_attempts": attempts, "timeout_seconds": 60, "failure_threshold": 3, "cooldown_seconds": 0},
	}
}

// TestTypedExecutionDefaultsAndBoundaries 验证公共执行参数的默认值、零值和错误类型处理。
// 这些读取器不得让请求变成零次尝试或零秒超时；纯内存测试无需清理。
func TestTypedExecutionDefaultsAndBoundaries(t *testing.T) {
	if BuiltinSettings().TimeoutSeconds() != 600 {
		t.Fatal("内置请求应允许600秒")
	}
	empty := RouteSettings{Settings: map[string]any{}}
	if empty.Retries() != 1 || empty.TimeoutSeconds() != 600 || empty.AllowedFails() != 3 || empty.Strategy() != "traffic-split" {
		t.Fatalf("缺省执行参数错误: %+v", empty)
	}
	zero := RouteSettings{Settings: map[string]any{"retry_policy": map[string]any{"max_attempts": 0.0, "timeout_seconds": 60, "failure_threshold": 0.0, "cooldown_seconds": 0}}}
	if zero.Retries() != 1 || zero.AllowedFails() != 0 {
		t.Fatalf("零值边界错误: %+v", zero)
	}
	quoted := RouteSettings{Settings: map[string]any{"retry_policy": map[string]any{"max_attempts": 1, "timeout_seconds": "30", "failure_threshold": 3, "cooldown_seconds": 0}}}
	if quoted.TimeoutSeconds() != 600 {
		t.Fatalf("字符串数字被错误接受: %v", quoted.TimeoutSeconds())
	}
	fromYAML := RouteSettings{Settings: templateDocument(5)}
	fromYAML.Settings["retry_policy"].(map[string]any)["timeout_seconds"] = 90
	if fromYAML.Retries() != 5 || fromYAML.TimeoutSeconds() != 90 {
		t.Fatalf("整数读取错误: %+v", fromYAML)
	}
}
