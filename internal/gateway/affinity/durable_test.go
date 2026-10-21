package affinity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// memoryConversation 提供确定性的持久边界替身；多个State共享它，清理由测试内存回收。
type memoryConversation struct {
	mu   sync.Mutex
	rows map[string]string
	fail bool
}

// ReadConversation 读取已存值或注入失败；参数为测试键，返回值不随State重建丢失。
func (m *memoryConversation) ReadConversation(_ context.Context, k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return "", errors.New("offline")
	}
	return m.rows[k], nil
}

// CompareConversation 原子选出首次写入赢家；模拟数据库CAS，不模拟Redis续期。
func (m *memoryConversation) CompareConversation(_ context.Context, k, o, v string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return "", errors.New("offline")
	}
	if m.rows[k] == o {
		m.rows[k] = v
	}
	return m.rows[k], nil
}

// WriteConversation 保存历史内容；失败模式不写入，测试结束后回收。
func (m *memoryConversation) WriteConversation(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("offline")
	}
	m.rows[k] = v
	return nil
}

// TestDurableConversationSurvivesCacheLoss 验证四类续聊状态跨缓存清空/重建仍在，偏好仍可丢弃。
// 前置内存持久替身，覆盖读、写、CAS及失败不降级；无网络和外部数据需清理。
func TestDurableConversationSurvivesCacheLoss(t *testing.T) {
	db := &memoryConversation{rows: map[string]string{}}
	s := NewDurable(nil, db)
	for _, kind := range []string{"owner", "response", "history", "messages"} {
		k := "session_route:v1:" + kind + ":test"
		if err := s.cacheWrite(k, "original"); err != nil {
			t.Fatal(err)
		}
		s = NewDurable(nil, db)
		got, _, err := s.cacheRead(k, false)
		if err != nil || got != "original" {
			t.Fatalf("重建丢失%s: %q %v", kind, got, err)
		}
		got, err = s.cacheCompare(k, "", "other", false)
		if err != nil || got != "original" {
			t.Fatalf("覆盖归属: %q %v", got, err)
		}
	}
	if durableKey("session_route:v1:preference:test") || durableKey("") {
		t.Fatal("错误持久化短期偏好")
	}
	db.fail = true
	k := "session_route:v1:owner:test"
	s.affinitySetFor(k, "stale", time.Hour)
	if _, _, err := s.cacheRead(k, true); err == nil {
		t.Fatal("数据库失败降级为旧记录")
	}
	if _, err := s.cacheCompare(k, "", "new", false); err == nil {
		t.Fatal("忽略CAS失败")
	}
	if err := s.cacheWrite(k, "new"); err == nil {
		t.Fatal("忽略写失败")
	}
}

// TestDurableConversationImportsLiveLegacy 验证升级只导入尚有效的旧归属，不复活过期状态。
// 前置旧进程缓存，核对导入后重建可用及过期未命中；数据由测试回收。
func TestDurableConversationImportsLiveLegacy(t *testing.T) {
	db := &memoryConversation{rows: map[string]string{}}
	s := NewDurable(nil, db)
	k := "session_route:v1:owner:legacy"
	s.affinitySetFor(k, "original", time.Hour)
	got, _, err := s.cacheRead(k, false)
	if err != nil || got != "original" {
		t.Fatal(got, err)
	}
	s = NewDurable(nil, db)
	got, _, err = s.cacheRead(k, false)
	if err != nil || got != "original" {
		t.Fatal(got, err)
	}
	k = "session_route:v1:owner:expired"
	s.affinitySetFor(k, "expired", -time.Hour)
	got, _, err = s.cacheRead(k, false)
	if err != nil || got != "" {
		t.Fatal("过期记录被复活", got, err)
	}
}
