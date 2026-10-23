package affinity

import (
	"net/http/httptest"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
)

// TestExternalCompaction 验证旧客户端完整压缩历史无需本地首轮记录，非法形态不获豁免。
// 参数 t 为测试上下文；使用空内存状态，验证原生路径及父响应/私有引用隔离，结束释放内存。
func TestExternalCompaction(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{{"vendor-cipher", true}, {"", false}, {"  ", false}, {nil, false}, {42, false}, {"xhub_messages_v1:unknown", false}} {
		body := map[string]any{"input": []any{map[string]any{"type": "compaction", "encrypted_content": tc.value}}}
		if got := hasExternalCompaction(body); got != tc.want {
			t.Fatalf("密文类型 %T 判定=%v，预期=%v", tc.value, got, tc.want)
		}
	}
	body := map[string]any{"input": []any{map[string]any{"type": "compaction", "encrypted_content": "vendor-cipher"}, map[string]any{"role": "user", "content": "Continue."}}}
	s := &State{}
	req := httptest.NewRequest("POST", "/v1/responses", nil)
	p := &auth.Principal{UserID: "alice"}
	if got := s.PlanRoute(req, "model", body, p); got.Err != nil || got.Required {
		t.Fatalf("完整压缩历史被本地归属拦截: %+v", got)
	}
	body["previous_response_id"] = "other-user-response"
	if got := s.PlanRoute(req, "model", body, p); got.Err == nil {
		t.Fatal("压缩历史绕过父响应归属")
	}
	delete(body, "previous_response_id")
	body["input"] = append(body["input"].([]any), map[string]any{"type": "reasoning", "encrypted_content": "xhub_messages_v1:unknown"})
	if got := s.PlanRoute(req, "model", body, p); got.Err == nil {
		t.Fatal("压缩历史绕过网关私有引用校验")
	}
	if hasExternalCompaction(nil) {
		t.Fatal("空请求误判压缩历史")
	}
}
