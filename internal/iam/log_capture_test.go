package iam

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLogCapturePreferencesAndExpiry 在隔离 PostgreSQL 中验证默认关闭、个人开关及到期清理。
// 测试只使用合成正文，确认到期后正文不可读取且未到期记录保留；私有 schema 在测试结束后清理。
func TestLogCapturePreferencesAndExpiry(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	u, err := db.CreateUser(ctx, Actor{Kind: "system"}, UserInput{Email: "capture@example.test", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := db.PersonalLogCaptureEnabled(ctx, u.ID); err != nil || enabled {
		t.Fatalf("默认应关闭: enabled=%v err=%v", enabled, err)
	}
	if err := db.SetPersonalLogCapture(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if enabled, err := db.PersonalLogCaptureEnabled(ctx, u.ID); err != nil || !enabled {
		t.Fatalf("个人开关未生效: enabled=%v err=%v", enabled, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	capture := LogCapture{RequestID: "old", UserID: u.ID, Source: "personal", CreatedAt: now.Add(-31 * 24 * time.Hour), ExpiresAt: now.Add(-24 * time.Hour), Request: `{"body":{"messages":[{"content":"synthetic"}]}}`, Response: `{"text":"synthetic answer"}`}
	state, err := db.SaveLogCapture(ctx, capture, CaptureLimits{PerRequest: 4096, PerUser: 8192, Global: 16384}, now.Add(-32*24*time.Hour))
	if err != nil || state != "saved" {
		t.Fatalf("保存失败: state=%s err=%v", state, err)
	}
	capture.RequestID, capture.ExpiresAt = "new", now.Add(time.Hour)
	state, err = db.SaveLogCapture(ctx, capture, CaptureLimits{PerRequest: 4096, PerUser: 8192, Global: 16384}, now)
	if err != nil || state != "saved" {
		t.Fatalf("保存新正文失败: state=%s err=%v", state, err)
	}
	if _, err := db.GetLogCapture(ctx, "old", now); err != ErrNotFound {
		t.Fatalf("到期正文仍可读取: %v", err)
	}
	deleted, err := db.PurgeExpiredLogCaptures(ctx, now, 100)
	if err != nil || deleted != 1 {
		t.Fatalf("到期清理错误: count=%d err=%v", deleted, err)
	}
	if got, err := db.GetLogCapture(ctx, "new", now); err != nil || !strings.Contains(got.Request, "synthetic") {
		t.Fatalf("未到期正文丢失: %+v err=%v", got, err)
	}
	if err := db.SetPersonalLogCapture(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := db.PersonalLogCaptureEnabled(ctx, u.ID); err != nil || enabled {
		t.Fatalf("关闭未生效: enabled=%v err=%v", enabled, err)
	}
}

// TestLogCaptureLimitsAreAtomic 并发写入合成正文，验证单条、个人和全局容量均不能超出上限。
// 前置独立 PostgreSQL；只检查结果与计数，测试结束自动删除 schema。
func TestLogCaptureLimitsAreAtomic(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	limits := CaptureLimits{PerRequest: 6, PerUser: 10, Global: 10}
	var wg sync.WaitGroup
	results := make(chan string, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := LogCapture{RequestID: string(rune('a' + i)), UserID: "owner", Source: "personal", CreatedAt: now, ExpiresAt: now.Add(time.Hour), Request: "12345"}
			state, err := db.SaveLogCapture(ctx, c, limits, now)
			if err != nil {
				t.Error(err)
				return
			}
			results <- state
		}(i)
	}
	wg.Wait()
	close(results)
	saved := 0
	for state := range results {
		if state == "saved" {
			saved++
		} else if state != "capacity" {
			t.Fatalf("意外状态: %s", state)
		}
	}
	if saved != 2 {
		t.Fatalf("并发容量限制未生效: saved=%d", saved)
	}
	state, err := db.SaveLogCapture(ctx, LogCapture{RequestID: "large", UserID: "other", Source: "personal", CreatedAt: now, ExpiresAt: now.Add(time.Hour), Request: "1234567"}, limits, now)
	if err != nil || state != "too_large" {
		t.Fatalf("单条上限未生效: %s %v", state, err)
	}
	deleted, err := db.PurgeExpiredLogCaptures(ctx, now.Add(2*time.Hour), 100)
	if err != nil || deleted != 2 {
		t.Fatalf("清理后未释放容量: %d %v", deleted, err)
	}
	state, err = db.SaveLogCapture(ctx, LogCapture{RequestID: "after", UserID: "owner", Source: "personal", CreatedAt: now, ExpiresAt: now.Add(3 * time.Hour), Request: "12345"}, limits, now.Add(2*time.Hour))
	if err != nil || state != "saved" {
		t.Fatalf("清理后不能重新写入: %s %v", state, err)
	}
}

// TestRequestMessagesRestoresStoredBody 用合成 JSON 验证无重复存储时的兼容重建及非法输入。
// 纯内存测试，不触及真实请求和外部资源。
func TestRequestMessagesRestoresStoredBody(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{`{"body":{"messages":[{"role":"user","content":"Hello"}],"model":"test"}}`, `[{"role":"user","content":"Hello"}]`},
		{`{"body":{"input":"Hello"}}`, `{"input":"Hello"}`}, {`{"body":null}`, ""}, {`invalid`, ""},
	} {
		if got := requestMessages(tc.doc); got != tc.want {
			t.Fatalf("重建消息错误: got=%s want=%s", got, tc.want)
		}
	}
}

// TestCaptureCleanupRepairsQuotaMismatch 在隔离数据库模拟占用计数损坏，验证过期清理和占用修复能继续。
// 测试只写入合成内容，核对未到期正文与个人/全局计数；私有 schema 自动清理。
func TestCaptureCleanupRepairsQuotaMismatch(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	limits := CaptureLimits{PerRequest: 1024, PerUser: 2048, Global: 4096}
	for _, item := range []struct {
		id      string
		expires time.Time
	}{{"expired", now.Add(time.Hour)}, {"active", now.Add(3 * time.Hour)}} {
		state, err := db.SaveLogCapture(t.Context(), LogCapture{RequestID: item.id, UserID: "owner", Source: "personal", CreatedAt: now, ExpiresAt: item.expires, Request: "synthetic"}, limits, now)
		if err != nil || state != "saved" {
			t.Fatalf("前置正文写入失败: %s %v", state, err)
		}
	}
	if _, err := db.Engine.Exec(`UPDATE request_log_capture_quotas SET size_bytes = 0 WHERE scope IN ('global', 'user:owner')`); err != nil {
		t.Fatal(err)
	}
	if count, err := db.PurgeExpiredLogCaptures(t.Context(), now.Add(2*time.Hour), 100); err != nil || count != 1 {
		t.Fatalf("计数异常阻塞过期清理: count=%d err=%v", count, err)
	}
	if _, err := db.GetLogCapture(t.Context(), "expired", now); err != ErrNotFound {
		t.Fatalf("过期正文仍未删除: %v", err)
	}
	if got, err := db.GetLogCapture(t.Context(), "active", now); err != nil || got.Request != "synthetic" {
		t.Fatalf("未过期正文丢失: %+v %v", got, err)
	}
	for _, scope := range []string{"global", "user:owner"} {
		var row struct {
			SizeBytes int64 `xorm:"'size_bytes'"`
		}
		if ok, err := db.Engine.SQL(`SELECT size_bytes FROM request_log_capture_quotas WHERE scope = ?`, scope).Get(&row); err != nil || !ok || row.SizeBytes != int64(len("synthetic")) {
			t.Fatalf("占用计数未修复 scope=%s row=%+v err=%v", scope, row, err)
		}
	}
	session := db.Engine.NewSession()
	defer session.Close()
	if err := reconcileCaptureQuota(session, "unknown"); err != ErrInvalid {
		t.Fatalf("非法占用范围未拒绝: %v", err)
	}
}

// TestCaptureCleanupRollsBackWhenRepairFails 验证计数修复遇数据库错误时仍回滚正文删除。
// 在隔离库注入仅供测试的约束，确认未清理记录仍可读取；测试结束自动删除 schema。
func TestCaptureCleanupRollsBackWhenRepairFails(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	limits := CaptureLimits{PerRequest: 1024, PerUser: 2048, Global: 4096}
	for _, item := range []struct {
		id      string
		expires time.Time
	}{{"expired", now.Add(time.Hour)}, {"active", now.Add(3 * time.Hour)}} {
		state, err := db.SaveLogCapture(t.Context(), LogCapture{RequestID: item.id, UserID: "owner", Source: "personal", CreatedAt: now, ExpiresAt: item.expires, Request: "synthetic"}, limits, now)
		if err != nil || state != "saved" {
			t.Fatalf("前置正文写入失败: %s %v", state, err)
		}
	}
	if _, err := db.Engine.Exec(`UPDATE request_log_capture_quotas SET size_bytes = 0 WHERE scope = 'user:owner'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.Exec(`ALTER TABLE request_log_capture_quotas ADD CONSTRAINT reject_quota_repair CHECK (scope <> 'user:owner' OR size_bytes = 0)`); err != nil {
		t.Fatal(err)
	}
	if count, err := db.PurgeExpiredLogCaptures(t.Context(), now.Add(2*time.Hour), 100); err == nil || count != 0 {
		t.Fatalf("修复失败被当成清理成功: count=%d err=%v", count, err)
	}
	if got, err := db.GetLogCapture(t.Context(), "expired", now); err != nil || got.Request != "synthetic" {
		t.Fatalf("修复失败未回滚正文删除: %+v %v", got, err)
	}
}
