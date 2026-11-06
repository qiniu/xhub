package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/cmd/regression/testsupport"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
)

// spoolRuntime 提供Flush最小宿主；未使用能力保留嵌入接口，便于发现意外调用；记录磁盘hook次数。
type spoolRuntime struct {
	Runtime
	db    *iam.DB
	err   error
	calls int
}

// Identity 返回隔离数据库或nil，供Flush判断是否可落账；无副作用。
func (s *spoolRuntime) Identity() *iam.DB { return s.db }

// Redis 返回nil，验证磁盘恢复不依赖Redis；无副作用。
func (s *spoolRuntime) Redis() *live.Client { return nil }

// FlushLocalUsage 记录恢复调用并返回注入错误，不访问外部上游或修改账单。
func (s *spoolRuntime) FlushLocalUsage(context.Context) error { s.calls++; return s.err }

// TestFlushLocalSpoolHook 验证无Redis时调用磁盘恢复hook，失败不panic，无身份库时不调用；私有PG/schema自动清理。
func TestFlushLocalSpoolHook(t *testing.T) {
	db, err := iam.Open(t.Context(), testsupport.Postgres(t, "runtime_spool"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, tc := range []struct {
		name  string
		db    *iam.DB
		err   error
		calls int
	}{{"success", db, nil, 1}, {"retained_failure", db, errors.New("retained"), 1}, {"no_identity", nil, nil, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			h := &spoolRuntime{db: tc.db, err: tc.err}
			Flush(h)
			if h.calls != tc.calls {
				t.Fatalf("恢复调用次数=%d，预期%d", h.calls, tc.calls)
			}
		})
	}
}

// deliveryRuntime 使用隔离的 Redis 和 PostgreSQL 测试确认队列，不访问真实供应商。
type deliveryRuntime struct {
	Runtime
	db    *iam.DB
	redis *live.Client
}

// Identity 返回本测试私有数据库，不修改任何外部数据。
func (s *deliveryRuntime) Identity() *iam.DB { return s.db }

// Redis 返回本测试独立 Redis 客户端，供实际队列和热费用验证。
func (s *deliveryRuntime) Redis() *live.Client { return s.redis }

// TestFlushConfirmedDelivery 验证到期标记先处理已确认 Redis 快照、准确结算并释放热费用。
// 前置私有 PostgreSQL 和本地临时 Redis；测试结束关闭连接并自动回收服务及 schema。
func TestFlushConfirmedDelivery(t *testing.T) {
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Fatal("Redis is required for queue recovery test")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	cmd := exec.Command(binary, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var redis *live.Client
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		redis, err = live.Open("redis://" + address + "/0")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("isolated Redis did not start: %v", err)
	}
	t.Cleanup(func() { _ = redis.Close() })
	db, err := iam.Open(t.Context(), testsupport.Postgres(t, "runtime_delivery"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	user, err := db.CreateUser(t.Context(), iam.Actor{Kind: "system"}, iam.UserInput{Email: "runtime-delivery@example.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	rec := iam.UsageRecord{RequestID: "runtime-confirmed", UserID: user.ID, OwnerType: iam.OwnerPersonal, TS: time.Now().UTC(), Model: "test", CallType: "chat", Status: "success", HTTPStatus: 200, Cost: 0.25}
	if err := db.PrepareDelivery(t.Context(), rec, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.Exec(`UPDATE usage_delivery_pending SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	row := live.SpendLog{RequestID: rec.RequestID, UserID: rec.UserID, Spend: rec.Cost, SpendValid: true, Start: rec.TS.Format(time.RFC3339Nano), DeliverySnapshot: string(raw)}
	if err := redis.EnqueueConfirmedDelivery(row); err != nil {
		t.Fatal(err)
	}
	if redis.HotSpend("user/"+user.ID) != 0.25 {
		t.Fatal("热费用缺失")
	}
	Flush(&deliveryRuntime{db: db, redis: redis})
	event, err := db.GetUsageEvent(t.Context(), iam.UsageQuery{}, rec.RequestID)
	if err != nil || event.Status != "success" || event.Cost != 0.25 {
		t.Fatalf("确认费用被错误恢复: %+v err=%v", event, err)
	}
	if empty, err := redis.SpendQueueEmpty(t.Context()); err != nil || !empty || redis.HotSpend("user/"+user.ID) != 0 {
		t.Fatal("结算后队列或热费用未清理")
	}
	// 一条归属冲突的已确认终态不能永久阻断其他用户的正常账单；原始证据必须保留。
	conflictMarker := rec
	conflictMarker.RequestID = "runtime-conflicting-owner"
	if err := db.PrepareDelivery(t.Context(), conflictMarker, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	conflictSnapshot := conflictMarker
	conflictSnapshot.Model = "different-model"
	conflictRaw, err := json.Marshal(conflictSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	leadingRow := live.SpendLog{RequestID: "runtime-before-conflict", UserID: user.ID, OwnerType: iam.OwnerPersonal, Model: "test", CallType: "chat", Status: "success", HTTPStatus: 200, Spend: 0.1, SpendValid: true, Start: rec.TS.Format(time.RFC3339Nano)}
	if err := redis.EnqueueSpend(leadingRow); err != nil {
		t.Fatal(err)
	}
	conflictRow := live.SpendLog{RequestID: conflictMarker.RequestID, UserID: user.ID, Spend: 0.25, SpendValid: true, Start: rec.TS.Format(time.RFC3339Nano), DeliverySnapshot: string(conflictRaw)}
	if err := redis.EnqueueConfirmedDelivery(conflictRow); err != nil {
		t.Fatal(err)
	}
	validRow := live.SpendLog{RequestID: "runtime-after-conflict", UserID: user.ID, OwnerType: iam.OwnerPersonal, Model: "test", CallType: "chat", Status: "success", HTTPStatus: 200, Spend: 0.1, SpendValid: true, Start: rec.TS.Format(time.RFC3339Nano)}
	if err := redis.EnqueueSpend(validRow); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		Flush(&deliveryRuntime{db: db, redis: redis})
	}
	if _, err := db.GetUsageEvent(t.Context(), iam.UsageQuery{}, leadingRow.RequestID); err != nil {
		t.Fatalf("冲突之前的正常账单也应结算: %v", err)
	}
	if _, err := db.GetUsageEvent(t.Context(), iam.UsageQuery{}, validRow.RequestID); err != nil {
		t.Fatalf("冲突账单不能永久阻塞之后的正常账单: %v", err)
	}
	if rows, _ := redis.PeekLogs(10); len(rows) != 1 || rows[0].RequestID != conflictMarker.RequestID || math.Abs(redis.HotSpend("user/"+user.ID)-0.25) > 1e-9 {
		t.Fatalf("冲突记录及待结算额度必须保留: rows=%+v hot=%v", rows, redis.HotSpend("user/"+user.ID))
	}
	if n, err := db.Engine.Table("usage_delivery_pending").Where("request_id = ?", conflictMarker.RequestID).Count(); err != nil || n != 1 {
		t.Fatalf("冲突原始标记必须保留: n=%d err=%v", n, err)
	}
	bad := rec
	bad.RequestID = "runtime-corrupt"
	if err := db.PrepareDelivery(t.Context(), bad, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.Exec(`UPDATE usage_delivery_pending SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	row.RequestID, row.DeliverySnapshot = bad.RequestID, string(raw) // 快照 ID 故意与队列条目不符。
	if err := redis.EnqueueConfirmedDelivery(row); err != nil {
		t.Fatal(err)
	}
	Flush(&deliveryRuntime{db: db, redis: redis})
	if empty, err := redis.SpendQueueEmpty(t.Context()); err != nil || empty {
		t.Fatal("损坏终态必须留在队列中")
	}
	if n, err := db.Engine.Table("usage_delivery_pending").Where("request_id = ?", bad.RequestID).Count(); err != nil || n != 1 {
		t.Fatalf("损坏队列不能触发零费用恢复: n=%d err=%v", n, err)
	}
}
