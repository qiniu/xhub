package live

import (
	"context"
	"encoding/json"
	"math"
	"net"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestOrganizationPendingPeriod 前置独立 Redis，验证边界时间、迟到旧费用、去重、确认与升级遗留队列。
// 参数 t 为测试上下文，无返回值；非法时间保守按当前计入，损坏索引不得部分写入；testRedis 负责清理进程。
func TestOrganizationPendingPeriod(t *testing.T) {
	c := testRedis(t)
	ctx := t.Context()
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	row := SpendLog{RequestID: "old", OrgID: "org", APIKey: "key", Spend: 10, SpendValid: true, Start: cutoff.Add(-time.Second).Format(time.RFC3339Nano)}
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	if got := c.HotSpendSince("org", cutoff); got != 0 {
		t.Fatalf("旧周期费用占新额度: %v", got)
	}
	row.RequestID, row.Start, row.Spend = "boundary", cutoff.Format(time.RFC3339Nano), 2
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	if got := c.HotSpendSince("org", cutoff); got != 2 {
		t.Fatalf("起点应包含且去重: %v", got)
	}
	row.RequestID, row.Start, row.Spend = "late-old", cutoff.Add(-time.Hour).Format(time.RFC3339Nano), 3
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	if got := c.HotSpendSince("org", cutoff); got != 2 {
		t.Fatalf("迟到旧费用污染周期: %v", got)
	}
	_, raw := c.PeekLogs(10)
	if err := c.AckFlushed(map[string]float64{"org/org": 15, "key/key": 15}, len(raw), raw[0]); err != nil {
		t.Fatal(err)
	}
	if got := c.HotSpendSince("org", cutoff); got != 0 {
		t.Fatalf("确认未释放时间索引: %v", got)
	}
	if n := c.rdb.ZCard(ctx, "xhub:spend:org-pending:org").Val(); n != 0 {
		t.Fatalf("确认遗留索引: %d", n)
	}
	row.RequestID, row.Start, row.Spend = "indexed-missing-time", "", 1
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	if got := c.HotSpendSince("org", cutoff.Add(time.Hour)); got != 1 {
		t.Fatalf("缺失时间不能被后续周期排除: %v", got)
	}
	_, raw = c.PeekLogs(10)
	if err := c.AckFlushed(map[string]float64{"org/org": 1, "key/key": 1}, 1, raw[0]); err != nil {
		t.Fatal(err)
	}
	// 直接还原升级前的队列形态，没有新索引；新旧事件混合时必须兼容，不可漏算当前费用。
	row.RequestID, row.Start, row.Spend = "legacy-current", cutoff.Format(time.RFC3339Nano), 4
	encoded, _ := json.Marshal(row)
	if err := c.rdb.RPush(ctx, "xhub:spendlog", encoded).Err(); err != nil {
		t.Fatal(err)
	}
	if got := c.HotSpendSince("org", cutoff); got != 4 {
		t.Fatalf("旧队列兼容漏费: %v", got)
	}
	row.RequestID, row.Start, row.Spend = "missing-time", "", 1
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	if got := c.HotSpendSince("org", cutoff); got != 5 {
		t.Fatalf("非法时间应保守计入: %v", got)
	}
	if got := c.HotSpendSince("other", cutoff); got != 0 {
		t.Fatalf("跨组织污染: %v", got)
	}
	if err := c.rdb.Set(ctx, "xhub:spend:org-pending:broken", "bad", 0).Err(); err != nil {
		t.Fatal(err)
	}
	row.RequestID, row.OrgID = "rejected", "broken"
	before := c.rdb.LLen(ctx, "xhub:spendlog").Val()
	if err := c.EnqueueSpend(row); err == nil {
		t.Fatal("损坏索引未拒绝入队")
	}
	if c.rdb.LLen(ctx, "xhub:spendlog").Val() != before || c.rdb.HExists(ctx, "xhub:spend:requests", "rejected").Val() {
		t.Fatal("拒绝入队却部分写入")
	}
}

// testRedis 为热状态单元测试启动随机端口的专用 Redis；参数 t 提供断言和清理注册。
// 返回独立客户端；缺少 Redis 二进制时跳过，连接失败时报错，结束关闭客户端并终止进程，不访问共享数据。
func testRedis(t *testing.T) *Client {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	_ = listener.Close()
	cmd := exec.Command(binary, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	rdb := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1, DialTimeout: time.Second})
	t.Cleanup(func() { _ = rdb.Close() })
	for deadline := time.Now().Add(5 * time.Second); ; {
		if err := rdb.Ping(context.Background()).Err(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("redis did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &Client{rdb: rdb}
}

// TestEnqueueSpendConcurrentDedupAndAck 在专用 Redis 中并发重放同一事件，验证仅入队一次、确认后仍幂等且不丢并发增量。
// 无外部凭据；testRedis 自动关闭客户端及进程，断言失败定位到日志数量、热花费或去重 TTL。
func TestEnqueueSpendConcurrentDedupAndAck(t *testing.T) {
	c := testRedis(t)
	row := SpendLog{RequestID: "tenant/task/settlement", APIKey: "key", OrgID: "org", Spend: 1.25, SpendValid: true}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.EnqueueSpend(row); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rows, raw := c.PeekLogs(100)
	if len(rows) != 1 {
		t.Fatalf("queued %d duplicates", len(rows))
	}
	if got := c.HotSpend("key/key"); got != 1.25 {
		t.Fatalf("hot spend %v", got)
	}
	row.RequestID = "tenant/task/other"
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	deltas := map[string]float64{"key/key": 1.25, "org/org": 1.25}
	if err := c.AckFlushed(deltas, 1, raw[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.AckFlushed(deltas, 1, raw[0]); err != nil {
		t.Fatal(err)
	}
	if got := c.HotSpend("key/key"); got != 1.25 {
		t.Fatalf("ack lost concurrent spend: %v", got)
	}
	row.RequestID = "tenant/task/settlement"
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	rows, _ = c.PeekLogs(100)
	if len(rows) != 1 {
		t.Fatalf("replay after ack queued again: %d", len(rows))
	}
	if ttl := c.rdb.TTL(context.Background(), "xhub:spend:requests").Val(); ttl != -1 {
		t.Fatalf("dedup unexpectedly expires: %v", ttl)
	}
}

// TestEnqueueSpendRequiresIdentity 验证独立 Redis 拒绝缺少请求标识的计费事件；不发布无主用量，夹具自动清理。
func TestEnqueueSpendRequiresIdentity(t *testing.T) {
	c := testRedis(t)
	if err := c.EnqueueSpend(SpendLog{Spend: 1, SpendValid: true}); err == nil {
		t.Fatal("accepted empty identity")
	}
}

// TestSpendQueueCurrentContract 验证计费、免费和未计价日志共用唯一可靠入口，五层热花费只记录合法可计费金额。
// 前置专用 Redis；正常确认与重复确认均不创建旧主体索引，非法队列类型拒绝且不留下计数/去重写入；夹具自动清理。
func TestSpendQueueCurrentContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spend float64
		valid bool
		want  float64
	}{
		{"计费", 1.25, true, 1.25},
		{"免费", 0, true, 0},
		{"未计价", 1.25, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testRedis(t)
			row := SpendLog{RequestID: "current-event", APIKey: "key", UserID: "user", TeamID: "team", OrgID: "org", ProjectID: "project", Spend: tc.spend, SpendValid: tc.valid}
			if err := c.EnqueueSpend(row); err != nil {
				t.Fatalf("当前事件入队失败: %v", err)
			}
			rows, raw := c.PeekLogs(10)
			if len(rows) != 1 || len(raw) != 1 || rows[0].SpendValid != tc.valid {
				t.Fatalf("日志丢失或计价状态改变: %+v", rows)
			}
			deltas := map[string]float64{}
			for _, ref := range []string{"key/key", "user/user", "team/team", "org/org", "project/project"} {
				if got := c.HotSpend(ref); got != tc.want {
					t.Fatalf("主体 %s 热花费=%v，预期 %v", ref, got, tc.want)
				}
				deltas[ref] = tc.want
			}
			for i := 0; i < 2; i++ {
				if err := c.AckFlushed(deltas, 1, raw[0]); err != nil {
					t.Fatalf("确认失败: %v", err)
				}
			}
			for ref := range deltas {
				if got := c.HotSpend(ref); got != 0 {
					t.Fatalf("确认后主体 %s 仍有花费 %v", ref, got)
				}
			}
			if got := c.rdb.Exists(context.Background(), "xhub:spend:ids").Val(); got != 0 {
				t.Fatal("可靠入口仍创建废弃主体索引")
			}
			if rows, _ := c.PeekLogs(10); len(rows) != 0 {
				t.Fatalf("已确认日志仍残留: %+v", rows)
			}
		})
	}
	t.Run("非法队列", func(t *testing.T) {
		c := testRedis(t)
		ctx := context.Background()
		if err := c.rdb.Set(ctx, "xhub:spendlog", "not-a-list", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := c.EnqueueSpend(SpendLog{RequestID: "rejected", APIKey: "key", Spend: 1, SpendValid: true}); err == nil {
			t.Fatal("非法队列类型未拒绝")
		}
		if got := c.rdb.Exists(ctx, "xhub:spend:key/key", "xhub:spend:requests", "xhub:spend:ids").Val(); got != 0 {
			t.Fatalf("拒绝后出现部分写入: %d", got)
		}
	})
}

// TestEnqueueSpendRejectsInvalidStateWithoutPartialWrites 在独立 Redis 注入损坏计数和非法金额。
// 验证入队/确认失败不产生部分写入、重复 payload 不覆盖原费用；夹具终止进程清理全部注入数据。
func TestEnqueueSpendRejectsInvalidStateWithoutPartialWrites(t *testing.T) {
	c := testRedis(t)
	ctx := context.Background()
	row := SpendLog{RequestID: "settlement", APIKey: "key", OrgID: "org", Spend: 2, SpendValid: true}
	if err := c.rdb.Set(ctx, "xhub:spend:org/org", "broken", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.EnqueueSpend(row); err == nil {
		t.Fatal("accepted corrupt counter")
	}
	if got := c.rdb.Exists(ctx, "xhub:spend:key/key", "xhub:spendlog", "xhub:spend:requests").Val(); got != 0 {
		t.Fatalf("partial writes: %d", got)
	}
	if err := c.rdb.Del(ctx, "xhub:spend:org/org").Err(); err != nil {
		t.Fatal(err)
	}
	for _, cost := range []float64{-1, math.NaN(), math.Inf(1)} {
		row.Spend = cost
		if err := c.EnqueueSpend(row); err == nil {
			t.Fatalf("accepted %v", cost)
		}
	}
	row.Spend = 2
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	row.Spend = 99
	if err := c.EnqueueSpend(row); err != nil {
		t.Fatal(err)
	}
	rows, raw := c.PeekLogs(10)
	if len(rows) != 1 || rows[0].Spend != 2 {
		t.Fatalf("first payload did not win: %+v", rows)
	}
	if err := c.rdb.Set(ctx, "xhub:spend:org/org", "broken", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.AckFlushed(map[string]float64{"key/key": 2, "org/org": 2}, 1, raw[0]); err == nil {
		t.Fatal("ack accepted corrupt counter")
	}
	if c.HotSpend("key/key") != 2 || c.rdb.LLen(ctx, "xhub:spendlog").Val() != 1 {
		t.Fatal("ack partially mutated queue or spend")
	}
}

// TestPeekLogsStopsAtCorruptEvent 注入损坏日志，验证只读取合法前缀且确认后不越过坏队头；独立 Redis 自动清理。
func TestPeekLogsStopsAtCorruptEvent(t *testing.T) {
	c := testRedis(t)
	if err := c.EnqueueSpend(SpendLog{RequestID: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := c.rdb.RPush(context.Background(), "xhub:spendlog", "{broken").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.EnqueueSpend(SpendLog{RequestID: "last"}); err != nil {
		t.Fatal(err)
	}
	rows, raw := c.PeekLogs(10)
	if len(rows) != 1 || len(raw) != 1 {
		t.Fatalf("invalid prefix: %d/%d", len(rows), len(raw))
	}
	if err := c.AckFlushed(nil, 1, raw[0]); err != nil {
		t.Fatal(err)
	}
	rows, raw = c.PeekLogs(10)
	if len(rows) != 0 || len(raw) != 0 || c.rdb.LLen(context.Background(), "xhub:spendlog").Val() != 2 {
		t.Fatal("corrupt event was skipped")
	}
}

// TestConcurrentTokenCounterHasExpiry 验证并发分钟计数无丢失且有 TTL，负增量拒绝；独立 Redis 自动清理。
func TestConcurrentTokenCounterHasExpiry(t *testing.T) {
	c := testRedis(t)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.bump("test:tpm", 3); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	keys := c.rdb.Keys(context.Background(), "test:tpm*").Val()
	if len(keys) != 1 {
		t.Fatalf("keys: %v", keys)
	}
	if got := c.rdb.Get(context.Background(), keys[0]).Val(); got != "120" {
		t.Fatalf("lost increments: %s", got)
	}
	if ttl := c.rdb.PTTL(context.Background(), keys[0]).Val(); ttl <= 0 || ttl > 120*time.Second {
		t.Fatalf("expiry: %v", ttl)
	}
	if _, err := c.bump("test:tpm", -1); err == nil {
		t.Fatal("accepted negative tokens")
	}
	if err := c.AddUsage("deployment", -1); err == nil {
		t.Fatal("accepted negative usage")
	}
}

// TestPendingSpendCount 验证硬额度启动闸门的空队列、真实排队和Redis失联边界；专用进程自动清理。
func TestPendingSpendCount(t *testing.T) {
	var absent *Client
	if n, err := absent.PendingSpendCount(); n != 0 || err != nil {
		t.Fatalf("无Redis不是空队列: %d %v", n, err)
	}
	c := testRedis(t)
	if err := c.EnqueueSpend(SpendLog{RequestID: "pending-count", KeyID: "key", Spend: 1, SpendValid: true}); err != nil {
		t.Fatal(err)
	}
	if n, err := c.PendingSpendCount(); n != 1 || err != nil {
		t.Fatalf("队列计数错误: %d %v", n, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PendingSpendCount(); err == nil {
		t.Fatal("Redis失败被当成空队列")
	}
}

// TestUserPendingPeriod 验证个人热账单的跨月边界、其他用户隔离及损坏失败路径；独立Redis由testRedis清理。
func TestUserPendingPeriod(t *testing.T) {
	c := testRedis(t)
	cutoff := time.Now().UTC()
	for _, row := range []SpendLog{
		{RequestID: "old", UserID: "u", APIKey: "k", Spend: 10, SpendValid: true, Start: cutoff.Add(-time.Second).Format(time.RFC3339Nano)},
		{RequestID: "current", UserID: "u", APIKey: "k", Spend: 2, SpendValid: true, Start: cutoff.Format(time.RFC3339Nano)},
		{RequestID: "other", UserID: "v", APIKey: "k", Spend: 9, SpendValid: true, Start: cutoff.Format(time.RFC3339Nano)},
	} {
		if err := c.EnqueueSpend(row); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.HotUserSpendSince("u", cutoff); got != 2 {
		t.Fatalf("个人周期热费用错误: %v", got)
	}
	if got := c.HotUserSpendSince("missing", cutoff); got != 0 {
		t.Fatal(got)
	}
	if err := c.rdb.RPush(t.Context(), "xhub:spendlog", "broken").Err(); err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(c.HotUserSpendSince("u", cutoff)) {
		t.Fatal("损坏账单必须明确失败")
	}
}
