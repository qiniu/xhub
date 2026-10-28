package regression

import (
	"reflect"
	"strings"
	"testing"
)

// TestServerOAuthBoundary 通过真实HTTP/PostgreSQL验证旧server授权别名不把KV写入当成授权完成。
// 前置隔离租户、本地模型及名为token的普通server；验证原权限、无凭据回显/写入/删除、会话与对话预算规则。
// 测试产生的server、用户及日志由harness私有schema严格清理，不连接OAuth供应商。
func TestServerOAuthBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("server-oauth"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "server-oauth")
	h.ok("POST", "/server", admin, map[string]any{"server_id": "token", "server_name": "ordinary-config", "note": "original"})
	before, err := h.store.ListKV("server")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ method, path string }{
		{"POST", "/server/oauth/session"}, {"GET", "/server/oauth/token/authorize"},
		{"POST", "/server/oauth/token/register"}, {"POST", "/server/oauth/token/token"},
		{"POST", "/server/token/oauth-user-credential"}, {"DELETE", "/server/token/oauth-user-credential"},
		{"GET", "/server/token/oauth-user-credential/status"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {admin, 501}} {
				got := h.do(op.method, op.path, actor.token, map[string]any{"access_token": "private-fixture-token", "refresh_token": "private-fixture-refresh", "code": "private-fixture-code", "server_id": "token"})
				if got.status != actor.status {
					t.Errorf("旧授权身份/拒绝状态错误: %s", got.describe())
					continue
				}
				if actor.status == 501 {
					e, _ := got.json()["error"].(map[string]any)
					if e["type"] != "unsupported_operation" || strings.Contains(got.text(), "private-fixture") || got.header("Location") != "" || got.header("Set-Cookie") != "" || got.header("Cache-Control") != "no-store" {
						t.Error("旧授权拒绝契约错误或泄露凭据", got.describe())
					}
				}
			}
		})
	}
	after, err := h.store.ListKV("server")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Error("授权占位操作改动了server配置", before, after, err)
	}
	// 普通server即使名称为token仍允许原CRUD，不把企业SSO或正常配置泛化为OAuth占位。
	if got := h.ok("GET", "/server/token", admin, nil).json(); got["server_name"] != "ordinary-config" {
		t.Fatal("正常server配置改变", got)
	}
	h.ok("PUT", "/server", admin, map[string]any{"server_id": "token", "note": "updated"})
	if got := h.ok("GET", "/server/token", admin, nil).json(); got["note"] != "updated" {
		t.Fatal("正常server更新失败", got)
	}
	h.ok("GET", "/auth/me", owner.session, nil)
	request := chatRequest("server-oauth", "Verify rejected authorization does not interrupt inference.")
	got := h.ok("POST", "/v1/chat/completions", owner.key, request)
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("真实模型调用或费用改变")
	}
	blocked := h.openScope(t, admin, "server-oauth-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if denied := h.do("POST", "/v1/chat/completions", blocked.key, request); denied.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算仍须拦截", denied.describe())
	}
	h.ok("DELETE", "/server/token", admin, nil)
	if missing := h.do("GET", "/server/token", admin, nil); missing.status != 404 {
		t.Fatal("普通server删除失败", missing.describe())
	}
}
