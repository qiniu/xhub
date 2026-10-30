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

// TestClientCachePolicy 验证正常、空值、组合与非法控制；独占内存对象，断言不消费供应商缓存字段，无需清理。
func TestClientCachePolicy(t *testing.T) {
	for _, tc := range []struct {
		name              string
		value             any
		read, write, fail bool
	}{
		{"absent", nil, true, true, false}, {"empty", map[string]any{}, true, true, false},
		{"read", map[string]any{"no-cache": true}, false, true, false},
		{"write", map[string]any{"no-store": true}, true, false, false},
		{"both", map[string]any{"no-cache": true, "no-store": true}, false, false, false},
		{"false", map[string]any{"no-cache": false, "no-store": false}, true, true, false},
		{"scalar", true, true, true, true}, {"list", []any{}, true, true, true},
		{"string", map[string]any{"no-cache": "true"}, true, true, true},
		{"number", map[string]any{"no-store": 1}, true, true, true},
		{"null flag", map[string]any{"no-cache": nil}, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"cache": tc.value, "prompt_cache_key": "vendor-value"}
			p, err := consumeClientCachePolicy(body)
			if (err != nil) != tc.fail {
				t.Fatalf("控制校验错误: %+v %v", p, err)
			}
			if !tc.fail && (p.read != tc.read || p.write != tc.write) {
				t.Fatalf("读写策略错误: %+v", p)
			}
			if body["prompt_cache_key"] != "vendor-value" {
				t.Fatal("供应商缓存参数改变")
			}
		})
	}
	body := map[string]any{"cache": map[string]any{"no-cache": true, "extension": "keep"}}
	if _, err := consumeClientCachePolicy(body); err != nil {
		t.Fatal(err)
	}
	if got := body["cache"].(map[string]any); len(got) != 1 || got["extension"] != "keep" {
		t.Fatal("扩展字段丢失", got)
	}
}
