package regression

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// identityAliasCases 独立登记C2的22条精确旧身份入口，不读取生产拒绝表，避免实现漏项时测试同步漏测。
var identityAliasCases = []string{
	"GET /authorize",
	"GET /discover",
	"GET /enabled",
	"GET /invitation/info",
	"GET /jwt/key/mapping/info",
	"GET /jwt/key/mapping/list",
	"GET /onboarding/get_token",
	"GET /token/generate",
	"POST /delete",
	"POST /introspect",
	"POST /invitation/delete",
	"POST /invitation/new",
	"POST /invitation/update",
	"POST /jwt/key/mapping/delete",
	"POST /jwt/key/mapping/new",
	"POST /jwt/key/mapping/update",
	"POST /onboarding/claim_token",
	"POST /register",
	"POST /revoke",
	"POST /test/connection",
	"POST /token",
	"POST /v3/login/exchange",
}

// TestIdentityAliasBoundary 验证旧身份操作明确拒绝，不签发或撤销会话、不泄漏输入、不误读写同名公共配置。
// 前置真实HTTP、隔离PostgreSQL和普通用户；逐条比较全部KV，保留原鉴权和JSON失败顺序，harness回收schema。
func TestIdentityAliasBoundary(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	user := h.openScope(t, admin, "identity-alias")
	for _, kind := range []string{"authorize", "discover", "enabled", "invitation", "jwt", "onboarding", "token", "delete", "introspect", "register", "revoke", "test", "v3"} {
		for _, id := range []string{"fixture", "mapping", "generate", "claim_token", "get_token", "connection", "exchange"} {
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
	for _, route := range identityAliasCases {
		t.Run(strings.ReplaceAll(route, "/", "|"), func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {user.session, 403}, {admin, 501}} {
				r := h.do(method, path, actor.token, map[string]any{"id": "fixture", "code": "private-fixture-code", "token": "private-fixture-token", "user_id": user.userID})
				if r.status != actor.status {
					t.Fatalf("旧身份操作权限/能力错误: %s %s", route, r.describe())
				}
				if actor.status == 501 {
					e, ok := r.json()["error"].(map[string]any)
					if !ok || e["type"] != "unsupported_operation" || r.header("Cache-Control") != "no-store" || r.header("Set-Cookie") != "" {
						t.Fatal("未明确拒绝或意外签发Cookie", r.describe())
					}
					for _, secret := range []string{"private-fixture-code", "private-fixture-token", "original-private-fixture", `"access_token"`, `"id_token"`} {
						if strings.Contains(r.text(), secret) {
							t.Fatal("旧身份响应泄漏输入或伪造令牌", r.describe())
						}
					}
				}
			}
			if method == "POST" {
				if r := catalogRawRequest(t, h, method, path, admin, `{"code":`); r.status != 400 {
					t.Fatal("损坏JSON错误顺序改变", r.describe())
				}
				if r := h.do(method, path, admin, map[string]any{"jsonrpc": "2.0"}); r.status != 400 {
					t.Fatal("RPC错误顺序改变", r.describe())
				}
			}
			after, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("旧身份操作改变公共KV", route)
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("旧身份操作不应调用上游")
			}
		})
	}
	h.ok("GET", "/auth/me", admin, nil)
	h.ok("GET", "/auth/me", user.session, nil)
	if got := h.do("POST", "/register", user.session, nil); got.status != 403 {
		t.Fatal("普通用户被意外提升权限", got.describe())
	}
}
