package regression

import (
	"net/http"
	"testing"
)

// TestUserContractPersonalCaptureOfRejectedRequest 用真实 HTTP 和隔离 PostgreSQL 验证已鉴权的标准/透传参数错误。
// 开启时保存正文，关闭不记录，匿名请求不继承他人设置；无外部供应商，harness 自动清理账号、日志和 schema。
func TestUserContractPersonalCaptureOfRejectedRequest(t *testing.T) {
	h := openHarness(t, false)
	member := h.provision(t, h.adminSession(), "capture-rejected")
	for _, enabled := range []bool{true, false} {
		h.ok(http.MethodPut, "/account/log-capture", member.session, map[string]any{"enabled": enabled})
		for _, path := range []string{"/v1/chat/completions", "/bypass/openai/v1/chat/completions"} {
			response := h.do(http.MethodPost, path, member.key, chatRequest("capture-missing-model", "Synthetic rejected request."))
			if response.status != 400 {
				t.Fatalf("模型校验未拒绝: %s", response.describe())
			}
			h.flushSpend()
			detail := logDetail(t, h, member.session, response.header("x-litellm-call-id"))
			want := "disabled"
			if enabled {
				want = "saved"
			}
			if detail["body_capture_status"] != want {
				t.Fatalf("%s enabled=%v 的失败请求记录状态=%v，预期=%s", path, enabled, detail["body_capture_status"], want)
			}
			if enabled && detail["messages"] == nil {
				t.Fatal("已授权记录的拒绝请求缺少正文")
			}
		}
	}
	anonymous := h.do(http.MethodPost, "/v1/chat/completions", "", chatRequest("capture-missing-model", "Anonymous request."))
	if anonymous.status != 401 {
		t.Fatal("未登录请求不应放行")
	}
}
