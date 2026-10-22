package regression

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// TestErrorLogFilterContract 通过真实HTTP产生预算403、速率429和上游402/403，核对客户端与本人日志一致。
// 前置私有PG和本地脚本上游；覆盖所有入口预算拒绝、组合筛选、分页计数、非法输入与身份隔离；harness清理schema。
func TestErrorLogFilterContract(t *testing.T) {
	const model = "error-filter-contract"
	dep := chatDeployment(model)
	dep.ModelInfo["endpoint_types"] = []string{"chat", "bypass:openai-chat"}
	h := newHarness(t, dep)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "error-filter-owner")
	other := h.openScope(t, admin, "error-filter-other")
	body := chatRequest(model, "Verify exact error filtering.")
	h.setKeyBudget(t, admin, owner.key, 0)
	var ids []string
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/messages/count_tokens", "/bypass/openai/v1/chat/completions", "/bypass/openai/v1/responses", "/bypass/anthropic/v1/messages"} {
		request := chatRequest(model, "Verify exact error filtering.")
		if strings.HasSuffix(path, "/responses") {
			request = map[string]any{"model": model, "input": "Verify exact error filtering."}
		}
		if strings.Contains(path, "/messages") {
			request["max_tokens"] = 16
		}
		got := h.do("POST", path, owner.key, request)
		if got.status != 403 || got.header("Retry-After") != "" || !strings.Contains(got.text(), "budget_exceeded") {
			t.Fatalf("%s预算未返回403: %s", path, got.describe())
		}
		id := got.header("x-litellm-call-id")
		ids = append(ids, id)
		detail := logDetail(t, h, owner.session, id)
		if detail["error"] != got.text() || detail["metadata"].(map[string]any)["http_status"] != float64(403) {
			t.Fatalf("%s日志与响应不一致: %v", path, detail)
		}
	}
	if len(h.upstreamCalls()) != 0 {
		t.Fatal("预算拒绝仍调用上游")
	}
	h.setKeyBudget(t, admin, owner.key, 100)
	rate := h.keyWith(t, owner.session, map[string]any{"team_id": owner.teamID, "rpm_limit": 0})
	got := h.do("POST", "/v1/chat/completions", rate, body)
	if got.status != 429 || got.header("Retry-After") == "" || !strings.Contains(got.text(), "rpm_limit") {
		t.Fatalf("RPM应保持429: %s", got.describe())
	}
	rateID := got.header("x-litellm-call-id")
	rateLog := logDetail(t, h, owner.session, rateID)
	if rateLog["error"] != got.text() {
		t.Fatal("速率日志正文与客户端不一致")
	}
	for _, code := range []int{402, 403} {
		h.scriptStatus(model, code)
		got := h.do("POST", "/bypass/openai/v1/chat/completions", owner.key, body)
		if got.status != code {
			t.Fatalf("上游%d被改写: %s", code, got.describe())
		}
		detail := logDetail(t, h, owner.session, got.header("x-litellm-call-id"))
		if detail["error"] != got.text() || detail["metadata"].(map[string]any)["http_status"] != float64(code) {
			t.Fatalf("上游%d日志错误: %v", code, detail)
		}
	}
	for _, tc := range []struct {
		code, message string
		count         int
	}{
		{"403", "budget_exceeded", len(ids)}, {"429", "rpm_limit", 1}, {"403", "rpm_limit", 0},
		{"402", "", 1}, {"403", "", len(ids) + 1}, {"", "budget_exceeded", len(ids)},
		{"403", "' OR true --", 0}, {"broken", "", 0}, {"600", "", 0},
	} {
		q := url.Values{"status_filter": {"error"}, "error_code": {tc.code}, "error_message": {tc.message}, "page_size": {"2"}}
		result := h.ok("GET", "/spend/logs/ui?"+q.Encode(), owner.session, nil)
		rows := rowsOf(result, "data")
		if asIntAny(result.json()["total"]) != tc.count || len(rows) != min(2, tc.count) {
			t.Fatalf("筛选%+v计数/分页错误: %s", tc, result.describe())
		}
		invisible := h.ok("GET", "/spend/logs/ui?"+q.Encode(), other.session, nil)
		if len(rowsOf(invisible, "data")) != 0 {
			t.Fatalf("筛选越权: %s", invisible.describe())
		}
	}
	// 认证失败不能冒认用户；注册Bypass仍必须落日志，仅管理员能够读取。
	unauth := h.do("POST", "/bypass/anthropic/v1/messages", "invalid-fixture-key", body)
	if unauth.status != 401 {
		t.Fatalf("无效密钥未拒绝: %s", unauth.describe())
	}
	id := unauth.header("x-litellm-call-id")
	detail := logDetail(t, h, admin, id)
	if detail["error"] != unauth.text() {
		t.Fatal("Bypass鉴权错误未完整记录")
	}
	if denied := h.do("GET", "/spend/logs/ui/"+id, owner.session, nil); denied.status != 404 {
		t.Fatalf("普通用户可读匿名失败: %s", denied.describe())
	}
	for page := 1; page <= len(ids); page++ {
		q := url.Values{"error_code": {"403"}, "error_message": {"budget_exceeded"}, "page_size": {"1"}, "page": {fmt.Sprint(page)}}
		rows := rowsOf(h.ok("GET", "/spend/logs/ui?"+q.Encode(), owner.session, nil), "data")
		if len(rows) != 1 {
			t.Fatalf("过滤后第%d页缺少结果", page)
		}
	}
}
