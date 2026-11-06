package gateway

import (
	"context"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
)

// idempotencyCleanupLoop 定期清理已过期的请求摘要；ctx 随网关监听生命周期取消，无返回值。
// 启动于 serveListener，每次最多清理 256 条；错误只记日志，不能阻断新请求或提前清除未过期标识。
func (s *Server) idempotencyCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			cleanCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := s.IAM.CleanupInferenceFences(cleanCtx, now, 256)
			cancel()
			if err != nil && ctx.Err() == nil {
				util.Error("idempotency fence cleanup failed: %v", err)
			}
		}
	}
}
