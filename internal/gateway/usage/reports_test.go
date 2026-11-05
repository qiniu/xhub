package usage

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestDetailErrorPreservesAuthorizedRawFailure 验证已记录的同步错误沿用原始响应，其他状态仅返回摘要。
// 覆盖取消、流式、过期、历史及空值；纯函数无需外部依赖或清理。
func TestDetailErrorPreservesAuthorizedRawFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event iam.UsageEvent
		body  *iam.RequestLog
		want  string
	}{
		{"saved", iam.UsageEvent{HTTPStatus: 403}, &iam.RequestLog{BodyCaptureStatus: "saved", ResponseBody: "raw error\n", Error: "summary"}, "raw error\n"},
		{"cancel", iam.UsageEvent{HTTPStatus: 499}, &iam.RequestLog{BodyCaptureStatus: "saved", ResponseBody: "partial stream", Error: "summary"}, "summary"},
		{"stream", iam.UsageEvent{HTTPStatus: 502, Streamed: true}, &iam.RequestLog{BodyCaptureStatus: "saved", ResponseBody: "partial stream", Error: "summary"}, "summary"},
		{"expired", iam.UsageEvent{HTTPStatus: 403}, &iam.RequestLog{BodyCaptureStatus: "expired", ResponseBody: "", Error: "summary"}, "summary"},
		{"empty", iam.UsageEvent{HTTPStatus: 403}, &iam.RequestLog{BodyCaptureStatus: "saved", Error: "summary"}, "summary"},
		{"legacy", iam.UsageEvent{HTTPStatus: 403}, &iam.RequestLog{Error: "legacy"}, "legacy"},
		{"missing", iam.UsageEvent{HTTPStatus: 403}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := detailError(tc.event, tc.body); got != tc.want {
				t.Fatalf("详情错误内容不正确: got=%q want=%q", got, tc.want)
			}
		})
	}
}

// TestDetailBodyOrMissing 区分真实正文、历史缺行和数据库故障；纯函数无需资源或清理。
func TestDetailBodyOrMissing(t *testing.T) {
	body := &iam.RequestLog{RequestID: "stored", ResponseBody: "answer"}
	got, err := detailBodyOrMissing("stored", body, nil)
	if err != nil || got != body {
		t.Fatal("已有正文不应被替换", got, err)
	}
	got, err = detailBodyOrMissing("history", nil, fmt.Errorf("wrapped: %w", iam.ErrNotFound))
	if err != nil || got == nil || got.RequestID != "history" || got.ResponseBody != "" {
		t.Fatal("历史缺行应保留空正文", got, err)
	}
	failure := errors.New("connection lost")
	got, err = detailBodyOrMissing("failed", nil, failure)
	if got != nil || !errors.Is(err, failure) {
		t.Fatal("存储失败不得伪装为空正文", got, err)
	}
}

// TestLogTimeAndValidation 验证日期、精确时间和时区边界，以及无效条件返回错误；纯请求解析测试，无外部依赖和清理。
func TestLogTimeAndValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		end   bool
		want  string
	}{
		{"2026-11-01", false, "2026-11-01T00:00:00Z"},
		{"2026-11-01", true, "2026-11-01T23:59:59.999999999Z"},
		{"2026-11-01 12:34:56", true, "2026-11-01T12:34:56Z"},
		{"2026-11-01T12:34:56+08:00", false, "2026-11-01T04:34:56Z"},
		{"2026-11-01T12:34:56.123Z", true, "2026-11-01T12:34:56.123Z"},
	} {
		parsed, err := logTime(tc.value, tc.end)
		if got := parsed.UTC().Format(time.RFC3339Nano); err != nil || got != tc.want {
			t.Errorf("时间 %q end=%t 得到 %s，期望 %s", tc.value, tc.end, got, tc.want)
		}
	}
	for _, query := range []string{"", "error_code=499&cache_hit_filter=miss", "start_date=2026-11-01&end_date=2026-11-01"} {
		if _, err := logQuery(httptest.NewRequest("GET", "/?"+query, nil), &authz.Scope{}); err != nil {
			t.Errorf("有效筛选 %s 被拒绝: %v", query, err)
		}
	}
	for _, query := range []string{"start_date=bad", "end_date=2026-02-30", "start_date=2026-11-02&end_date=2026-11-01", "error_code=99", "error_code=600", "error_code=4xx", "cache_hit_filter=true"} {
		if _, err := logQuery(httptest.NewRequest("GET", "/?"+query, nil), &authz.Scope{}); err == nil {
			t.Errorf("无效筛选 %s 未被拒绝", query)
		}
	}
}

// TestLogFilterMapping 验证筛选不混淆密钥 ID/Hash，并保留显式缓存未命中；纯函数无持久化和清理。
func TestLogFilterMapping(t *testing.T) {
	r := httptest.NewRequest("GET", "/?api_key=id&key_hash=hash&key_alias=alias&supplier=s&inbound_endpoint=/v1/responses&error_code=499&error_message=cancel&cache_hit_filter=miss", nil)
	q, err := logQuery(r, &authz.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if q.KeyID != "id" || q.KeyHash != "hash" || q.KeyAlias != "alias" || q.Supplier != "s" || q.InboundEndpoint != "/v1/responses" || q.ErrorCode != "499" || q.ErrorMessage != "cancel" || q.CacheHit == nil || *q.CacheHit {
		t.Fatalf("筛选映射不符合接口契约: %+v", q)
	}
	if got, err := logQuery(httptest.NewRequest("GET", "/", nil), &authz.Scope{}); err != nil || got.CacheHit != nil {
		t.Fatal("未指定缓存条件不应等同未命中")
	}
}

// TestTaskStatusFilters 验证日志查询接受任务阶段和旧失败筛选，未知状态不扩大筛选；纯函数测试无数据清理。
func TestTaskStatusFilters(t *testing.T) {
	// 普通日志由 non_error 明确指定，不能退化为不带条件的全部日志。
	got, err := logQuery(httptest.NewRequest("GET", "/spend/logs/ui?status_filter=non_error", nil), &authz.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "non_error" {
		t.Fatalf("普通日志分类丢失: %q", got.Status)
	}
	searched, err := logQuery(httptest.NewRequest("GET", "/spend/logs/ui?request_id=exact-id&search=partial-id", nil), &authz.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if searched.RequestID != "exact-id" || searched.Search != "partial-id" {
		t.Fatalf("请求 ID 筛选丢失: %+v", searched)
	}
	for _, tc := range []struct{ input, want string }{{"executing", "executing"}, {"polling", "polling"}, {"completed", "completed"}, {"success", "success"}, {"failed", "error"}, {"error", "error"}, {"unknown", ""}, {"", ""}} {
		raw := url.Values{"status_filter": {tc.input}}
		got, err := logQuery(httptest.NewRequest("GET", "/spend/logs/ui?"+raw.Encode(), nil), &authz.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tc.want {
			t.Fatalf("status filter %q = %q, want %q", tc.input, got.Status, tc.want)
		}
	}
}

func TestCollapseSessionsSeparatesCallersWithTheSameSessionID(t *testing.T) {
	rows := []map[string]any{
		{"request_id": "key-a-1", "session_id": "shared", "api_key": "key-a", "user": "user-1", "spend": 1.0, "total_tokens": 10},
		{"request_id": "key-b-1", "session_id": "shared", "api_key": "key-b", "user": "user-1", "spend": 2.0, "total_tokens": 20},
		{"request_id": "key-a-2", "session_id": "shared", "api_key": "key-a", "user": "user-1", "spend": 3.0, "total_tokens": 30},
		{"request_id": "user-2-1", "session_id": "shared", "api_key": "", "user": "user-2", "spend": 4.0, "total_tokens": 40},
	}

	got := collapseSessions(rows)
	if len(got) != 3 {
		t.Fatalf("collapsed rows = %d, want 3: %#v", len(got), got)
	}
	assertSessionTotals(t, got[0], 2, 4, 40)
	assertSessionTotals(t, got[1], 1, 2, 20)
	assertSessionTotals(t, got[2], 1, 4, 40)
}

func TestCollapseSessionsUsesUserForKeylessCalls(t *testing.T) {
	rows := []map[string]any{
		{"request_id": "u1", "session_id": "shared", "user": "user-1"},
		{"request_id": "u2", "session_id": "shared", "user": "user-2"},
	}

	got := collapseSessions(rows)
	if len(got) != 2 {
		t.Fatalf("collapsed rows = %d, want 2: %#v", len(got), got)
	}
}

func TestCollapseSessionsDoesNotJoinRowsWithoutCaller(t *testing.T) {
	rows := []map[string]any{
		{"request_id": "first", "session_id": "shared"},
		{"request_id": "second", "session_id": "shared"},
		{"session_id": "shared"},
		{"session_id": "shared"},
	}
	got := collapseSessions(rows)
	if len(got) != 4 {
		t.Fatalf("anonymous calls were merged: %#v", got)
	}
}

func assertSessionTotals(t *testing.T, row map[string]any, count int, spend float64, tokens int) {
	t.Helper()
	if row["session_total_count"] != count || row["session_total_spend"] != spend || row["session_total_tokens"] != tokens {
		t.Fatalf("session totals = count %#v spend %#v tokens %#v", row["session_total_count"], row["session_total_spend"], row["session_total_tokens"])
	}
}

// TestErrorRowFields 验证失败列表行提供前端约定的状态和 HTTP 状态。
// 前置条件是持久化事件带 error/502；结果应在 metadata.http_status 中原样返回；纯内存测试无需清理。
func TestErrorRowFields(t *testing.T) {
	rows := eventRows([]iam.UsageEvent{{RequestID: "failed", Status: "error", HTTPStatus: 502}})
	if len(rows) != 1 || rows[0]["status"] != "error" {
		t.Fatalf("失败状态字段错误: %#v", rows)
	}
	meta, _ := rows[0]["metadata"].(map[string]any)
	if meta["http_status"] != 502 {
		t.Fatalf("HTTP 状态未返回: %#v", meta)
	}
}

// TestJSONOrTextPreservesRawResponse 验证详情 response 对 JSON 解码、对纯文本原样返回。
// 前置条件是数据库可能存两种正文；结果不得清空纯文本；纯函数测试无需清理。
func TestJSONOrTextPreservesRawResponse(t *testing.T) {
	if got := jsonOrText("plain upstream error"); got != "plain upstream error" {
		t.Fatalf("纯文本响应被改写: %#v", got)
	}
	jsonBody, ok := jsonOrText("{\"error\":\"bad\"}").(map[string]any)
	if !ok || jsonBody["error"] != "bad" {
		t.Fatalf("JSON 响应未解码: %#v", jsonBody)
	}
	if got, ok := jsonOrText("  ").(map[string]any); !ok || len(got) != 0 {
		t.Fatalf("空响应边界错误: %#v", got)
	}
}

// TestLogPaginationQuery 验证排序方向、未知列传递及安全偏移边界；前置内存请求，无数据库数据需要清理。
// 排序列由 IAM 白名单处理，这里必须保留前端列名且只有 asc 启用升序。
func TestLogPaginationQuery(t *testing.T) {
	for _, c := range []struct {
		raw, field string
		asc        bool
	}{{"", "", false}, {"sort_by=spend&sort_order=asc", "spend", true}, {"sort_by=unknown&sort_order=broken", "unknown", false}} {
		q, err := logQuery(httptest.NewRequest("GET", "/?"+c.raw, nil), &authz.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if q.SortBy != c.field || q.SortAsc != c.asc {
			t.Fatalf("日志排序条件错误: %+v", q)
		}
	}
	for _, c := range []struct {
		raw  string
		want int
	}{{"", 0}, {"page=2", 5000}, {"page=-1", 0}, {"page=broken", 0}, {"page=9223372036854775807", int(^uint(0) >> 1)}} {
		if got := pageOffset(httptest.NewRequest("GET", "/?"+c.raw, nil), 5000); got != c.want {
			t.Fatalf("用量分页偏移%q: %d != %d", c.raw, got, c.want)
		}
	}
}

// TestLogTPS 使用账单投影验证日志列表/详情沿用输出公式；前置固定计时，覆盖正常流、同步、缓存和失败，无持久化清理。
func TestLogTPS(t *testing.T) {
	ttft := 100
	base := iam.UsageEvent{CallType: "responses", Status: "success", HTTPStatus: 200, Streamed: true, PromptTokens: 10000, CompletionTokens: 10, DurationMS: 1100, TTFTMs: &ttft}
	for _, tc := range []struct {
		name   string
		change func(*iam.UsageEvent)
		valid  bool
	}{
		{"stream", func(e *iam.UsageEvent) {}, true},
		{"sync", func(e *iam.UsageEvent) { e.Streamed = false }, false},
		{"cache", func(e *iam.UsageEvent) { e.CacheHit = true }, false},
		{"failed", func(e *iam.UsageEvent) { e.Status = "failed" }, false},
		{"zero output", func(e *iam.UsageEvent) { e.CompletionTokens = 0 }, false},
		{"invalid interval", func(e *iam.UsageEvent) { e.DurationMS = 100 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := base
			tc.change(&e)
			got := logTPS(e)
			if (got != nil) != tc.valid || got != nil && *got != 10 {
				t.Fatalf("日志输出 TPS 错误: %v", got)
			}
			row := eventRows([]iam.UsageEvent{e})[0]
			value := row["tps"].(*float64)
			if (value != nil) != tc.valid || value != nil && *value != 10 {
				t.Fatal("日志列表与详情 TPS 口径不一致", row)
			}
		})
	}
}

// TestLogErrorFilters 验证日志接口完整接收错误码与消息，非法值返回错误且不能退化成无条件查询。
// 前置内存请求，覆盖空值、组合、空白和SQL样式输入；无持久化副作用，不需清理。
func TestLogErrorFilters(t *testing.T) {
	for _, tc := range []struct{ code, message, want string }{
		{"", "", ""}, {"403", "budget_exceeded", "403"}, {" 429 ", "rate_limit", "429"},
		{"broken", "%_' OR true --", "broken"}, {"600", "Message With Spaces", "600"},
	} {
		raw := url.Values{"error_code": {tc.code}, "error_message": {tc.message}}
		q, err := logQuery(httptest.NewRequest("GET", "/spend/logs/ui?"+raw.Encode(), nil), &authz.Scope{})
		if tc.code == "broken" || tc.code == "600" {
			if err == nil {
				t.Fatalf("非法错误码 %q 未拒绝", tc.code)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if q.ErrorCode != tc.want || q.ErrorMessage != tc.message {
			t.Fatalf("错误筛选未原样进入查询: %+v", q)
		}
	}
}

// TestLogExactWindow 验证控制台 UTC 时间和带时区时间不丢失，日期结束仍包含整天；纯查询无持久化清理。
func TestLogExactWindow(t *testing.T) {
	for _, tc := range []struct{ from, to, wantFrom, wantTo string }{
		{"2026-10-29 02:52:00", "2026-10-30 02:52:00", "2026-10-29T02:52:00Z", "2026-10-30T02:52:00Z"},
		{"2026-10-29T10:52:00+08:00", "2026-10-30T10:52:00.123+08:00", "2026-10-29T02:52:00Z", "2026-10-30T02:52:00.123Z"},
		{"2026-10-29", "2026-10-30", "2026-10-29T00:00:00Z", "2026-10-30T23:59:59.999999999Z"},
	} {
		q, err := logQuery(httptest.NewRequest("GET", "/?"+url.Values{"start_date": {tc.from}, "end_date": {tc.to}}.Encode(), nil), &authz.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if q.From.UTC().Format(time.RFC3339Nano) != tc.wantFrom || q.To.UTC().Format(time.RFC3339Nano) != tc.wantTo {
			t.Errorf("时间条件丢失或范围扩大: %+v", q)
		}
	}
}

// TestLogInvalidWindow 验证非法、反向及空白时间拒绝查询；空边界仍兼容，纯函数无清理。
func TestLogInvalidWindow(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		bad      bool
	}{
		{"", "", false}, {"2026-10-30", "", false}, {"bad", "", true}, {"", "bad", true}, {" ", "", true}, {"2026-10-31", "2026-10-30", true},
	} {
		_, err := logQuery(httptest.NewRequest("GET", "/?"+url.Values{"start_date": {tc.from}, "end_date": {tc.to}}.Encode(), nil), &authz.Scope{})
		if (err != nil) != tc.bad {
			t.Errorf("无效时间窗口校验错误: %+v err=%v", tc, err)
		}
	}
}
