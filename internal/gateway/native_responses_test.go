package gateway

import (
	"github.com/sunqirui1987/xhub/cmd/regression/testsupport"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/store"
	"testing"
)

// TestNativeResponsesSettingsSnapshot 验证DB开关覆盖YAML、删除后恢复启动配置、存储故障失败关闭。
// 参数t为测试上下文；使用隔离schema，连接和schema均自动回收，不修改生产配置。
func TestNativeResponsesSettingsSnapshot(t *testing.T) {
	st, err := store.Open(testsupport.Postgres(t, "native_settings"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Engine.Close() })
	server := &Server{Cfg: &config.Config{GeneralRaw: map[string]any{"native_responses": map[string]any{"enabled": true, "user_ids": []string{"u"}}}}, Store: st}
	if !server.NativeResponsesAllowed("u") || server.NativeResponsesAllowed("other") {
		t.Fatal("启动名单错误")
	}
	if err := st.PutConfig("general_settings", "native_responses", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	if server.NativeResponsesAllowed("u") {
		t.Fatal("DB紧急关闭未覆盖YAML")
	}
	if err := st.DeleteConfig("general_settings", "native_responses"); err != nil {
		t.Fatal(err)
	}
	if !server.NativeResponsesAllowed("u") {
		t.Fatal("删除后未恢复YAML")
	}
	_ = st.Engine.Close()
	if server.NativeResponsesAllowed("u") {
		t.Fatal("配置读取失败仍开启")
	}
	if (&Server{}).NativeResponsesAllowed("u") {
		t.Fatal("空宿主误开启")
	}
}
