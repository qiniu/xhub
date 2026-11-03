package deployment_test

import (
	"fmt"
	"testing"

	"github.com/sunqirui1987/xhub/internal/config"
	deployment "github.com/sunqirui1987/xhub/internal/provider/deployment"
)

// TestDefaultDialogueBypass 验证有效新旧部署无需声明即可开放匹配的三种透传入口。
// 前置本地注册目录；覆盖缺项、空值、旧标准声明及显式声明，错误协议、停用、无效配置和其他透传仍拒绝；无外部资源或清理。
func TestDefaultDialogueBypass(t *testing.T) {
	protocols := []string{"openai-chat", "openai-responses", "anthropic-messages"}
	transports := []string{"bypass_openai_chat", "bypass_openai_responses", "bypass_anthropic_messages"}
	for i, protocol := range protocols {
		for j, declared := range []any{nil, []string{}, []string{"chat", "responses", "messages"}, []string{"bypass:" + protocol}} {
			t.Run(fmt.Sprintf("%s/config-%d", protocol, j), func(t *testing.T) {
				m := config.ModelEntry{ModelName: "public", LiteLLMParams: map[string]any{"model": "real", "custom_llm_provider": "custom"}, ModelInfo: map[string]any{"transport": transports[i]}}
				if declared != nil {
					m.ModelInfo["endpoint_types"] = declared
				}
				for _, target := range protocols {
					err := deployment.AllowsEndpoint(m, "bypass:"+target)
					if (err == nil) != (target == protocol) {
						t.Fatalf("上游 %s 对 %s 的默认开放错误: %v", protocol, target, err)
					}
				}
				endpoints := deployment.DeploymentEndpoints(m)
				found := 0
				for _, e := range endpoints {
					if e.EndpointID == "bypass:"+protocol {
						found++
					}
				}
				if found != 1 || len(endpoints) != 8 {
					t.Fatalf("发现接口与选路不一致: %+v", endpoints)
				}
				m.ModelInfo["disabled"] = true
				if deployment.AllowsEndpoint(m, "bypass:"+protocol) == nil {
					t.Fatal("停用仍开放")
				}
				delete(m.ModelInfo, "disabled")
				m.ModelInfo["transport"] = "unknown"
				if deployment.AllowsEndpoint(m, "bypass:"+protocol) == nil {
					t.Fatal("无效配置仍开放")
				}
			})
		}
	}
	for _, tc := range []struct{ transport, endpoint string }{{"gemini_generate_content", "bypass:gemini"}, {"vertex_generate_content", "bypass:vertex"}, {"bypass_openai_image_generation", "bypass:openai-images"}} {
		t.Run(tc.endpoint, func(t *testing.T) {
			m := config.ModelEntry{ModelName: "public", LiteLLMParams: map[string]any{"model": "real", "custom_llm_provider": "custom"}, ModelInfo: map[string]any{"transport": tc.transport}}
			if deployment.AllowsEndpoint(m, tc.endpoint) == nil {
				t.Fatal("其他透传不应默认开放")
			}
			m.ModelInfo["endpoint_types"] = []string{tc.endpoint}
			if err := deployment.AllowsEndpoint(m, tc.endpoint); err != nil {
				t.Fatalf("其他透传显式声明应仍可用: %v", err)
			}
		})
	}
}
