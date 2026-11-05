package execution

import (
	"context"
	"testing"
)

// TestBypassAttemptFailure 验证转发失败后取消优先、已输出禁止重试、对话重试和任务不重放。
// 使用入站上下文与响应状态的内存事实；覆盖取消、截止时间及输出边界，无外部资源或清理。
func TestBypassAttemptFailure(t *testing.T) {
	for _, tc := range []struct {
		name             string
		callerErr        error
		streamed, replay bool
		want             bypassFailure
	}{
		{"caller_canceled_first", context.Canceled, true, true, bypassCanceled},
		{"caller_deadline", context.DeadlineExceeded, false, true, bypassCanceled},
		{"partial_stream", nil, true, true, bypassStarted},
		{"retry_dialogue", nil, false, true, bypassRetry},
		{"ambiguous_task", nil, false, false, bypassStop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBypassFailure(tc.callerErr, tc.streamed, tc.replay); got != tc.want {
				t.Fatalf("失败分类错误: got=%v want=%v", got, tc.want)
			}
		})
	}
}
