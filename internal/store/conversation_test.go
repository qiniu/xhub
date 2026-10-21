package store

import (
	"context"
	"sync"
	"testing"
)

// TestConversationPersistence 验证真实PG中的原子归属、历史读写、跨实例与错误路径。
// 前置独立schema，16个并发创建只能有一个赢家；不依赖Redis，结束由fixture删除schema。
func TestConversationPersistence(t *testing.T) {
	st, _ := openModelWeightStores(t)
	ctx := t.Context()
	key := "session_route:v1:owner:test"
	if v, e := st.ReadConversation(ctx, key); e != nil || v != "" {
		t.Fatal(v, e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, e := st.CompareConversation(ctx, key, "", "first"); e != nil || v != "first" {
				t.Errorf("并发首次写失败: %q %v", v, e)
			}
		}()
	}
	wg.Wait()
	if v, e := st.CompareConversation(ctx, key, "", "wrong"); e != nil || v != "first" {
		t.Fatal("覆盖首次归属", v, e)
	}
	if v, e := st.CompareConversation(ctx, key, "first", "invalid"); e != nil || v != "invalid" {
		t.Fatal(v, e)
	}
	if v, e := st.CompareConversation(ctx, key, "first", "wrong"); e != nil || v != "invalid" {
		t.Fatal("过期CAS覆盖", v, e)
	}
	if v, e := st.CompareConversation(ctx, "missing", "old", "new"); e != nil || v != "" {
		t.Fatal("非首次CAS创建记录", v, e)
	}
	if e := st.WriteConversation(ctx, "history", `{"text":"hello"}`); e != nil {
		t.Fatal(e)
	}
	other := &Store{DB: st.DB}
	if v, e := other.ReadConversation(ctx, "history"); e != nil || v != `{"text":"hello"}` {
		t.Fatal(v, e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := st.ReadConversation(cancelled, key); e == nil {
		t.Fatal("忽略读取失败")
	}
	if _, e := st.CompareConversation(cancelled, key, "", "new"); e == nil {
		t.Fatal("忽略比较失败")
	}
	if e := st.WriteConversation(cancelled, key, "new"); e == nil {
		t.Fatal("忽略保存失败")
	}
	var absent *Store
	if _, e := absent.ReadConversation(ctx, key); e == nil {
		t.Fatal("缺存储仍读成功")
	}
	if _, e := absent.CompareConversation(ctx, key, "", "new"); e == nil {
		t.Fatal("缺存储仍比较成功")
	}
	if e := absent.WriteConversation(ctx, key, "new"); e == nil {
		t.Fatal("缺存储仍写成功")
	}
}
