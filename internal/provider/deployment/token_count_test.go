package deployment_test

import (
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/provider/deployment"
	"github.com/sunqirui1987/xhub/internal/provider/registry"
	"testing"
)

// TestTokenCountEndpointBoundary 验证标准计数沿用Messages权限、透传显式授权及跨协议拒绝。
// 参数t为测试上下文，返回无；前置只读注册表和内存部署，不访问上游或产生需清理数据。
func TestTokenCountEndpointBoundary(t *testing.T) {
	hit, _ := registry.Match("POST", "/v1/messages/count_tokens")
	hit.Transport.EndpointID = "messages"
	m := config.ModelEntry{ModelName: "count", LiteLLMParams: map[string]any{"model": "claude", "custom_llm_provider": "anthropic"}, ModelInfo: map[string]any{"transport": "bypass_anthropic_messages", "endpoint_types": []string{"messages"}}}
	if resolved, err := deployment.ResolveHit(hit, m); err != nil || resolved.Action.UpstreamPath != "/v1/messages/count_tokens" {
		t.Fatalf("标准计数不可用: %+v %v", resolved, err)
	}
	bypass, _ := registry.Match("POST", "/bypass/anthropic/v1/messages/count_tokens")
	if _, err := deployment.ResolveHit(bypass, m); err == nil {
		t.Fatal("标准权限隐式开启透传")
	}
	m.ModelInfo["endpoint_types"] = []string{"messages", "bypass:anthropic-messages"}
	if _, err := deployment.ResolveHit(bypass, m); err != nil {
		t.Fatalf("显式透传不可用: %v", err)
	}
	m.ModelInfo["transport"] = "bypass_openai_chat"
	m.LiteLLMParams["custom_llm_provider"] = "openai"
	if _, err := deployment.ResolveHit(hit, m); err == nil {
		t.Fatal("计数被错误转换为其他协议")
	}
}
