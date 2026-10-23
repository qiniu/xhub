package execution

import (
	"context"
	"testing"
	"time"
)

// TestDiagnosticResponseCachePolicy 验证内部诊断标记只禁缓存且保留取消、期限和其他请求值。
// 参数t为上下文；前置内存context，默认不禁缓存，派生标记不污染父上下文；没有网络或存储资源，结束取消context。
func TestDiagnosticResponseCachePolicy(t *testing.T) {
	type otherKey struct{}
	deadline := time.Now().Add(time.Minute)
	parent, cancel := context.WithDeadline(context.WithValue(t.Context(), otherKey{}, "caller"), deadline)
	defer cancel()
	if responseCacheDisabled(parent) {
		t.Fatal("普通请求默认被禁用缓存")
	}
	child := WithoutResponseCache(parent)
	if !responseCacheDisabled(child) || responseCacheDisabled(parent) || child.Value(otherKey{}) != "caller" {
		t.Fatal("内部缓存标记未生效或污染原上下文")
	}
	if got, ok := child.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatal("诊断请求丢失原期限")
	}
	cancel()
	select {
	case <-child.Done():
		if child.Err() != context.Canceled {
			t.Fatalf("诊断请求取消原因错误: %v", child.Err())
		}
	default:
		t.Fatal("诊断请求丢失原取消信号")
	}
}
