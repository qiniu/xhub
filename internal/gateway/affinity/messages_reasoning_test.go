package affinity

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// TestMessagesEnvelopeRequiresOwnedConnection 验证 Messages 不透明推理只能复用原调用方、模型、会话和连接。
// 参数 t 为上下文；前置独立内存路由状态，首次密文拒绝，成功首轮后固定连接，其他身份/会话/协议入口均拒绝；内存夹具自动回收。
func TestMessagesEnvelopeRequiresOwnedConnection(t *testing.T) {
	s := &State{}
	p := &auth.Principal{UserID: "alice"}
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	r.Header.Set("X-Session-Id", "conversation")
	body := map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "redacted_thinking", "data": llmdialogue.ResponsesReasoningSignaturePrefix + "cipher"},
	}}}}
	if s.PlanRoute(r, "model", body, p).Err == nil {
		t.Fatal("未建立原连接归属的封装被放行")
	}
	first := s.PlanRoute(r, "model", map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Read"}}}, p)
	first.Target = dpcontract.RouteTarget{DeploymentID: "dep", ConnectionID: "account-a", Model: "model", Pool: "model"}
	s.CommitRoute(first, "dep", "response-a")
	got := s.PlanRoute(r, "model", body, p)
	if got.Err != nil || !got.Required || got.Target.ConnectionID != "account-a" {
		t.Fatalf("Messages 封装未绑定原连接: %+v", got)
	}
	for _, tc := range []struct{ user, alias, session, path string }{
		{"bob", "model", "conversation", "/v1/messages"}, {"alice", "other", "conversation", "/v1/messages"},
		{"alice", "model", "new", "/v1/messages"}, {"alice", "model", "conversation", "/v1/responses"},
	} {
		req := httptest.NewRequest("POST", tc.path, nil)
		req.Header.Set("X-Session-Id", tc.session)
		if candidate := s.PlanRoute(req, tc.alias, body, &auth.Principal{UserID: tc.user}); candidate.Err == nil {
			t.Fatalf("跨范围密文重放被接受: %+v", tc)
		}
	}
}

// TestNativeMessagesFullHistorySurvivesRename 验证完整原厂历史无需本地归属且改名后仍能选路。
// 参数 t 为上下文；独立内存夹具覆盖首次、已绑定、改名及非法会话，保留原签名，无外部清理。
func TestNativeMessagesFullHistorySurvivesRename(t *testing.T) {
	for _, block := range []map[string]any{{"type": "thinking", "thinking": "Plan", "signature": "vendor-signature"}, {"type": "redacted_thinking", "data": "vendor-cipher"}} {
		s := &State{}
		p := &auth.Principal{UserID: "alice"}
		r := httptest.NewRequest("POST", "/v1/messages", nil)
		r.Header.Set("X-Session-Id", "native")
		body := map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{block}}}}
		first := s.PlanRoute(r, "model", body, p)
		if first.Err != nil || first.Required {
			t.Fatalf("完整原生历史不应要求本地归属: %+v", first)
		}
		first.Target = dpcontract.RouteTarget{DeploymentID: "old", ConnectionID: "account-a", Model: "model", Pool: "model"}
		s.CommitRoute(first, "old", "msg-first")
		for _, alias := range []string{"model", "renamed"} {
			got := s.PlanRoute(r, alias, body, p)
			if got.Err != nil || got.Required {
				t.Fatalf("改名前后不应强制绑定旧部署: %+v", got)
			}
		}
		r.Header.Set("X-Session-Id", strings.Repeat("x", 4097))
		if s.PlanRoute(r, "renamed", body, p).Err == nil {
			t.Fatal("完整历史不能绕过非法会话校验")
		}
	}
}
