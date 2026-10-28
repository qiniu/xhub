package regression

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestServerRuntimeBoundary 以真实HTTP/PostgreSQL复现旧server健康和个人配置被通用KV冒充的结果。
// 前置隔离租户、本地模型及名称碰撞配置；验证身份、明确拒绝、无秘密回显/持久化、普通CRUD和真实推理预算。
// 本测试不访问外部MCP服务；harness严格回收私有schema、Redis和HTTP服务。
func TestServerRuntimeBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("server-runtime"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "server-runtime")
	for _, id := range []string{"user-credential", "user-env-vars"} {
		h.ok("POST", "/server", admin, map[string]any{"server_id": id, "server_name": id, "url": "http://127.0.0.1:1/unreachable", "note": "original"})
	}
	before, err := h.store.ListKV("server")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ method, path string }{
		{"GET", "/server/health"},
		{"POST", "/server/example/user-credential"}, {"DELETE", "/server/example/user-credential"},
		{"GET", "/server/example/user-env-vars"}, {"POST", "/server/example/user-env-vars"}, {"DELETE", "/server/example/user-env-vars"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {admin, 501}} {
				out := h.do(op.method, op.path, actor.token, map[string]any{"credentials": map[string]any{"api_key": "private-runtime-key"}, "env_vars": map[string]any{"TOKEN": "private-runtime-env"}})
				if out.status != actor.status {
					t.Errorf("旧server运行时入口状态错误: %s", out.describe())
					continue
				}
				if actor.status == 501 {
					e, _ := out.json()["error"].(map[string]any)
					if e["type"] != "unsupported_operation" || out.header("Cache-Control") != "no-store" || strings.Contains(out.text(), "private-runtime") || out.header("Location") != "" || out.header("Set-Cookie") != "" {
						t.Error("不支持操作的错误契约或保密边界不正确", out.describe())
					}
				}
			}
		})
	}
	after, err := h.store.ListKV("server")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Error("个人凭据或环境变量操作误改公共配置", err)
	}
	if len(h.upstreamCalls()) != 0 {
		t.Fatal("未实现探测不应调用模型上游")
	}
	// 普通配置必须仍支持读写删除；特殊名称不是一概禁用，拒绝只针对注册的子操作。
	for _, id := range []string{"user-credential", "user-env-vars"} {
		got := h.ok("GET", "/server/"+id, admin, nil).json()
		if got["note"] != "original" {
			t.Fatal("普通server配置被误改", got)
		}
		h.ok("PUT", "/server", admin, map[string]any{"server_id": id, "note": "updated"})
		if h.ok("GET", "/server/"+id, admin, nil).json()["note"] != "updated" {
			t.Fatal("普通更新未生效")
		}
	}
	h.ok("GET", "/auth/me", owner.session, nil)
	request := chatRequest("server-runtime", "Verify unavailable server helpers do not interrupt inference.")
	got := h.ok("POST", "/v1/chat/completions", owner.key, request)
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("正常推理或计费发生变化")
	}
	blocked := h.openScope(t, admin, "server-runtime-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, request); out.status != http.StatusForbidden || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算拦截发生变化", out.describe())
	}
	for _, id := range []string{"user-credential", "user-env-vars"} {
		h.ok("DELETE", "/server/"+id, admin, nil)
		if h.do("GET", "/server/"+id, admin, nil).status != 404 {
			t.Fatal("普通server未清理")
		}
	}
}
