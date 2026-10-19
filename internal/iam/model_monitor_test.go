package iam

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
	"xorm.io/builder"
)

// TestMonitorContribution 验证正常流、同步、缓存、失败和计时边界的独立 TPS/TTFT 样本及可逆贡献，无外部清理。
func TestMonitorContribution(t *testing.T) {
	ttft := 100
	base := UsageRecord{TS: time.Date(2026, 1, 1, 0, 3, 0, 0, time.UTC), Supplier: "s", UpstreamModel: "m", Status: "success", CallType: "chat", HTTPStatus: 200, PromptTokens: 20, CompletionTokens: 10, Cost: 0.2, DurationMS: 1100, TTFTMs: &ttft, Streamed: true}
	for _, tc := range []struct {
		name                string
		change              func(*UsageRecord)
		tps, ttft, requests int64
	}{
		{"stream", func(r *UsageRecord) {}, 1, 1, 1},
		{"native-completions", func(r *UsageRecord) { r.CallType = "openai_completions:create" }, 1, 1, 1},
		{"sync", func(r *UsageRecord) { r.Streamed = false }, 0, 1, 1},
		{"cache", func(r *UsageRecord) { r.CacheHit = true }, 0, 0, 1},
		{"failed", func(r *UsageRecord) { r.HTTPStatus = 502 }, 0, 0, 1},
		{"negative", func(r *UsageRecord) { n := -1; r.TTFTMs = &n }, 0, 0, 1},
		{"inverted", func(r *UsageRecord) { r.DurationMS = 99 }, 0, 0, 1},
		{"zero interval", func(r *UsageRecord) { r.DurationMS = 100 }, 0, 1, 1},
		{"no output", func(r *UsageRecord) { r.CompletionTokens = 0 }, 0, 1, 1},
		{"polling", func(r *UsageRecord) { r.Status = "polling" }, 0, 0, 0},
		{"image", func(r *UsageRecord) { r.CallType = "image" }, 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.change(&r)
			m := map[monitorKey]monitorDelta{}
			accumulateMonitor(m, r, 1)
			for k, d := range m {
				if k.Bucket.Minute() != 0 || k.Supplier != "s" || k.Model != "m" {
					t.Fatal("桶维度丢失", k)
				}
				if d.Requests != tc.requests || d.TPSSamples != tc.tps || d.TTFTSamples != tc.ttft {
					t.Fatalf("样本筛选错误: %+v", d)
				}
				if tc.tps == 1 && d.TPS != 10 {
					t.Fatal("输入 Token 被错误计入输出 TPS", d)
				}
				if tc.ttft == 1 && d.TTFT != 100 {
					t.Fatal("首字节计时被改变", d)
				}
			}
			accumulateMonitor(m, r, -1)
			for _, d := range m {
				if d != (monitorDelta{}) {
					t.Fatal("贡献不能抵消", d)
				}
			}
		})
	}
}

// monitorRows 供存储测试读取固定日期的授权窗口；返回监控行，失败终止测试，无写入。
func monitorRows(t *testing.T, db *DB, cond builder.Cond) []ModelMonitorRow {
	t.Helper()
	rows, err := db.ModelMonitor(context.Background(), ModelMonitorQuery{UsageQuery: UsageQuery{Cond: cond, From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestMonitorAtomicIdempotentBuckets 在私有数据库验证并发去重、连接隔离、授权及回滚；testDB 清理全部数据。
func TestMonitorAtomicIdempotentBuckets(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ttft := 100
	r := UsageRecord{RequestID: "monitor-1", TS: time.Date(2026, 1, 1, 0, 3, 0, 0, time.UTC), OwnerType: OwnerPersonal, UserID: "owner-a", Model: "alias", Supplier: "a", UpstreamModel: "actual", Status: "success", CallType: "chat", HTTPStatus: 200, PromptTokens: 20, CompletionTokens: 10, Cost: 0.2, DurationMS: 1100, TTFTMs: &ttft, Streamed: true}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.RecordUsage(ctx, []UsageRecord{r, r}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	second := r
	second.RequestID = "monitor-2"
	second.DurationMS = 600
	other := r
	other.RequestID = "monitor-other"
	other.Supplier = "b"
	other.UserID = "owner-b"
	if err := db.RecordUsage(ctx, []UsageRecord{second, other}); err != nil {
		t.Fatal(err)
	}
	bad := r
	bad.RequestID = "bad"
	bad.OwnerType = "invalid"
	good := r
	good.RequestID = "rollback"
	if err := db.RecordUsage(ctx, []UsageRecord{good, bad}); err == nil {
		t.Fatal("无效归属必须回滚")
	}
	// 删除原始明细仍应保持报表完整，证明读路径只依赖预聚合。
	s := db.session(ctx)
	defer s.Close()
	if _, err := s.Exec("DELETE FROM request_logs"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec("DELETE FROM usage_events"); err != nil {
		t.Fatal(err)
	}
	for _, row := range monitorRows(t, db, builder.Eq{"user_id": "owner-a"}) {
		if row.Requests != 2 || row.TotalTokens != 60 || row.Cost != 0.4 || row.AvgTPS == nil || *row.AvgTPS != 15 || row.TPSSamples != 2 {
			t.Fatalf("预聚合、授权或平均错误: %+v", row)
		}
	}
	models := 0
	for _, row := range monitorRows(t, db, builder.NewCond()) {
		if row.Kind == "model" {
			models++
		}
	}
	if models != 2 {
		t.Fatal("同模型必须按连接分开", models)
	}
	if _, err := db.ModelMonitor(ctx, ModelMonitorQuery{}, 0); err == nil {
		t.Fatal("缺少授权不得查询")
	}
}

// TestMonitorTaskCorrection 验证进行中排除、失败纠正、延迟结算和重放；私有 schema 自动清理。
func TestMonitorTaskCorrection(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := UsageRecord{RequestID: "monitor-task", TS: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), OwnerType: OwnerPersonal, UserID: "owner", Model: "alias", Supplier: "supplier", UpstreamModel: "actual", Status: "executing", HTTPStatus: 200, CallType: "video"}
	if err := db.RecordTaskUsage(ctx, r, true, false); err != nil {
		t.Fatal(err)
	}
	for _, row := range monitorRows(t, db, builder.NewCond()) {
		if row.Requests != 0 {
			t.Fatal("进行中被计数", row)
		}
	}
	for i, state := range []string{"failed", "completed", "completed", "completed"} {
		r.Status = state
		r.Supplier = "changed"
		r.UpstreamModel = "changed"
		r.Cost = 0.5
		r.PromptTokens = 2
		r.CompletionTokens = 3
		r.DurationMS = 1000
		if err := db.RecordTaskUsage(ctx, r, false, i >= 2); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range monitorRows(t, db, builder.NewCond()) {
		if row.Requests != 1 || row.FailedRequests != 0 || row.TotalTokens != 5 || row.Cost != 0.5 {
			t.Fatal("任务修正错误", row)
		}
		if row.Kind == "model" && (row.Supplier != "supplier" || row.Model != "actual") {
			t.Fatal("轮询重写归属", row)
		}
	}
}

// TestMonitorQuarterHourTimezone 验证 UTC+5:45 本地日半开边界；私有 schema 自动清理。
func TestMonitorQuarterHourTimezone(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 18, 15, 0, 0, time.UTC)
	for i, offset := range []time.Duration{-time.Microsecond, 0, 24*time.Hour - time.Microsecond, 24 * time.Hour} {
		r := UsageRecord{RequestID: fmt.Sprint(i), TS: start.Add(offset), Status: "success", Supplier: "s", UpstreamModel: "m"}
		if err := db.RecordUsage(ctx, []UsageRecord{r}); err != nil {
			t.Fatal(err)
		}
	}
	q := ModelMonitorQuery{UsageQuery: UsageQuery{Cond: builder.NewCond(), From: start, To: start.Add(24 * time.Hour)}}
	rows, err := db.ModelMonitor(ctx, q, 345)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Requests != 2 || (row.Kind == "trend" && row.Day != "2026-01-02") {
			t.Fatal("本地日期边界错误", row)
		}
	}
	for _, tz := range []int{1, 900} {
		if _, err := db.ModelMonitor(ctx, q, tz); err == nil {
			t.Fatal("非法时区被接受", tz)
		}
	}
}

// TestMonitorBatchCompression 在私有 schema 写入 5000 条同维度账单，验证一次桶累加和精确汇总；testDB 清理全部记录。
func TestMonitorBatchCompression(t *testing.T) {
	db := testDB(t)
	records := make([]UsageRecord, 5000)
	for i := range records {
		records[i] = UsageRecord{RequestID: fmt.Sprintf("batch-%d", i), TS: time.Date(2026, 1, 1, 0, 3, 0, 0, time.UTC), Supplier: "batch", UpstreamModel: "actual", Status: "success", PromptTokens: 2, CompletionTokens: 3, Cost: 0.125}
	}
	begin := time.Now()
	if err := db.RecordUsage(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(begin)
	s := db.session(context.Background())
	defer s.Close()
	n, err := s.Table("model_monitor_buckets").Count()
	if err != nil || n != 1 {
		t.Fatalf("5000 条账单应压缩为一桶: %d, %v", n, err)
	}
	begin = time.Now()
	rows := monitorRows(t, db, builder.NewCond())
	for _, r := range rows {
		if r.Requests != 5000 || r.TotalTokens != 25000 || r.Cost != 625 {
			t.Fatalf("批次统计不准确: %+v", r)
		}
	}
	t.Logf("5000 events -> %d bucket; transactional ingestion %v; aggregate query %v", n, elapsed, time.Since(begin))
}
