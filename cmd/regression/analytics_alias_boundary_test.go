package regression

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// analyticsAliasCases 固化C5的26条范围，不从生产匹配器生成，保留三个既有IAM边界。
var analyticsAliasCases = []string{
	"GET /global/activity/exceptions",
	"GET /global/activity/exceptions/deployment",
	"GET /global/all_end_users",
	"GET /global/spend",
	"GET /global/spend/report",
	"GET /key/spend/report",
	"GET /model/metrics",
	"GET /model/metrics/exceptions",
	"GET /model/metrics/slow_responses",
	"GET /model/streaming_metrics",
	"GET /organization/spend/report",
	"GET /otel-spans",
	"GET /spend/logs",
	"GET /spend_logs/end_users",
	"GET /spend_logs/users",
	"GET /tag/dau",
	"GET /tag/distinct",
	"GET /tag/mau",
	"GET /tag/summary",
	"GET /tag/user-agent/per-user-analytics",
	"GET /tag/wau",
	"GET /team/spend/report",
	"GET /user/spend/report",
	"POST /global/spend/refresh",
	"POST /global/spend/reset",
	"POST /team/{team_id}/member/{user_id}/reset_spend",
}

// TestAnalyticsAliasBoundary 通过真实HTTP/隔离PG产生成功、失败和消费，再验证旧统计不假成功或更改账单。
// 验证鉴权、JSON错误、KV隔离、真实日志及用量；harness严格清理专属schema，无生产凭据。
func TestAnalyticsAliasBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("analytics-chat"))
	admin := h.adminSession()
	a := h.provision(t, admin, "analytics")
	success := h.ok("POST", "/v1/chat/completions", a.key, chatRequest("analytics-chat", "Record a successful analytics request."))
	h.failUpstream(true)
	failed := h.do("POST", "/v1/chat/completions", a.key, chatRequest("analytics-chat", "Record a failed analytics request."))
	h.failUpstream(false)
	if failed.status != 500 {
		t.Fatal("故障注入未到真实上游", failed.describe())
	}
	h.flushSpend()
	for _, item := range []struct{ id, status string }{{success.header("x-litellm-call-id"), "non_error"}, {failed.header("x-litellm-call-id"), "error"}} {
		r := h.ok("GET", "/spend/logs/ui?status_filter="+item.status, admin, nil)
		if findLogByRequestID(rowsOf(r, "data"), item.id) == nil {
			t.Fatal("真实日志未包含对应成功/失败请求", r.describe())
		}
	}
	userBefore, err := h.db.GetUser(t.Context(), a.userID)
	if err != nil || userBefore.Spend <= 0 {
		t.Fatal("夹具必须产生真实费用", userBefore, err)
	}
	logsBefore := h.ok("GET", "/spend/logs/ui?page_size=100", admin, nil).json()
	activityBefore := h.ok("GET", "/user/daily/activity?user_id="+a.userID, admin, nil).json()
	for _, kind := range []string{"global", "spend", "spend_logs", "otel-spans", "model", "tag", "key", "user"} {
		raw, _ := json.Marshal(map[string]any{"id": "fixture", "secret": "private-catalog-sentinel"})
		if err := h.store.PutKV(kind, "fixture", string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	kvBefore, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
	if err != nil {
		t.Fatal(err)
	}
	calls := len(h.upstreamCalls())
	for _, route := range analyticsAliasCases {
		t.Run(strings.ReplaceAll(route, "/", "|"), func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			path = strings.ReplaceAll(strings.ReplaceAll(path, "{team_id}", a.teamID), "{user_id}", a.userID)
			kind := "unsupported_analytics_operation"
			if strings.HasPrefix(path, "/team/") || strings.HasPrefix(path, "/organization/") {
				kind = "unsupported_iam_operation"
			}
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {a.session, 403}, {admin, 501}} {
				r := h.do(method, path, actor.token, map[string]any{"user_id": a.userID, "api_key": "private-input-sentinel"})
				if r.status != actor.status {
					t.Fatalf("%s want=%d %s", route, actor.status, r.describe())
				}
				if actor.status == 501 {
					e, _ := r.json()["error"].(map[string]any)
					if e["type"] != kind {
						t.Fatal("错误类型改变", r.describe())
					}
					if kind == "unsupported_analytics_operation" && r.header("Cache-Control") != "no-store" {
						t.Fatal("错误应禁止缓存")
					}
					if strings.Contains(r.text(), "sentinel") {
						t.Fatal("泄露私密数据", r.describe())
					}
				}
			}
			if method == "POST" && strings.HasPrefix(path, "/global/") {
				if r := catalogRawRequest(t, h, method, path, admin, `{"id":`); r.status != 400 {
					t.Fatal("无效JSON应400", r.describe())
				}
				if r := h.do(method, path, admin, map[string]any{"jsonrpc": "2.0"}); r.status != 400 {
					t.Fatal("RPC不可冒充REST", r.describe())
				}
			}
		})
	}
	kvAfter, err := h.store.Engine.QueryString("SELECT kind,id,body FROM kv ORDER BY kind,id")
	if err != nil || !reflect.DeepEqual(kvBefore, kvAfter) {
		t.Fatal("旧操作污染KV", err)
	}
	userAfter, err := h.db.GetUser(t.Context(), a.userID)
	if err != nil || !reflect.DeepEqual(userBefore, userAfter) {
		t.Fatal("旧操作更改真实用户或预算", err)
	}
	if got := h.ok("GET", "/spend/logs/ui?page_size=100", admin, nil).json(); !reflect.DeepEqual(logsBefore, got) {
		t.Fatal("旧操作更改账单日志")
	}
	if got := h.ok("GET", "/user/daily/activity?user_id="+a.userID, admin, nil).json(); !reflect.DeepEqual(activityBefore, got) {
		t.Fatal("旧操作更改真实用量")
	}
	if len(h.upstreamCalls()) != calls {
		t.Fatal("统计查询不应调用模型")
	}
}
