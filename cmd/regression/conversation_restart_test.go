package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunqirui1987/xhub/internal/gateway/affinity"
)

// TestClaudeNativeConversationRestart 验证原生Claude带签名及隐藏推理的同会话在网关状态重建后完成工具续轮。
// 前置私有PG、真实HTTP和独立Key；覆盖同步/流式及无权限Key拒绝，harness清理全部资源。
func TestClaudeNativeConversationRestart(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if n > 1 && !strings.Contains(string(mustJSON(body)), "PRIVATE_SIGNATURE_") {
					t.Error("续轮丢失原签名")
				}
				content := signedMessagesContent(1)
				stop := "tool_use"
				if n > 1 {
					content = []any{map[string]any{"type": "text", "text": "RESTORED"}}
					stop = "end_turn"
				}
				writeSignedMessagesFixture(w, map[string]any{"id": fmt.Sprintf("msg_restart_%d", n), "type": "message", "role": "assistant", "content": content, "stop_reason": stop, "usage": map[string]any{"input_tokens": 8, "output_tokens": 4}}, stream)
			}))
			t.Cleanup(up.Close)
			dep := hardBudgetDeployment("claude-restart")
			dep.LiteLLMParams["api_base"] = up.URL
			dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
			dep.ModelInfo["transport"] = "bypass_anthropic_messages"
			dep.ModelInfo["endpoint_types"] = []string{"messages"}
			dep.ModelInfo["id"] = "restart-native"
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, dep.ModelName)
			headers := map[string]string{"X-Claude-Code-Session-Id": "unchanged-conversation"}
			body := map[string]any{"model": dep.ModelName, "max_tokens": 64, "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "Read a value."}}}
			first := h.doHeaders("POST", "/v1/messages", owner.key, body, headers)
			if first.status != 200 {
				t.Fatal(first.describe())
			}
			content := encryptedMessagesContent(t, first, stream)
			body["messages"] = []any{map[string]any{"role": "user", "content": "Read a value."}, map[string]any{"role": "assistant", "content": content}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "VALUE_1"}}}}
			h.gw.State = *affinity.NewDurable(nil, h.store)
			second := h.doHeaders("POST", "/v1/messages", owner.key, body, headers)
			if second.status != 200 || !strings.Contains(second.text(), "RESTORED") {
				t.Fatal(second.describe())
			}
			other := h.openScope(t, admin, "stranger")
			h.ok("POST", "/key/update", admin, map[string]any{"key": other.key, "models": []string{"another-model"}})
			denied := h.doHeaders("POST", "/v1/messages", other.key, body, headers)
			if denied.status < 400 || calls.Load() != 2 {
				t.Fatalf("跨身份读取或额外外发: %s calls=%d", denied.describe(), calls.Load())
			}
		})
	}
}
