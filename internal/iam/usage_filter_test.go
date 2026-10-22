package iam

import (
	"context"
	"testing"
)

// TestUsageLogCategories 验证日志分类在数据库分页前执行且普通日志和错误日志互补。
// 参数 t 为测试上下文；前置私有数据库，覆盖任务各阶段、旧失败状态、HTTP 400 边界与空状态；
// 查询结果应互不重叠并覆盖全部记录，失败状态不能进入成功筛选；schema 随测试自动清理。
func TestUsageLogCategories(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		id, status string
		code       int
	}{
		{"success", "success", 200}, {"completed", "completed", 200},
		{"executing", "executing", 200}, {"polling", "polling", 200},
		{"redirect", "success", 399}, {"empty", "", 0},
		{"error", "error", 502}, {"failed", "failed", 200}, {"failure", "failure", 200},
		{"http-error", "success", 400},
	} {
		r := usageRecord(tc.id, "", 0)
		r.Status, r.HTTPStatus = tc.status, tc.code
		if err := db.RecordUsage(ctx, []UsageRecord{r}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		filter string
		count  int
	}{{"non_error", 6}, {"error", 4}, {"failed", 4}, {"success", 4}, {"polling", 1}, {"missing", 0}} {
		rows, err := db.ListUsage(ctx, UsageQuery{Status: tc.filter})
		if err != nil || len(rows) != tc.count {
			t.Fatalf("分类 %s 应有 %d 条，实际 %d，错误 %v", tc.filter, tc.count, len(rows), err)
		}
		for _, row := range rows {
			failed := row.Status == "error" || row.Status == "failed" || row.Status == "failure" || row.HTTPStatus >= 400
			if (tc.filter == "non_error" || tc.filter == "success") && failed {
				t.Fatalf("失败请求进入普通日志: %+v", row)
			}
			if (tc.filter == "error" || tc.filter == "failed") && !failed {
				t.Fatalf("非失败请求进入错误日志: %+v", row)
			}
		}
	}
	rows, err := db.ListUsage(ctx, UsageQuery{Status: "non_error", Limit: 2, Offset: 2})
	if err != nil || len(rows) != 2 {
		t.Fatalf("分类后分页应保持满页: rows=%d err=%v", len(rows), err)
	}
	for _, tc := range []struct {
		q     UsageQuery
		count int
	}{
		{UsageQuery{Status: "non_error", Search: "error"}, 0},
		{UsageQuery{Status: "error", Search: "http-error"}, 1},
		{UsageQuery{RequestID: "success"}, 1},
		{UsageQuery{Search: "%"}, 0}, {UsageQuery{Search: "_"}, 0},
		{UsageQuery{Search: "' OR true --"}, 0},
	} {
		found, err := db.ListUsage(ctx, tc.q)
		if err != nil || len(found) != tc.count {
			t.Fatalf("请求 ID 搜索 %+v 应有 %d 条，实际 %d，错误 %v", tc.q, tc.count, len(found), err)
		}
	}
}

// TestUsageErrorFilters 验证最终状态和完整错误正文在分页、计数及会话聚合前共同收窄。
// 前置私有数据库，覆盖字面通配符、非法码、缺少详情、零匹配和身份隔离；testDB清理schema。
func TestUsageErrorFilters(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		id, user, message string
		code              int
	}{
		{"budget-a", "owner", "budget_exceeded %_", 403}, {"budget-b", "owner", "budget_exceeded second", 403},
		{"rate", "owner", "rate_limit", 429}, {"foreign", "other", "budget_exceeded %_", 403},
		{"missing", "owner", "", 402},
	} {
		r := usageRecord(tc.id, "", 0)
		r.UserID, r.SessionID, r.Status, r.HTTPStatus, r.Error = tc.user, "shared", "error", tc.code, tc.message
		if err := db.RecordUsage(ctx, []UsageRecord{r}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Engine.Exec("DELETE FROM request_logs WHERE request_id = ?", "missing"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		code, message string
		count         int
	}{
		{"", "", 4}, {"403", "", 2}, {"429", "", 1}, {"402", "", 1}, {"403", "budget_exceeded", 2},
		{"403", "%_", 1}, {"429", "budget_exceeded", 0}, {"", "no-such-error", 0},
		{"", "' OR true --", 0}, {"invalid", "", 0}, {"99", "", 0}, {"600", "", 0}, {"403 OR true", "", 0},
	} {
		q := UsageQuery{UserID: "owner", ErrorCode: tc.code, ErrorMessage: tc.message, Limit: 1}
		count, err := db.CountUsage(ctx, q)
		if err != nil || count != int64(tc.count) {
			t.Fatalf("筛选%+v计数错误: %d %v", tc, count, err)
		}
		rows, err := db.ListUsage(ctx, q)
		want := min(tc.count, 1)
		if err != nil || len(rows) != want {
			t.Fatalf("筛选%+v分页错误: %d %v", tc, len(rows), err)
		}
		groups, total, err := db.ListUsageSessions(ctx, q)
		if err != nil || len(groups) != want || total != int64(want) {
			t.Fatalf("会话筛选%+v错误: %d %d %v", tc, len(groups), total, err)
		}
		if want > 0 && groups[0].SessionCount != int64(tc.count) {
			t.Fatalf("会话统计未收窄: %+v", groups)
		}
	}
	q := UsageQuery{UserID: "owner", ErrorCode: "403", Limit: 1, Offset: 1}
	rows, err := db.ListUsage(ctx, q)
	if err != nil || len(rows) != 1 {
		t.Fatalf("第二页遗漏匹配记录: %v %v", rows, err)
	}
}
