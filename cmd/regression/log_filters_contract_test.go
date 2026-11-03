package regression

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestLogFiltersMatchStoredEvidence 前置隔离 PostgreSQL、真实网关和本地上游；验证筛选在分页前生效、错误条件及权限不扩大，结束由 harness 删除 schema。
func TestLogFiltersMatchStoredEvidence(t *testing.T) {
	h := newHarness(t, chatDeployment("filter-contract"))
	admin := h.adminSession()
	owner := h.provision(t, admin, "filter-owner")
	other := h.provision(t, admin, "filter-other")
	r := h.ok(http.MethodPost, "/v1/chat/completions", owner.key, chatRequest("filter-contract", "Filter evidence"))
	h.flushSpend()
	if r.header("x-litellm-call-id") == "" {
		t.Fatal("真实请求没有关联 ID")
	}
	now := time.Now().UTC().Truncate(time.Second)
	rows := []iam.UsageRecord{
		{RequestID: "filter-a", UserID: owner.userID, Model: "historical-model", KeyHash: "hash-a", KeyAlias: "old-key", SessionID: "filter-session", Status: "error", HTTPStatus: 499, Error: "downstream canceled 100%_literal", Supplier: "supplier-a", InboundEndpoint: "/v1/responses", DurationMS: 60000, TS: now},
		{RequestID: "filter-b", UserID: owner.userID, Model: "historical-model", KeyHash: "hash-b", KeyAlias: "new-key", Status: "error", HTTPStatus: 502, Error: "upstream overloaded", TS: now.Add(-2 * time.Hour)},
		{RequestID: "filter-c", UserID: other.userID, Model: "historical-model", KeyHash: "hash-a", KeyAlias: "old-key", Status: "error", HTTPStatus: 499, Error: "downstream canceled 100%_literal", TS: now},
		{RequestID: "filter-cache", UserID: owner.userID, Model: "historical-model", Status: "success", HTTPStatus: 200, CacheHit: true, TS: now},
	}
	if err := h.db.RecordUsage(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query string
		want  int
	}{
		{"error_code=499", 2}, {"error_code=404", 0}, {"error_message=" + url.QueryEscape("100%_literal"), 2},
		{"key_alias=old-key", 2}, {"key_hash=hash-b", 1}, {"cache_hit_filter=hit", 1},
		{"supplier=supplier-a&inbound_endpoint=" + url.QueryEscape("/v1/responses"), 1},
		{"start_date=" + url.QueryEscape(now.Add(-time.Minute).Format("2006-01-02 15:04:05")), 3},
		{"end_date=" + url.QueryEscape(now.Add(-time.Minute).Format(time.RFC3339)), 1},
		{"error_code=499&key_alias=new-key", 0},
		{"cache_hit_filter=miss", 3},
		{"error_message=" + url.QueryEscape("CANCELED 100%_literal") + "&group_by_session=true", 2},
		{"key_alias=old-key&error_code=499&start_date=" + url.QueryEscape(now.Format(time.RFC3339)) + "&end_date=" + url.QueryEscape(now.Format(time.RFC3339)), 2},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			body := h.ok(http.MethodGet, "/spend/logs/ui?model=historical-model&page_size=1&"+c.query, admin, nil).json()
			if got := numberOrZero(body["total"]); got != float64(c.want) {
				t.Fatalf("筛选 %s total=%v，期望 %d", c.query, got, c.want)
			}
			data, ok := body["data"].([]any)
			if !ok || len(data) > 1 || (c.want > 0 && len(data) != 1) {
				t.Fatalf("筛选后分页结果错误: %v", body)
			}
		})
	}
	scoped := h.ok(http.MethodGet, "/spend/logs/ui?model=historical-model&error_code=499", owner.session, nil).json()
	if numberOrZero(scoped["total"]) != 1 {
		t.Fatalf("普通用户筛选范围错误: %v", scoped)
	}
	if got := h.do(http.MethodGet, "/spend/logs/ui/filter-c", owner.session, nil).status; got != 404 {
		t.Fatalf("他人详情应隐藏，得到 %d", got)
	}
	// 详情链接不传列表日期条件，历史记录仍能按权限读取。
	if got := h.ok(http.MethodGet, "/spend/logs/ui/filter-b", owner.session, nil).json()["request_id"]; got != "filter-b" {
		t.Fatalf("历史详情请求不一致: %v", got)
	}
	for _, q := range []string{"start_date=broken", "error_code=oops", "cache_hit_filter=unknown"} {
		if got := h.do(http.MethodGet, "/spend/logs/ui?"+q, admin, nil).status; got != 400 {
			t.Fatalf("无效筛选 %s 返回 %d", q, got)
		}
	}
}
