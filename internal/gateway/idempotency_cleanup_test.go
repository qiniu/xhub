package gateway

import (
	"context"
	"testing"
	"time"
)

// TestIdempotencyCleanupLoopStops 验证服务关闭时维护循环及时退出，不依赖数据库或留下后台进程。
func TestIdempotencyCleanupLoopStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		(&Server{}).idempotencyCleanupLoop(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消后清理循环未退出")
	}
}
