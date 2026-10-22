package regression

import (
	"net/http"
	"strings"
	"testing"
)

// TestCustomKeyFailurePreservesSession 验证无效自定义Key不会撤销独立登录会话。
// 前置真实网关、隔离数据库与本地上游；核对401错误、会话身份、模型目录和实际推理，harness自动回收数据。
func TestCustomKeyFailurePreservesSession(t *testing.T) {
	h := newHarness(t, chatDeployment("session-isolation"))
	owner := h.openScope(t, h.adminSession(), "session-isolation")
	bad := h.do(http.MethodGet, "/model/available", "sk-invalid-custom", nil)
	if bad.status != http.StatusUnauthorized || !strings.Contains(bad.text(), "Authentication Error") {
		t.Fatalf("无效自定义Key应返回鉴权错误，得到 %s", bad.describe())
	}
	me := h.ok(http.MethodGet, "/auth/me", owner.session, nil)
	if !strings.Contains(me.text(), owner.userID) {
		t.Fatalf("会话核验必须返回原用户身份，得到 %s", me.describe())
	}
	models := h.ok(http.MethodGet, "/model/available", owner.session, nil)
	if !strings.Contains(models.text(), "session-isolation") {
		t.Fatalf("登录会话应继续访问模型目录，得到 %s", models.describe())
	}
	reply := h.ok(http.MethodPost, "/v1/chat/completions", owner.session, chatRequest("session-isolation", "Verify independent session"))
	if !strings.Contains(reply.text(), "regression-ok") {
		t.Fatalf("有效会话应完成真实网关推理，得到 %s", reply.describe())
	}
	h.ok(http.MethodPost, "/auth/logout", owner.session, nil)
	invalid := h.do(http.MethodGet, "/auth/me", owner.session, nil)
	if invalid.status != http.StatusUnauthorized {
		t.Fatalf("无效会话核验应返回401，得到 %s", invalid.describe())
	}
}
