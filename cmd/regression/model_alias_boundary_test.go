package regression

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// modelAliasCases 固化C3的14条模型旧入口；与生产匹配器独立维护，避免同错同过。
var modelAliasCases = []string{
	"DELETE /fallback/{model}",
	"GET /fallback/login",
	"GET /fallback/{model}",
	"GET /model/deprecations",
	"GET /model/info",
	"GET /model/settings",
	"GET /models/{model_id}",
	"GET /v1/model/deprecations",
	"GET /v1/model/info",
	"POST /fallback",
	"POST /make_public",
	"POST /model/block",
	"POST /model/unblock",
	"POST /model_group/make_public",
}

// TestModelAliasBoundary 使用真实HTTP和隔离PostgreSQL验证未实现操作不能读写公共KV或发上游请求。
// 管理员、普通用户、匿名及JSON错误逐项校验；harness清理用户、配置与schema，不修改线上数据。
func TestModelAliasBoundary(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	user := h.openScope(t, admin, "model-alias")
	h.addDBModel(t, admin, "fixture", "fixture", "dep-fixture", nil)
	modelsBefore := h.ok("GET", "/v2/model/info", admin, nil).json()

	for _, kind := range []string{"model", "models", "v1", "fallback", "make_public", "model_group"} {
		for _, id := range []string{"fixture", "info", "deprecations", "settings", "login", "block", "unblock"} {
			raw, _ := json.Marshal(map[string]any{"id": id, "marker": "private-config-fixture"})
			if err := h.store.PutKV(kind, id, string(raw)); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range modelAliasCases {
		t.Run(strings.ReplaceAll(route, "/", "|"), func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			path = strings.NewReplacer("{model}", "fixture", "{model_id}", "fixture").Replace(path)
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {user.session, 403}, {admin, 501}} {
				r := h.do(method, path, actor.token, map[string]any{"id": "fixture", "api_key": "private-input-fixture", "enabled": true})
				if r.status != actor.status {
					t.Fatalf("模型旧接口操作鉴权或能力边界错误: %s %s", route, r.describe())
				}
				if actor.status == 501 {
					e, ok := r.json()["error"].(map[string]any)
					if !ok || e["type"] != "unsupported_model_operation" || r.header("Cache-Control") != "no-store" {
						t.Fatal("缺少明确失败契约", r.describe())
					}
					if strings.Contains(r.text(), "private-input-fixture") || strings.Contains(r.text(), "private-config-fixture") {
						t.Fatal("模型旧接口失败不应回显凭据或存量配置", r.describe())
					}
				}
			}
			if method != "GET" {
				if r := catalogRawRequest(t, h, method, path, admin, `{"id":`); r.status != 400 {
					t.Fatal("无效JSON顺序改变", r.describe())
				}
				if r := h.do(method, path, admin, map[string]any{"jsonrpc": "2.0"}); r.status != 400 {
					t.Fatal("REST入口误接受RPC", r.describe())
				}
			}
			after, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("旧模型旧接口操作不应修改公共KV", route)
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("旧模型旧接口操作不应调用上游", route)
			}
		})
	}
	h.ok("GET", "/auth/me", admin, nil)
	h.ok("GET", "/auth/me", user.session, nil)
	if !reflect.DeepEqual(modelsBefore, h.ok("GET", "/v2/model/info", admin, nil).json()) {
		t.Fatal("旧别名改变真实部署")
	}
	if r := h.ok("GET", "/v1/models/fixture", user.key, nil).json(); r["id"] != "fixture" {
		t.Fatal("真实模型详情错误", r)
	}
	request := chatRequest("fixture", "Verify model management isolation.")
	h.ok("POST", "/v1/chat/completions", user.key, request)
	beforeCalls := len(h.upstreamCalls())
	h.ok("POST", "/model/disable", admin, map[string]any{"id": "dep-fixture"})
	if r := h.do("POST", "/v1/chat/completions", user.key, request); r.status != 400 || len(h.upstreamCalls()) != beforeCalls {
		t.Fatal("真实停用未阻止推理", r.describe())
	}
	h.ok("POST", "/model/enable", admin, map[string]any{"id": "dep-fixture"})
	// 使用新提示词，避免命中首次请求缓存而误把缓存回复当成重新拨到上游。
	request = chatRequest("fixture", "Verify inference after re-enabling the deployment.")
	h.ok("POST", "/v1/chat/completions", user.key, request)
	if len(h.upstreamCalls()) != beforeCalls+1 {
		t.Fatal("真实启用未恢复推理")
	}
	blocked := h.openScope(t, admin, "model-alias-zero-budget")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if r := h.do("POST", "/v1/chat/completions", blocked.key, request); r.status != 403 || len(h.upstreamCalls()) != beforeCalls+1 {
		t.Fatal("零预算规则改变", r.describe())
	}
	h.ok("POST", "/model/delete", admin, map[string]any{"id": "dep-fixture"})
	if r := h.ok("GET", "/v2/model/info?modelId=dep-fixture", admin, nil).json(); len(r["data"].([]any)) != 0 {
		t.Fatal("部署清理失败", r)
	}

}
