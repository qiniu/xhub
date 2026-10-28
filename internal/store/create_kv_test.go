package store

import (
	"context"
	"sync"
	"testing"

	"github.com/sunqirui1987/xhub/cmd/regression/testsupport"
)

// TestCreateKVIfAbsent通过真实隔离PostgreSQL验证并发仅一赢家、冲突不覆盖、取消和数据库失败；关闭存储并由testsupport删除schema。
func TestCreateKVIfAbsent(t *testing.T) {
	st, err := Open(testsupport.Postgres(t, "create_kv"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Engine.Close(); err != nil {
			t.Error("关闭配置库失败", err)
		}
	})
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := st.CreateKVIfAbsent(t.Context(), "test", "same", `{"name":"original"}`)
			results <- ok
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	count := 0
	for ok := range results {
		if ok {
			count++
		}
	}
	for err := range failures {
		if err != nil {
			t.Error(err)
		}
	}
	if count != 1 {
		t.Fatal("并发注册必须只有一个赢家", count)
	}
	if ok, err := st.CreateKVIfAbsent(t.Context(), "test", "same", `{"name":"changed"}`); ok || err != nil {
		t.Fatal("重名不应覆盖", ok, err)
	}
	got, err := st.GetKV("test", "same")
	if err != nil || got["name"] != "original" {
		t.Fatal("原记录改变", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ok, err := st.CreateKVIfAbsent(ctx, "test", "cancelled", `{}`); ok || err == nil {
		t.Fatal("取消不能算成功")
	}
	if _, err := st.DB.Exec(`ALTER TABLE kv ADD CONSTRAINT reject_create CHECK(kind <> 'reject')`); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.CreateKVIfAbsent(t.Context(), "reject", "new", `{}`); ok || err == nil {
		t.Fatal("SQL失败不能算成功")
	}
	if err := st.DeleteKV("test", "same"); err != nil {
		t.Fatal("清理记录失败", err)
	}
	for _, st := range []*Store{nil, {}} {
		if ok, err := st.CreateKVIfAbsent(t.Context(), "test", "missing", `{}`); ok || err == nil {
			t.Fatal("缺失数据库不能成功")
		}
	}
}
