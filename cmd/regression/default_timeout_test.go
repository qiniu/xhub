package regression

import (
	"net/http"
	"testing"
	"time"
)

// TestBuiltinRequestTimeout 验证真实鉴权路由为未绑定模板的推理请求创建600秒完整读取期限。
// 参数t为测试上下文；真实本地上游和私有数据库，传输观察器只核验context后继续真实HTTP；harness回收数据。
func TestBuiltinRequestTimeout(t *testing.T) {
	h := newHarness(t, chatDeployment("timeout-default"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "timeout-default")
	next := h.gw.Client.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	observed := make(chan time.Duration, 1)
	h.gw.Client.Transport = cacheObserverRoundTripper(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("上游请求缺少期限")
		} else {
			observed <- time.Until(deadline)
		}
		return next.RoundTrip(r)
	})
	h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("timeout-default", "Reply briefly."))
	select {
	case remaining := <-observed:
		if remaining < 590*time.Second || remaining > 600*time.Second {
			t.Fatalf("实际请求期限为%s，期望600秒", remaining)
		}
	default:
		t.Fatal("真实请求未经过上游传输")
	}
	h.flushSpend()
}
