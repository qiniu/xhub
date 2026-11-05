package execution

import (
	"errors"
	"testing"
)

// TestStreamAttemptFailureBoundary 验证流失败后的既有重试边界，涵盖输出、历史准备和原生响应。
// 使用独立内存事实检查成功及各类失败，不访问网络或数据库，测试结束无需清理。
func TestStreamAttemptFailureBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		started, preparationFailed, native, failed, want bool
	}{
		{"uncommitted_failure", false, false, false, true, false},
		{"committed_failure", true, false, false, true, true},
		{"history_failure", false, true, false, true, true},
		{"native_failure", false, false, true, true, true},
		{"successful_output", true, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := streamAttempt{upstreamStatus: 200, clientStarted: tc.started}
			if tc.failed {
				a.err = errors.New("synthetic upstream failure")
			}
			if got := a.mustStopAfterFailure(tc.preparationFailed, tc.native); got != tc.want {
				t.Fatalf("失败流是否停止不符: got=%v want=%v", got, tc.want)
			}
		})
	}
}
