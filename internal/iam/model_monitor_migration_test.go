package iam

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"xorm.io/builder"
)

// TestMonitorTPSUpgrade 验证真实启动升级修正旧指标，覆盖全部文本操作、同步/失败/缓存/零计时排除及明细已清理的桶。
// 前置私有旧指标与账单；验证仅 TPS 改变、重复升级与重放幂等，testDB 自动关闭连接并回收 schema。
func TestMonitorTPSUpgrade(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	ttft := 100
	base := UsageRecord{TS: time.Date(2026, 1, 1, 0, 3, 0, 0, time.UTC), OwnerType: OwnerPersonal, UserID: "owner", Supplier: "supplier", UpstreamModel: "model", Status: "success", CallType: "responses", HTTPStatus: 200, Streamed: true, PromptTokens: 10000, CompletionTokens: 10, Cost: 0.25, DurationMS: 1100, TTFTMs: &ttft}
	records := []UsageRecord{}
	for _, call := range textTimingCalls {
		r := base
		r.RequestID, r.CallType = call, call
		records = append(records, r)
	}
	for _, kind := range []string{"sync", "cache", "failed", "zero", "missing", "image"} {
		r := base
		r.RequestID = kind
		switch kind {
		case "sync":
			r.Streamed = false
		case "cache":
			r.CacheHit = true
		case "failed":
			r.Status, r.HTTPStatus = "failed", 502
		case "zero":
			r.DurationMS = 100
		case "missing":
			r.TTFTMs = nil
		case "image":
			r.CallType = "image"
		}
		records = append(records, r)
	}
	orphan := base
	orphan.RequestID, orphan.UpstreamModel = "purged", "purged-model"
	records = append(records, orphan)
	if err := db.RecordUsage(ctx, records); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.Exec("DELETE FROM usage_events WHERE request_id='purged'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.Exec("UPDATE model_monitor_buckets SET tps_sum=999999,tps_samples=77"); err != nil {
		t.Fatal(err)
	}
	before, err := db.Engine.QueryString("SELECT requests,failed_requests,prompt_tokens,completion_tokens,cost,ttft_sum,ttft_samples,latency_sum,latency_samples FROM model_monitor_buckets ORDER BY model")
	if err != nil {
		t.Fatal(err)
	}
	// 删除版本表模拟尚未引入该修复的旧库；必须由生产 Migrate 创建并完成修正。
	if _, err := db.Engine.Exec("DROP TABLE model_monitor_migrations; ALTER TABLE model_monitor_buckets DROP COLUMN output_tps_sum, DROP COLUMN output_tps_samples"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.RecordUsage(ctx, records[:len(records)-1]); err != nil {
			t.Fatal(err)
		}
	}
	after, err := db.Engine.QueryString("SELECT requests,failed_requests,prompt_tokens,completion_tokens,cost,ttft_sum,ttft_samples,latency_sum,latency_samples FROM model_monitor_buckets ORDER BY model")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("修正 TPS 不得重算账单或其他指标: before=%v after=%v err=%v", before, after, err)
	}
	for _, row := range monitorRows(t, db, builder.NewCond()) {
		if row.Model == "purged-model" {
			if row.AvgTPS != nil || row.TPSSamples != 0 || row.Requests != 1 {
				t.Fatal("无法还原原始事件的历史桶必须显示无 TPS 样本", row)
			}
		} else if row.AvgTPS == nil || *row.AvgTPS != 10 || row.TPSSamples != int64(len(textTimingCalls)) {
			t.Fatal("升级公式、有效样本或原子聚合错误", row)
		}
	}
	// 模拟仍运行的旧版本追加旧列；新指标不能重新混入总 Token 或同步口径。
	if _, err := db.Engine.Exec("UPDATE model_monitor_buckets SET tps_sum=tps_sum+123456,tps_samples=tps_samples+1"); err != nil {
		t.Fatal(err)
	}
	for _, row := range monitorRows(t, db, builder.NewCond()) {
		if row.Model != "purged-model" && (row.AvgTPS == nil || *row.AvgTPS != 10) {
			t.Fatal("旧口径写入污染了输出 TPS", row)
		}
	}
	markers, err := db.Engine.QueryString("SELECT name FROM model_monitor_migrations")
	if err != nil || len(markers) != 1 || markers[0]["name"] != outputTPSMigration {
		t.Fatalf("迁移标记必须唯一且幂等: %v %v", markers, err)
	}
	var count int64
	count, err = db.Engine.Table("usage_events").Count()
	if err != nil || count != int64(len(records)-1) {
		t.Fatalf("升级或重放改变了历史事件数量: %d %v", count, err)
	}
}

// TestMonitorTPSUpgradeFailureAndConcurrency 验证修正失败完整回滚，恢复后并发升级与写入不丢样本、不重复记账。
// 前置私有数据库与拒绝更新的测试约束；验证失败不留完成标记，移除约束后并发结果正确；schema 自动清理。
func TestMonitorTPSUpgradeFailureAndConcurrency(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	ttft := 100
	r := UsageRecord{RequestID: "old", TS: time.Date(2026, 1, 1, 0, 3, 0, 0, time.UTC), OwnerType: OwnerPersonal, UserID: "owner", Supplier: "s", UpstreamModel: "m", Status: "success", CallType: "responses", HTTPStatus: 200, Streamed: true, CompletionTokens: 10, DurationMS: 1100, TTFTMs: &ttft}
	if err := db.RecordUsage(ctx, []UsageRecord{r}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"DELETE FROM model_monitor_migrations",
		"UPDATE model_monitor_buckets SET output_tps_sum=30,output_tps_samples=1",
		"ALTER TABLE model_monitor_buckets ADD CONSTRAINT reject_tps_rebuild CHECK(output_tps_sum >= 30)",
	} {
		if _, err := db.Engine.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx); err == nil {
		t.Fatal("历史修正失败必须使启动迁移失败")
	}
	markers, err := db.Engine.QueryString("SELECT name FROM model_monitor_migrations")
	if err != nil || len(markers) != 0 {
		t.Fatal("失败迁移不能留下完成标记", markers, err)
	}
	for _, row := range monitorRows(t, db, builder.NewCond()) {
		if row.AvgTPS == nil || *row.AvgTPS != 30 {
			t.Fatal("失败迁移没有整体回滚", row)
		}
	}
	if _, err := db.Engine.Exec("ALTER TABLE model_monitor_buckets DROP CONSTRAINT reject_tps_rebuild"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 3)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- db.migrateMonitorTPS(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); r.RequestID = "new"; errors <- db.RecordUsage(ctx, []UsageRecord{r}) }()
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range monitorRows(t, db, builder.NewCond()) {
		if row.Requests != 2 || row.TPSSamples != 2 || row.AvgTPS == nil || *row.AvgTPS != 10 {
			t.Fatal("并发升级和结算丢失或重复 TPS 样本", row)
		}
	}
}
