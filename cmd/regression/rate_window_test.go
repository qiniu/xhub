package regression

import (
	"testing"
	"time"
)

// rateTestWindowDelay 计算短 HTTP 限流序列开始前的等待；now 为当前时刻，返回到下个分钟安全起点的时长。
// 回归夹具使用：只在分钟最后十秒等待，避免把正确的分钟重置误判为限流失效；不改变生产时钟或计数。
func rateTestWindowDelay(now time.Time) time.Duration {
	remaining := now.Truncate(time.Minute).Add(time.Minute).Sub(now)
	if remaining <= 10*time.Second {
		return remaining + 50*time.Millisecond
	}
	return 0
}

// waitForRateTestMinute 为真实 HTTP 限流序列保留至少十秒；参数为当前测试，无返回值。
// 调用方在首个计数请求前使用，取消时终止测试；定时器在返回时清理，不修改数据库或生产限制。
func waitForRateTestMinute(t *testing.T) {
	t.Helper()
	if delay := rateTestWindowDelay(time.Now()); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-t.Context().Done():
			t.Fatal("限流回归等待新分钟时测试已取消")
		}
	}
}

// TestRateTestWindowDelay 验证正常、边界和最后一纳秒的等待；前置固定时间，断言不会跨桶误测，无外部资源需清理。
func TestRateTestWindowDelay(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{"minute start", time.Unix(120, 0), 0},
		{"safe window", time.Unix(169, 0), 0},
		{"ten second boundary", time.Unix(170, 0), 10*time.Second + 50*time.Millisecond},
		{"CI rejection crossed 05:09:59 to 05:10:00", time.Date(2026, 10, 28, 5, 9, 59, 0, time.UTC), time.Second + 50*time.Millisecond},
		{"last nanosecond", time.Unix(179, 999999999), 50*time.Millisecond + time.Nanosecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rateTestWindowDelay(tc.now); got != tc.want {
				t.Fatalf("分钟边界等待错误: got=%s want=%s", got, tc.want)
			}
		})
	}
}
