package iam

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/cmd/regression/testsupport"
)

// TestDeliveryPendingRecoveryAndConfirmation 验证响应确认前只保留零费用恢复记录，确认后才允许结算实际费用。
// 使用隔离 PostgreSQL，分别模拟正常确认、原进程退出以及多实例并发恢复；testDB 在结束后回收库。
func TestDeliveryPendingRecoveryAndConfirmation(t *testing.T) {
	db := testDB(t)
	user, err := db.CreateUser(t.Context(), Actor{Kind: "system"}, UserInput{Email: "delivery@example.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	good := usageRecord("delivery-confirmed", user.ID, 0.125)
	good.RequestBody, good.ResponseBody = "private-prompt", "private-response"
	good.Label = "private-label"
	if err := db.PrepareDelivery(t.Context(), good, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Engine.QueryString("SELECT payload::text AS payload FROM usage_delivery_pending WHERE request_id=?", good.RequestID)
	if err != nil || len(rows) != 1 || strings.Contains(rows[0]["payload"], "private-") || strings.Contains(rows[0]["payload"], "0.125") {
		t.Fatalf("未确认记录泄露正文或可计费金额: rows=%v err=%v", rows, err)
	}
	if err := db.FlushPendingUsage(t.Context(), 10); err != nil || countEvents(t, db, good.RequestID) != 0 || userSpend(t, db, user.ID) != 0 {
		t.Fatalf("未确认账单提前结算: err=%v", err)
	}
	if err := db.ConfirmDelivery(t.Context(), good); err != nil {
		t.Fatal(err)
	}
	if err := db.FlushPendingUsage(t.Context(), 10); err != nil || countEvents(t, db, good.RequestID) != 1 || userSpend(t, db, user.ID) != 0.125 {
		t.Fatalf("确认后的账单未正确结算: err=%v", err)
	}
	unknown := usageRecord("delivery-unknown", user.ID, 0.5)
	if err := db.PrepareDelivery(t.Context(), unknown, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := db.RecoverExpiredDeliveries(t.Context(), now, 10); err != nil || n != 0 {
		t.Fatalf("未过期请求被恢复: n=%d err=%v", n, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.RecoverExpiredDeliveries(context.Background(), now.Add(2*time.Second), 10); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := db.ConfirmDelivery(t.Context(), unknown); err == nil {
		t.Fatal("未知结果恢复后不能重新计费")
	}
	if err := db.FlushPendingUsage(t.Context(), 10); err != nil || countEvents(t, db, unknown.RequestID) != 1 || userSpend(t, db, user.ID) != 0.125 {
		t.Fatalf("未知结果未按零费用恢复或重复扣费: err=%v", err)
	}
	event, err := db.GetUsageEvent(t.Context(), UsageQuery{}, unknown.RequestID)
	if err != nil || event.Status != "error" || event.Cost != 0 {
		t.Fatalf("恢复结果不是失败零费用: %+v %v", event, err)
	}
}

// TestDeliveryPendingRejectsConflictingRecords 验证非法记录、归属冲突与数据库失败不会创建或转移账单。
// 前置独立测试库，使用约束模拟写入失败；数据库和测试数据在结束时清理。
func TestDeliveryPendingRejectsConflictingRecords(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	if err := db.PrepareDelivery(t.Context(), UsageRecord{}, now.Add(time.Minute)); err == nil {
		t.Fatal("空标识应拒绝")
	}
	rec := usageRecord("delivery-conflict", "", 0.1)
	if err := db.PrepareDelivery(t.Context(), rec, now.Add(-time.Second)); err == nil {
		t.Fatal("失效时间应拒绝")
	}
	if err := db.PrepareDelivery(t.Context(), rec, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	other := rec
	other.UserID = "another-user"
	if err := db.ConfirmDelivery(t.Context(), other); err == nil {
		t.Fatal("不能更换账单归属")
	}
	failed := rec
	failed.Status, failed.HTTPStatus = "error", 499
	if err := db.ConfirmDelivery(t.Context(), failed); err == nil {
		t.Fatal("失败响应不能产生费用")
	}
	changedModel := rec
	changedModel.Model = "different-model"
	if err := db.ConfirmDelivery(t.Context(), changedModel); err == nil {
		t.Fatal("不能更换账单模型")
	}
	if _, err := db.Engine.Exec("ALTER TABLE usage_pending ADD CONSTRAINT reject_delivery_pending CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmDelivery(t.Context(), rec); err == nil {
		t.Fatal("持久化失败不能报告确认成功")
	}
	if n, err := db.Engine.Table("usage_delivery_pending").Count(); err != nil || n != 1 {
		t.Fatalf("失败必须保留恢复记录: n=%d err=%v", n, err)
	}
}

// TestDeliveryConfirmRecoveryRace 验证确认和到期恢复并发时只选择一个终态，费用不会重复或部分提交。
// 前置独立 PostgreSQL，模拟刚好跨过过期时间的并发实例；全部事件和用户由测试库清理。
func TestDeliveryConfirmRecoveryRace(t *testing.T) {
	db := testDB(t)
	u, err := db.CreateUser(t.Context(), Actor{Kind: "system"}, UserInput{Email: "delivery-race@example.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		rec := usageRecord(fmt.Sprintf("delivery-race-%d", i), u.ID, 0.125)
		deadline := time.Now().Add(time.Minute)
		if err := db.PrepareDelivery(t.Context(), rec, deadline); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() { <-start; results <- db.ConfirmDelivery(context.Background(), rec) }()
		go func() {
			<-start
			_, err := db.RecoverExpiredDeliveries(context.Background(), deadline.Add(time.Second), 10)
			results <- err
		}()
		close(start)
		first, second := <-results, <-results
		if first != nil && !errors.Is(first, ErrDeliveryMarkerGone) || second != nil && !errors.Is(second, ErrDeliveryMarkerGone) {
			t.Fatalf("并发确认异常: %v %v", first, second)
		}
		if err := db.FlushPendingUsage(t.Context(), 10); err != nil || countEvents(t, db, rec.RequestID) != 1 {
			t.Fatalf("并发结算丢失或重复: %v", err)
		}
		event, err := db.GetUsageEvent(t.Context(), UsageQuery{}, rec.RequestID)
		if err != nil || (event.Status == "success" && event.Cost != 0.125) || (event.Status == "error" && event.Cost != 0) || (event.Status != "success" && event.Status != "error") {
			t.Fatalf("并发落账终态无效: %+v %v", event, err)
		}
	}
}

// TestDeliveryRecoveryAfterProcessExit 验证保存待确认记录的进程退出后，新进程恢复为失败且费用为零。
// 测试使用专属 PostgreSQL schema 和子进程模拟中断，结束后关闭连接并自动删除隔离数据。
func TestDeliveryRecoveryAfterProcessExit(t *testing.T) {
	dsn := testsupport.Postgres(t, "iam")
	db, err := Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	child := exec.Command(os.Args[0], "-test.run=^TestDeliveryExitHelper$")
	child.Env = append(os.Environ(), "XHUB_TEST_DELIVERY_CHILD=1", "XHUB_TEST_DELIVERY_DSN="+dsn)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("模拟进程退出失败: %v %s", err, output)
	}
	if countEvents(t, db, "delivery-process-exit") != 0 {
		t.Fatal("确认之前发生结算")
	}
	if n, err := db.RecoverExpiredDeliveries(t.Context(), time.Now().Add(2*time.Minute), 10); err != nil || n != 1 {
		t.Fatalf("新进程未恢复: n=%d err=%v", n, err)
	}
	if err := db.FlushPendingUsage(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	event, err := db.GetUsageEvent(t.Context(), UsageQuery{}, "delivery-process-exit")
	if err != nil || event.Status != "error" || event.Cost != 0 {
		t.Fatalf("进程退出应为结果未知且零费用: %+v %v", event, err)
	}
}

// TestDeliveryExitHelper 在隔离子进程内写入待确认记录并立即退出；只由 TestDeliveryRecoveryAfterProcessExit 调用。
// 环境变量只指向父测试创建的数据库，不打印连接信息；父测试负责回收 schema 和连接。
func TestDeliveryExitHelper(t *testing.T) {
	if os.Getenv("XHUB_TEST_DELIVERY_CHILD") != "1" {
		return
	}
	db, err := Open(context.Background(), os.Getenv("XHUB_TEST_DELIVERY_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PrepareDelivery(context.Background(), usageRecord("delivery-process-exit", "", 0.75), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// 不执行关闭或确认路径，模拟网关在写出结果不明时进程消失。
	os.Exit(0)
}

// TestDeliveryRecordedFailureCleanup 验证早期标记只在账单已持久化时清理，恢复不能覆盖已记录的失败。
// 使用独立 PostgreSQL，先模拟尚无账单再写零费用失败，测试结束自动清理 schema。
func TestDeliveryRecordedFailureCleanup(t *testing.T) {
	db := testDB(t)
	rec := usageRecord("delivery-existing-failure", "", 0)
	deadline := time.Now().Add(time.Minute)
	if err := db.PrepareDelivery(t.Context(), rec, deadline); err != nil {
		t.Fatal(err)
	}
	if err := db.DiscardRecordedDelivery(t.Context(), rec.RequestID); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Engine.Table("usage_delivery_pending").Count(); n != 1 {
		t.Fatal("没有账单时不应清理恢复标记")
	}
	rec.Status, rec.HTTPStatus = "error", 502
	if _, err := db.RecordUsageDurably(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if n, err := db.RecoverExpiredDeliveries(t.Context(), deadline.Add(time.Second), 10); err != nil || n != 1 {
		t.Fatalf("已有账单标记清理失败: n=%d err=%v", n, err)
	}
	if err := db.FlushPendingUsage(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	event, err := db.GetUsageEvent(t.Context(), UsageQuery{}, rec.RequestID)
	if err != nil || event.HTTPStatus != 502 || event.Cost != 0 {
		t.Fatalf("已记录的错误被覆盖: %+v %v", event, err)
	}
	if n, _ := db.Engine.Table("usage_delivery_pending").Count(); n != 0 {
		t.Fatal("已有账单的标记未清理")
	}
	if err := db.DiscardRecordedDelivery(t.Context(), ""); err == nil {
		t.Fatal("空调用 ID 应拒绝")
	}
}

// TestSettleConfirmedDelivery 验证已确认快照可幂等恢复，缺失标记和冲突快照不能凭空计费。
// 前置独立 PostgreSQL，覆盖原子确认、重复重放与载荷冲突；测试后 schema 自动清理。
func TestSettleConfirmedDelivery(t *testing.T) {
	db := testDB(t)
	rec := usageRecord("confirmed-delivery", "", 0.5)
	if err := db.SettleConfirmedDelivery(t.Context(), rec); err == nil {
		t.Fatal("缺少标记不得创建账单")
	}
	if err := db.PrepareDelivery(t.Context(), rec, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.SettleConfirmedDelivery(t.Context(), rec); err != nil {
			t.Fatalf("确认/重放失败: %v", err)
		}
	}
	if countEvents(t, db, rec.RequestID) != 1 {
		t.Fatal("重复确认产生多条账单")
	}
	conflict := rec
	conflict.Cost = 0.7
	if err := db.SettleConfirmedDelivery(t.Context(), conflict); err == nil {
		t.Fatal("不同快照不应伪造重放成功")
	}
}

// TestDeliverySpoolOwnedMarker 验证磁盘独占标记不会被其他数据库实例按超时恢复。
// 使用隔离 PostgreSQL；在持有磁盘的实例确认前保持记录，确认后可正常清理，schema 自动回收。
func TestDeliverySpoolOwnedMarker(t *testing.T) {
	db := testDB(t)
	rec := usageRecord("spool-owned", "", 0.25)
	rec.DeliveryPending, rec.DeliverySpoolOnly = true, true
	now := time.Now()
	if err := db.PrepareDelivery(t.Context(), rec, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := db.RecoverExpiredDeliveries(t.Context(), now.Add(time.Minute), 10); err != nil || n != 0 {
		t.Fatalf("其他实例不能恢复磁盘标记: n=%d err=%v", n, err)
	}
	if err := db.SettleConfirmedDelivery(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	event, err := db.GetUsageEvent(t.Context(), UsageQuery{}, rec.RequestID)
	if err != nil || event.Cost != 0.25 || event.Status != "success" {
		t.Fatalf("磁盘终态未正确结算: %+v %v", event, err)
	}
}

// TestConfirmedDeliveryIgnoresUnrelatedPendingFailure 验证其他损坏账单不能阻止本请求确认并结算。
// 使用独立 PostgreSQL 注入无效待处理快照；检查本请求只结算一次且故障快照保留，测试库自动清理。
func TestConfirmedDeliveryIgnoresUnrelatedPendingFailure(t *testing.T) {
	db := testDB(t)
	if _, err := db.Engine.Exec(`INSERT INTO usage_pending(request_id,payload) VALUES ('unrelated-bad', '{}'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	rec := usageRecord("confirmed-isolated", "", 0.25)
	if err := db.PrepareDelivery(t.Context(), rec, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.SettleConfirmedDelivery(t.Context(), rec); err != nil {
		t.Fatalf("无关待结算错误不应阻止已确认账单: %v", err)
	}
	if countEvents(t, db, rec.RequestID) != 1 {
		t.Fatal("已确认账单未唯一结算")
	}
	if n, err := db.Engine.Table("usage_pending").Where("request_id = ?", "unrelated-bad").Count(); err != nil || n != 1 {
		t.Fatalf("无关故障快照应保留以供排查: n=%d err=%v", n, err)
	}
}

// TestConfirmedDeliveryUsesPreparedTimestamp 验证交付标记冻结的发生时间在确认和故障重放时保持一致。
// 使用隔离 PostgreSQL 准备不同开始时间的快照并重复结算，验证唯一账单和费用；测试库自动清理。
func TestConfirmedDeliveryUsesPreparedTimestamp(t *testing.T) {
	db := testDB(t)
	initial := usageRecord("prepared-timestamp", "", 0.25)
	initial.TS = time.Now().UTC().Truncate(time.Second)
	if err := db.PrepareDelivery(t.Context(), initial, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	record := initial
	record.TS = initial.TS.Add(-time.Second)
	if err := db.SettleConfirmedDelivery(t.Context(), record); err != nil {
		t.Fatalf("确认时应沿用标记的时间: %v", err)
	}
	// 重放必须携带已冻结的标记时间，否则严格快照比较应拒绝不同账单。
	record.TS = initial.TS
	if err := db.SettleConfirmedDelivery(t.Context(), record); err != nil {
		t.Fatalf("已冻结的同一终态应可安全重放: %v", err)
	}
	event, err := db.GetUsageEvent(t.Context(), UsageQuery{}, initial.RequestID)
	if err != nil || !event.TS.Equal(initial.TS) || event.Cost != initial.Cost {
		t.Fatalf("账单时间或金额与标记不一致: event=%+v err=%v", event, err)
	}
}

// TestFlushPendingUsageRequestRejectsMissing 验证定向结算不能把不存在的账单误认为已成功。
// 使用独立 PostgreSQL，覆盖空标识、缺失记录及已提交账单；测试库由夹具清理。
func TestFlushPendingUsageRequestRejectsMissing(t *testing.T) {
	db := testDB(t)
	if err := db.FlushPendingUsageRequest(t.Context(), ""); err == nil {
		t.Fatal("空请求标识应被拒绝")
	}
	if err := db.FlushPendingUsageRequest(t.Context(), "missing-request"); err == nil {
		t.Fatal("无待结算快照和账单时不能报告成功")
	}
	record := usageRecord("already-committed", "", 0.25)
	if err := db.RecordUsageExact(t.Context(), []UsageRecord{record}); err != nil {
		t.Fatal(err)
	}
	if err := db.FlushPendingUsageRequest(t.Context(), record.RequestID); err != nil {
		t.Fatalf("已提交的账单应可安全确认: %v", err)
	}
}
