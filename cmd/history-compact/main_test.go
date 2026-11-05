package main

import (
	"github.com/sunqirui1987/xhub/cmd/regression/testsupport"
	"github.com/sunqirui1987/xhub/internal/store"
	"strings"
	"testing"
)

// TestRunRequiresExplicitInputs 确认迁移需显式执行和连接地址；无外部依赖且不创建数据。
func TestRunRequiresExplicitInputs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		dsn  string
	}{{nil, ""}, {[]string{"--execute"}, ""}, {[]string{"--execute", "--confirm-no-legacy-writers", "--limit=0"}, "postgres://placeholder"}, {[]string{"unexpected"}, "postgres://placeholder"}} {
		if _, err := run(tc.args, tc.dsn); err == nil {
			t.Fatalf("缺少安全参数却被接受: %v", tc.args)
		}
	}
}

// TestRunMigratesOneBatch 验证工具连接真实隔离数据库、分批迁移并保留旧副本；夹具清理 schema。
func TestRunMigratesOneBatch(t *testing.T) {
	dsn := testsupport.Postgres(t, "history_cli")
	s, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Engine.Close() })
	key := "session_route:v1:history:fixture"
	want := `[{"role":"user","content":"Hello"}]`
	if err = s.WriteConversation(t.Context(), key, want); err != nil {
		t.Fatal(err)
	}
	result, err := run([]string{"--execute", "--confirm-no-legacy-writers", "--limit=1"}, dsn)
	if err != nil || !strings.Contains(result, "migrated=1") {
		t.Fatal(result, err)
	}
	got, err := s.ReadHistory(t.Context(), key, "fixture-scope")
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	old, err := s.ReadConversation(t.Context(), key)
	if err != nil || old != want {
		t.Fatal("旧副本丢失", old, err)
	}
	result, err = run([]string{"--execute", "--confirm-no-legacy-writers", "--limit=1"}, dsn)
	if err != nil || !strings.Contains(result, "migrated=0") {
		t.Fatal(result, err)
	}
}
