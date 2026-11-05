package store

import (
	"context"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/cmd/regression/testsupport"
)

// TestFreshConfigValueContext 验证真实 SQL 读取尊重取消/超时，并且失败后连接仍可读取当前值。
// 使用私有 schema 的表锁阻塞读取；事务及连接在测试结束回收，不碰生产数据。
func TestFreshConfigValueContext(t *testing.T) {
	st, err := Open(testsupport.Postgres(t, "config_context"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Engine.Close() })
	if err := st.PutConfig("general_settings", "capture", true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := st.FreshConfigValueContext(ctx, "general_settings", "capture"); err == nil {
		t.Fatal("已取消的配置读取未失败")
	}
	tx := st.Engine.NewSession()
	defer tx.Close()
	if err := tx.Begin(); err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("LOCK TABLE proxy_config IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, _, err = st.FreshConfigValueContext(ctx, "general_settings", "capture")
	if err == nil {
		t.Fatal("持锁查询没有按 deadline 失败")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	value, found, err := st.FreshConfigValueContext(t.Context(), "general_settings", "capture")
	if err != nil || !found || value != true {
		t.Fatalf("超时后读取失败: value=%v found=%v err=%v", value, found, err)
	}
}

// TestFreshConfigValue 验证不存在、合法值、绕过缓存的外部更新、坏JSON和断库；隔离schema及连接由cleanup回收。
func TestFreshConfigValue(t *testing.T) {
	st, err := Open(testsupport.Postgres(t, "fresh_config"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Engine.Close() })
	if _, found, err := st.FreshConfigValue("general_settings", "test"); err != nil || found {
		t.Fatal("缺失键读取错误")
	}
	if err := st.PutConfig("general_settings", "test", true); err != nil {
		t.Fatal(err)
	}
	if value, found, err := st.FreshConfigValue("general_settings", "test"); err != nil || !found || value != true {
		t.Fatal("合法值读取错误")
	}
	if _, err := st.Engine.Exec("UPDATE proxy_config SET value_json = ? WHERE namespace = ? AND key = ?", "false", "general_settings", "test"); err != nil {
		t.Fatal(err)
	}
	if value, _, err := st.FreshConfigValue("general_settings", "test"); err != nil || value != false {
		t.Fatal("外部关闭未立即生效")
	}
	if _, err := st.Engine.Exec("UPDATE proxy_config SET value_json = ? WHERE namespace = ? AND key = ?", "bad-json", "general_settings", "test"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.FreshConfigValue("general_settings", "test"); err == nil || !found {
		t.Fatal("坏配置被当作缺失")
	}
	_ = st.Engine.Close()
	if _, _, err := st.FreshConfigValue("general_settings", "test"); err == nil {
		t.Fatal("断库未返回错误")
	}
	if _, _, err := (*Store)(nil).FreshConfigValue("general_settings", "test"); err == nil {
		t.Fatal("空存储未返回错误")
	}
}
