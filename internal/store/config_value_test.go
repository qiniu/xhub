package store

import (
	"github.com/sunqirui1987/xhub/cmd/regression/testsupport"
	"testing"
)

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
