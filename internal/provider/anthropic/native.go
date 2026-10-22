// Package anthropic 登记 Anthropic Messages 的原生执行协议。
package anthropic

import (
	"github.com/sunqirui1987/xhub/internal/provider/contract"
	"github.com/sunqirui1987/xhub/internal/provider/measurement"
	"github.com/sunqirui1987/xhub/internal/provider/registry"
)

// init 登记 Messages 固定路径、版本头和 API key 鉴权，供启动时装配。
// 无参数或返回值；只写入内置注册表，不创建 HTTP 客户端或发起外部调用。
func init() {
	registry.RegisterTransport(contract.Transport{
		ID: "bypass_anthropic_messages", Label: "bypass:anthropic-messages", Kind: contract.KindBypass,
		EndpointID: "bypass:anthropic-messages", Protocol: "anthropic-messages", Family: "chat", ModelField: "model",
		Auth: contract.AuthConfig{Header: "x-api-key"}, Headers: map[string]string{"anthropic-version": "2023-06-01"},
		ResponseUsage: measurement.NativeUsage("anthropic-messages"),
		Actions: []contract.Action{
			{Name: "create", Method: "POST", PublicPath: "/bypass/anthropic/v1/messages", UpstreamPath: "/v1/messages"},
			{Name: "count_tokens", Method: "POST", PublicPath: "/bypass/anthropic/v1/messages/count_tokens", UpstreamPath: "/v1/messages/count_tokens"},
			{Name: "count_tokens", Method: "POST", PublicPath: "/v1/messages/count_tokens", UpstreamPath: "/v1/messages/count_tokens"},
		},
	})
}
