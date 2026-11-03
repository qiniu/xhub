package family

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAnalyticsAliasesRejects 用独立清单验证23条旧统计接口；nil存储证明拒绝发生在任何目录读写之前。
// 包含正常路径、单尾斜杠及私密输入；仅内存无清理资源。
func TestAnalyticsAliasesRejects(t *testing.T) {
	for _, route := range []string{
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
		"GET /user/spend/report",
		"POST /global/spend/refresh",
		"POST /global/spend/reset",
	} {
		for _, suffix := range []string{"", "/"} {
			t.Run(strings.ReplaceAll(route, "/", "|")+suffix, func(t *testing.T) {
				method, path, _ := strings.Cut(route, " ")
				w := httptest.NewRecorder()
				writeCatalogStore(nil, w, httptest.NewRequest(method, path+suffix, nil), map[string]any{"api_key": "private-fixture"})
				if w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"type":"unsupported_analytics_operation"`) || strings.Contains(w.Body.String(), "private-fixture") {
					t.Fatalf("统计占位入口应明确失败: %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

// TestAnalyticsAliasBoundaries 确认真实统计、IAM边界及错误方法/相似路径不被误拦；纯HTTP内存，无需清理。
func TestAnalyticsAliasBoundaries(t *testing.T) {
	for _, route := range []string{"GET /spend/logs/ui", "GET /spend/logs/v2", "GET /global/spend/models", "GET /global/spend/logs", "GET /user/daily/activity", "GET /management/v1/spend_logs/users", "GET /team/spend/report", "GET /organization/spend/report", "POST /team/t/member/u/reset_spend", "POST /global/spend", "GET /global/spend/reset", "GET /model/metrics/extra", "GET /global/spend//"} {
		method, path, _ := strings.Cut(route, " ")
		w := httptest.NewRecorder()
		if rejectAnalyticsAlias(w, httptest.NewRequest(method, path, nil)) || w.Body.Len() != 0 {
			t.Fatal("误拦截有效接口或扩大匹配", route)
		}
	}
	w := httptest.NewRecorder()
	writeCatalogStore(nil, w, httptest.NewRequest("POST", "/global/spend/reset", nil), map[string]any{"jsonrpc": "2.0"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_protocol") {
		t.Fatal("JSONRPC校验顺序改变", w.Body.String())
	}
}
