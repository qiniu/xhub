package execution

import (
	"context"
	"errors"
	"testing"

	dptransport "github.com/sunqirui1987/xhub/internal/dataplane/transport"
)

// TestBypassAttemptFailure 验证取消优先级、已输出禁止重试、对话重试与任务不重放。
// 使用内存响应事实和合成错误；覆盖成功、入站取消及上游超时，测试无外部资源或清理。
func TestBypassAttemptFailure(t *testing.T) {
	for _, tc := range []struct {
		name             string
		err, callerErr   error
		streamed, replay bool
		want             bypassFailure
	}{
		{"success", nil, nil, false, true, bypassNoFailure},
		{"caller_canceled_first", errors.New("broken"), context.Canceled, true, true, bypassCanceled},
		{"partial_stream", errors.New("broken"), nil, true, true, bypassStarted},
		{"retry_dialogue", context.DeadlineExceeded, nil, false, true, bypassRetry},
		{"ambiguous_task", context.DeadlineExceeded, nil, false, false, bypassStop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := bypassAttempt{response: dptransport.ForwardResponse{Streamed: tc.streamed}, err: tc.err}
			if got := a.failure(tc.callerErr, tc.replay); got != tc.want {
				t.Fatalf("失败分类错误: got=%v want=%v", got, tc.want)
			}
		})
	}
}
