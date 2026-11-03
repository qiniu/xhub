package regression

import (
	"net/url"
	"testing"
	"time"
)

// TestLogExactTimeWindow 由真实HTTP生成失败记录，校验精确时间筛选、总数、会话查询和身份隔离。
// 前置私有PG及本地上游；非法窗口必须400，日期格式保持兼容；harness回收所有日志和用户。
func TestLogExactTimeWindow(t *testing.T) {
	h := newHarness(t, chatDeployment("window-check"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "window-owner")
	other := h.openScope(t, admin, "window-other")
	h.setKeyBudget(t, admin, owner.key, 0)
	before := time.Now().Add(-time.Second).UTC()
	failed := h.do("POST", "/v1/chat/completions", owner.key, chatRequest("window-check", "Check time filtering."))
	if failed.status != 403 {
		t.Fatal(failed.describe())
	}
	id := failed.header("x-litellm-call-id")
	logDetail(t, h, owner.session, id)
	after := time.Now().Add(time.Second).UTC()
	for _, tc := range []struct {
		from, to string
		count    int
		bad      bool
	}{
		{before.Format("2006-01-02 15:04:05"), after.Format("2006-01-02 15:04:05"), 1, false},
		{before.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano), 1, false},
		{before.Add(-time.Hour).Format(time.RFC3339Nano), before.Format(time.RFC3339Nano), 0, false},
		{after.Format(time.RFC3339Nano), after.Add(time.Hour).Format(time.RFC3339Nano), 0, false},
		{before.Format("2006-01-02"), after.Format("2006-01-02"), 1, false},
		{"bad", "", 0, true}, {"", "bad", 0, true}, {after.Format(time.RFC3339), before.Format(time.RFC3339), 0, true},
	} {
		q := url.Values{"start_date": {tc.from}, "end_date": {tc.to}, "request_id": {id}, "status_filter": {"error"}}
		for _, group := range []string{"false", "true"} {
			q.Set("group_by_session", group)
			r := h.do("GET", "/spend/logs/ui?"+q.Encode(), owner.session, nil)
			if tc.bad {
				if r.status != 400 {
					t.Fatalf("非法时间扩大查询: %s", r.describe())
				}
				continue
			}
			if r.status != 200 || asIntAny(r.json()["total"]) != tc.count || len(rowsOf(r, "data")) != tc.count {
				t.Fatalf("时间筛选错误 %+v: %s", tc, r.describe())
			}
			hidden := h.ok("GET", "/spend/logs/ui?"+q.Encode(), other.session, nil)
			if len(rowsOf(hidden, "data")) != 0 {
				t.Fatal("时间筛选泄漏其他用户日志")
			}
		}
	}
}
