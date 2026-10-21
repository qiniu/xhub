package affinity

import (
	"github.com/sunqirui1987/xhub/internal/auth"
	_ "github.com/sunqirui1987/xhub/internal/provider/anthropic"
	_ "github.com/sunqirui1987/xhub/internal/provider/openai"
	"net/http/httptest"
	"testing"
)

// TestBypassFullHistory 验证三种原生透传完整历史无需本地归属，仍拒绝跨身份父响应和网关引用。
// 参数t为上下文；内存状态覆盖改名、缺失归属、原始签名及保留引用；无外部清理。
func TestBypassFullHistory(t *testing.T) {
	for _, path := range []string{"/bypass/anthropic/v1/messages", "/bypass/openai/v1/responses", "/bypass/openai/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			s := &State{}
			r := httptest.NewRequest("POST", path, nil)
			r.Header.Set("X-Session-Id", "same-session")
			p := &auth.Principal{UserID: "alice"}
			body := map[string]any{"input": []any{map[string]any{"type": "reasoning", "encrypted_content": "vendor-cipher"}}, "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "Plan", "signature": "vendor-signature"}}}}}
			for _, alias := range []string{"original", "renamed"} {
				got := s.PlanRoute(r, alias, body, p)
				if got.Err != nil || got.Required {
					t.Fatalf("原生完整历史误拒绝: %+v", got)
				}
			}
			body["previous_response_id"] = "another-users-response"
			if s.PlanRoute(r, "renamed", body, p).Err == nil {
				t.Fatal("未知父响应被接受")
			}
			delete(body, "previous_response_id")
			body["input"] = []any{map[string]any{"type": "reasoning", "encrypted_content": "xhub_messages_v1:unknown"}}
			if s.PlanRoute(r, "renamed", body, p).Err == nil {
				t.Fatal("网关引用被当作供应商密文")
			}
		})
	}
}

// TestGatewayHistoryReferenceBoundaries 验证保留标记仅在协议历史字段中识别；工具参数和用户文字不误判。
// 参数t为上下文，正常/边界/封装输入均为内存值，无副作用与清理。
func TestGatewayHistoryReferenceBoundaries(t *testing.T) {
	if hasGatewayHistoryReference(nil) {
		t.Fatal("空历史误判")
	}
	if hasGatewayHistoryReference(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "xhub_messages_v1:hello"}}}) {
		t.Fatal("用户文本误判")
	}
	for _, field := range []string{"signature", "data"} {
		b := map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", field: "xhub_responses_reasoning:cipher"}}}}}
		if !hasGatewayHistoryReference(b) {
			t.Fatal("桥接签名未识别")
		}
	}
}
