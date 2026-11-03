package regression

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestServerSubmissionBoundary 以真实HTTP/PostgreSQL复现旧server导入、注册和审批被通用KV冒充的结果。
// 前置隔离租户、本地模型及名称碰撞配置；验证身份、明确拒绝、无秘密回显/持久化、普通CRUD和真实推理预算。
// 本测试不访问外部MCP服务；harness严格回收私有schema、Redis和HTTP服务。
func TestServerSubmissionBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("server-submission"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "server-submission")
	for _, id := range []string{"approve", "reject"} {
		h.ok("POST", "/server", admin, map[string]any{"server_id": id, "server_name": id, "url": "http://127.0.0.1:1/unreachable", "note": "original"})
	}
	before, err := h.store.ListKV("server")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ method, path string }{
		{"POST", "/server/import"}, {"POST", "/server/register"}, {"GET", "/server/submissions"},
		{"PUT", "/server/example/approve"}, {"PUT", "/server/example/reject"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			// 原有JSON校验必须先于能力拒绝，损坏正文不能得到成功或进入存储。
			if out := catalogRawRequest(t, h, op.method, op.path, admin, `{"status":`); out.status != 400 {
				t.Fatal("旧审批入口损坏JSON应返回400", out.describe())
			}
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {admin, 501}} {
				out := h.do(op.method, op.path, actor.token, map[string]any{"server_id": "example", "status": "approved", "servers": []any{map[string]any{"url": "http://127.0.0.1:1/unreachable", "api_key": "private-submission-key"}}})
				if out.status != actor.status {
					t.Errorf("旧server审批入口状态错误: %s", out.describe())
					continue
				}
				if actor.status == 501 {
					e, _ := out.json()["error"].(map[string]any)
					if e["type"] != "unsupported_operation" || out.header("Cache-Control") != "no-store" || strings.Contains(out.text(), "private-submission") || out.header("Location") != "" || out.header("Set-Cookie") != "" {
						t.Error("不支持操作的错误契约或保密边界不正确", out.describe())
					}
				}
			}
		})
	}
	after, err := h.store.ListKV("server")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Error("导入或审批操作误改公共配置", err)
	}
	if len(h.upstreamCalls()) != 0 {
		t.Fatal("未实现审批不应调用模型上游")
	}
	// 普通配置必须仍支持读写删除；特殊名称不是一概禁用，拒绝只针对注册的子操作。
	for _, id := range []string{"approve", "reject"} {
		got := h.ok("GET", "/server/"+id, admin, nil).json()
		if got["note"] != "original" {
			t.Fatal("普通server配置被误改", got)
		}
		h.ok("PUT", "/server", admin, map[string]any{"server_id": id, "note": "updated"})
		if h.ok("GET", "/server/"+id, admin, nil).json()["note"] != "updated" {
			t.Fatal("普通更新未生效")
		}
	}
	for _, op := range []struct{ method, path string }{{"POST", "/v1/mcp/server/import"}, {"POST", "/v1/mcp/server/register"}, {"GET", "/v1/mcp/server/submissions"}, {"PUT", "/v1/mcp/server/example/approve"}, {"PUT", "/v1/mcp/server/example/reject"}} {
		out := h.do(op.method, op.path, admin, map[string]any{})
		if out.status != 404 {
			t.Fatal("未注册的MCP前缀入口不能被此修复恢复", out.describe())
		}
	}
	h.ok("GET", "/auth/me", owner.session, nil)
	request := chatRequest("server-submission", "Verify unavailable server helpers do not interrupt inference.")
	got := h.ok("POST", "/v1/chat/completions", owner.key, request)
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("正常推理或计费发生变化")
	}
	blocked := h.openScope(t, admin, "server-submission-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, request); out.status != http.StatusForbidden || len(h.upstreamCalls()) != 1 {
		t.Fatal("零预算拦截发生变化", out.describe())
	}
	for _, id := range []string{"approve", "reject"} {
		h.ok("DELETE", "/server/"+id, admin, nil)
		if h.do("GET", "/server/"+id, admin, nil).status != 404 {
			t.Fatal("普通server未清理")
		}
	}
}
