package main

import (
	"strings"
	"testing"
)

// TestExpectedOperationalFailure 验证 sweep 只放行未配置价格源的精确失败，其他方法、状态和 502 仍失败；无外部数据且无需清理。
func TestExpectedOperationalFailure(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		code   int
		body   string
		want   bool
	}{
		{name: "明确未配置价格源", method: "POST", path: "/reload/model_cost_map", code: 502, body: "price feed URL must be explicitly configured", want: true},
		{name: "其他服务端错误", method: "POST", path: "/reload/model_cost_map", code: 502, body: "dial failed"},
		{name: "错误方法", method: "GET", path: "/reload/model_cost_map", code: 502, body: "price feed URL must be explicitly configured"},
		{name: "错误状态", method: "POST", path: "/reload/model_cost_map", code: 500, body: "price feed URL must be explicitly configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expectedOperationalFailure(tt.method, tt.path, tt.code, tt.body); got != tt.want {
				t.Fatalf("expectedOperationalFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTypedUnsupported 验证 sweep 只对两个未记录归因维度接受专用 501，且不会把同类型错误扩散到其他路由；纯解析无测试数据。
func TestTypedUnsupported(t *testing.T) {
	dimension := `{"error":{"type":"unsupported_usage_dimension","message":"not recorded"}}`
	generic := `{"error":{"type":"not_implemented","message":"unsupported"}}`
	for _, path := range []string{"/customer/daily/activity", "/tag/daily/activity"} {
		if !typedUnsupported("GET", path, dimension) {
			t.Fatalf("expected dimension contract for %s", path)
		}
	}
	if typedUnsupported("GET", "/team/daily/activity", dimension) {
		t.Fatal("dimension error unexpectedly accepted for another route")
	}
	if !typedUnsupported("POST", "/some/catalog/route", generic) {
		t.Fatal("generic not_implemented contract rejected")
	}
	if typedUnsupported("GET", "/customer/daily/activity", generic) {
		t.Fatal("customer dimension accepted the wrong error type")
	}
}

// TestOperationalUnsupported 验证精确运维契约被扫描器识别；无网络前置，其他路径、方法和缺失消息仍必须失败，无清理副作用。
func TestOperationalUnsupported(t *testing.T) {
	body := `{"error":{"type":"unsupported_operation","message":"unavailable"}}`
	for _, route := range []string{"GET /coordination_redis/settings", "POST /coordination_redis/settings", "POST /coordination_redis/settings/test", "GET /debug/asyncio-tasks", "GET /debug/memory/details", "GET /debug/memory/summary", "POST /debug/memory/gc/configure", "GET /memory-usage", "GET /memory-usage-in-mem-cache", "GET /memory-usage-in-mem-cache-items", "GET /health/backlog", "GET /health/drain", "GET /health/history", "GET /health/license", "GET /health/shared-status", "GET /health/services"} {
		parts := strings.SplitN(route, " ", 2)
		if !typedUnsupported(parts[0], parts[1], body) {
			t.Errorf("明确拒绝契约未识别: %s", route)
		}
	}
	for _, route := range []string{"DELETE /health/drain", "GET /health", "GET /health/readiness", "POST /health/services", "GET /health/drain/other"} {
		parts := strings.SplitN(route, " ", 2)
		if typedUnsupported(parts[0], parts[1], body) {
			t.Errorf("未声明的失败被吞掉: %s", route)
		}
	}
	for _, invalid := range []string{`{"error":{"type":"unsupported_operation"}}`, `{"error":{"type":"unsupported_operation","message":""}}`, `{"error":{"type":"unsupported_operation","message":123}}`, `{`} {
		if typedUnsupported("GET", "/health/services", invalid) {
			t.Errorf("损坏错误响应被接受: %s", invalid)
		}
	}
}
