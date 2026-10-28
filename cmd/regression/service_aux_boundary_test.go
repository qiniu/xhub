package regression

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// serviceAuxCases 独立列举C1验收入口，不导入实现中的匹配表，防止实现漏项被测试同步隐藏。
// 参数模板使用audit-resource替换；只包含旧辅助操作，不含真实配置CRUD或数据面。
var serviceAuxCases = []string{
	"DELETE /policies/attachments/{attachment_id}",
	"DELETE /policies/name/{policy_name}/all-versions",
	"GET /credentials/by_model/{model_id}",
	"GET /credentials/migrate-encryption/check",
	"GET /policies/attachments/list",
	"GET /policies/attachments/{attachment_id}",
	"GET /policies/compare",
	"GET /policies/name/{policy_name}/versions",
	"GET /policies/usage/overview",
	"GET /policies/{policy_id}/resolved-guardrails",
	"GET /policy/templates",
	"GET /prompts/{prompt_id}/versions",
	"POST /credentials/migrate-encryption",
	"POST /policies/attachments",
	"POST /policies/attachments/estimate-impact",
	"POST /policies/name/{policy_name}/versions",
	"POST /policies/resolve",
	"POST /policies/test-pipeline",
	"POST /policy/templates/enrich",
	"POST /policy/templates/enrich/stream",
	"POST /policy/templates/suggest",
	"POST /policy/templates/test",
	"POST /policy/test",
	"POST /policy/validate",
	"POST /utils/test_policies_and_guardrails",
	"PUT /policies/{policy_id}/status",
}

// TestServiceAuxBoundary 验证26个辅助入口没有执行器时明确拒绝，原有权限、JSON错误和公共配置保持。
// 前置真实HTTP/隔离PostgreSQL及普通用户；先预置操作名碰撞记录，逐项核对全部KV不变和零上游调用，harness清理schema。
func TestServiceAuxBoundary(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	user := h.openScope(t, admin, "service-aux")
	for _, kind := range []string{"policy", "policies", "credentials", "prompts", "utils"} {
		for _, id := range []string{"audit-resource", "attachments", "all-versions", "versions", "status", "test-pipeline", "resolve", "migrate-encryption", "check", "test", "validate", "templates", "suggest", "enrich", "stream", "estimate-impact", "test_policies_and_guardrails"} {
			raw, _ := json.Marshal(map[string]any{"id": id, "marker": "original-private-fixture"})
			if err := h.store.PutKV(kind, id, string(raw)); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
	if err != nil {
		t.Fatal(err)
	}
	parameter := regexp.MustCompile(`\{[^}]+\}`)
	for _, route := range serviceAuxCases {
		t.Run(route, func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			path = parameter.ReplaceAllString(path, "audit-resource")
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {user.session, 403}, {admin, 501}} {
				r := h.do(method, path, actor.token, map[string]any{"private_input": "must-not-echo"})
				if r.status != actor.status {
					t.Fatalf("辅助操作权限/能力错误: %s %s", route, r.describe())
				}
				if actor.status == 501 {
					e, ok := r.json()["error"].(map[string]any)
					if !ok || e["type"] != "unsupported_operation" || r.header("Cache-Control") != "no-store" || strings.Contains(r.text(), "must-not-echo") {
						t.Fatalf("缺少明确失败契约: %s", r.describe())
					}
				}
			}
			if method == "POST" || method == "PUT" {
				if r := catalogRawRequest(t, h, method, path, admin, `{"broken":`); r.status != 400 {
					t.Fatal("JSON错误顺序改变", r.describe())
				}
				if r := h.do(method, path, admin, map[string]any{"jsonrpc": "2.0"}); r.status != 400 {
					t.Fatal("RPC拒绝顺序改变", r.describe())
				}
			}
			after, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("辅助操作改写/删除了公共KV: %s", route)
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("未实现操作意外调用上游")
			}
		})
	}
	h.ok("GET", "/auth/me", admin, nil)
}

// TestServiceAuxBoundaryPreservesResources 验证操作名仍可作为正常资源ID，单数策略读取及未注册路径边界不变。
// 前置真实HTTP/私有数据库；正常CRUD读回与删除均断言，未知路径404；普通用户/预算规则另由既有生命周期回归覆盖，schema最终清理。
func TestServiceAuxBoundaryPreservesResources(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	for _, tc := range []struct{ base, id, field, read, update string }{
		{"/policies", "status", "policy_id", "/policies/status", "PUT"},
		{"/prompts", "versions", "prompt_id", "/prompts/versions", "PUT"},
		{"/credentials", "migrate-encryption", "credential_name", "/credentials/by_name/migrate-encryption", "PATCH"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			h.ok("POST", tc.base, admin, map[string]any{tc.field: tc.id, "note": "original"})
			h.ok(tc.update, tc.base+"/"+tc.id, admin, map[string]any{"note": "saved"})
			if read := h.ok("GET", tc.read, admin, nil).json(); read["note"] != "saved" {
				t.Fatal("合法操作名资源未保存", read)
			}
			h.ok("DELETE", tc.base+"/"+tc.id, admin, nil)
			if got := h.do("GET", tc.read, admin, nil); got.status != 404 {
				t.Fatal("正常资源未删除", got.describe())
			}
		})
	}
	if err := h.store.PutKV("policy", "fixture", `{"id":"fixture","note":"singular-original"}`); err != nil {
		t.Fatal(err)
	}
	if got := h.ok("GET", "/policy/info/fixture", admin, nil).json(); got["note"] != "singular-original" {
		t.Fatal("单数策略读取改变", got)
	}
	if got := rowsOf(h.ok("GET", "/policy/list", admin, nil), "policies"); len(got) != 1 {
		t.Fatal("单数策略列表改变", got)
	}
	if err := h.store.DeleteKV("policy", "fixture"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/policies/a/b/resolved-guardrails", "/prompts/a/b/versions", "/credentials/migrate-encryption/unknown"} {
		if got := h.do("GET", path, admin, nil); got.status != 404 {
			t.Fatal("未知路径被恢复", path, got.describe())
		}
	}
}
