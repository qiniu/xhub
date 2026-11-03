package regression

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestLegacyWebsocketBoundary 使用真实RFC6455客户端验证两条未实现别名不返回假200/101。
// 前置隔离PG、真实登录和本地供应商；覆盖匿名、坏Key、普通用户与管理员、旧伪方法及GET握手。
// 验证拒绝无上游调用且正常对话、计费仍有效；harness回收全部身份、账单和schema。
func TestLegacyWebsocketBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("websocket-boundary"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "websocket-boundary")
	for _, path := range []string{"/openai_passthrough/responses", "/vertex_ai/live"} {
		for _, actor := range []struct {
			name, token string
			status      int
		}{
			{"anonymous", "", 401}, {"invalid", "sk-invalid-fixture", 401}, {"user", owner.session, 501}, {"key", owner.key, 501}, {"admin", admin, 501},
		} {
			t.Run(path+"/"+actor.name, func(t *testing.T) {
				headers := http.Header{}
				if actor.token != "" {
					headers.Set("Authorization", "Bearer "+actor.token)
				}
				dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
				conn, resp, err := dialer.Dial("ws"+strings.TrimPrefix(h.server.URL, "http")+path, headers)
				if conn != nil {
					conn.Close()
					t.Fatal("未实现接口错误建立了WebSocket连接")
				}
				if err == nil || resp == nil {
					t.Fatalf("握手缺少明确HTTP拒绝: response=%v error=%v", resp, err)
				}
				defer resp.Body.Close()
				raw, readErr := io.ReadAll(resp.Body)
				if readErr != nil || resp.StatusCode != actor.status {
					t.Fatalf("握手拒绝错误: status=%d body=%s err=%v", resp.StatusCode, raw, readErr)
				}
				if actor.status == 501 && (!strings.Contains(string(raw), "unsupported_protocol_operation") || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Sec-WebSocket-Accept") != "") {
					t.Fatalf("假握手或错误契约: %s", raw)
				}
				// 兼容历史目录中的伪HTTP方法，但不能保留其假成功；普通GET也给出同一鉴权结果。
				for _, method := range []string{"GET", "WEBSOCKET"} {
					out := h.do(method, path, actor.token, nil)
					if out.status != actor.status {
						t.Fatalf("%s 拒绝边界错误: %s", method, out.describe())
					}
				}
			})
		}
	}
	if len(h.upstreamCalls()) != 0 {
		t.Fatal("未实现协议调用了供应商")
	}
	h.ok("GET", "/auth/me", owner.session, nil)
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("websocket-boundary", "Verify chat after a rejected WebSocket handshake."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("正常推理与计费异常")
	}
}
