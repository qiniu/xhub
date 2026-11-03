package util

import (
	"testing"
)

// TestCacheReady 检查nil、零值、正常实例与清理后的状态；只读内存对象，无外部前置及清理。
func TestCacheReady(t *testing.T) {
	var absent *DualCache
	if absent.Ready() || (&DualCache{}).Ready() {
		t.Fatal("未初始化缓存不能就绪")
	}
	c := NewCache()
	c.Set("entry", []byte("value"))
	if !c.Ready() {
		t.Fatal("正常缓存未就绪")
	}
	if _, ok := c.Get("entry"); !ok {
		t.Fatal("就绪检查改动缓存")
	}
	c.Flush()
	if !c.Ready() {
		t.Fatal("空缓存仍应就绪")
	}
}

// TestFlush 验证空缓存、重复清理及多条响应全部失效，清理后可重新写入。
// 前置独立内存缓存；无非法键限制，空键也必须清理；不访问外部资源，无需清理。
func TestFlush(t *testing.T) {
	c := NewCache()
	c.Flush()
	for _, key := range []string{"", "a", "b"} {
		c.Set(key, []byte("response"))
	}
	c.Flush()
	c.Flush()
	for _, key := range []string{"", "a", "b"} {
		if _, ok := c.Get(key); ok {
			t.Fatalf("清理后缓存仍命中 %q", key)
		}
	}
	c.Set("a", []byte("new"))
	if value, ok := c.Get("a"); !ok || string(value.Body) != "new" {
		t.Fatal("清理后无法保存新响应")
	}
}

// TestKeySeparatesPartsAndCacheCopiesBytes 验证键边界隔离和正文/权限范围的独立副本。
// 前置内存缓存，覆盖输入、返回值修改及同键替换；无外部资源，缓存随测试释放。
func TestKeySeparatesPartsAndCacheCopiesBytes(t *testing.T) {
	if CacheKey("ab", "c") == CacheKey("a", "bc") {
		t.Fatal("cache key aliases different part boundaries")
	}
	c := NewCache()
	source := []byte("response")
	models := []string{"group", "backup"}
	c.Set("key", source, models...)
	source[0] = 'X'
	models[0] = "changed"
	first, ok := c.Get("key")
	if !ok || string(first.Body) != "response" {
		t.Fatalf("stored value changed: %q, ok=%v", first, ok)
	}
	first.Body[0] = 'Y'
	first.Models[0] = "changed"
	second, _ := c.Get("key")
	if string(second.Body) != "response" || second.Models[0] != "group" || second.Models[1] != "backup" {
		t.Fatalf("returned value aliases cache storage: %q", second)
	}
	c.Set("key", []byte("replacement"), "new-model")
	replaced, _ := c.Get("key")
	if string(replaced.Body) != "replacement" || len(replaced.Models) != 1 || replaced.Models[0] != "new-model" {
		t.Fatalf("同键替换未同步更新正文和权限: %+v", replaced)
	}
	if missing, ok := c.Get("missing"); ok || missing.Body != nil || missing.Models != nil {
		t.Fatalf("不存在的条目应返回零值: %+v ok=%v", missing, ok)
	}
}
