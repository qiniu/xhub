// Package testsupport holds the fixture plumbing shared by the Go test suites.
//
// It exists because the same block was copied into three packages: resolve a
// DSN, create a throwaway schema, point the connection's search_path at it, and
// drop it on cleanup. Three copies meant three places to fix when the default
// port moved, and it quietly made the suites skip for different reasons.
//
// The package deliberately imports neither iam nor gateway. Every consumer is a
// test inside one of those packages, so importing them back would be an import
// cycle, and the fixture does not need them: it hands back a DSN and lets the
// caller open whatever store it wants.
package testsupport

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// DefaultDatabaseURL 是本地 Compose 测试库；所有调用 Postgres 的测试都要求数据库可用。
// 缺少依赖必须失败，不再以 skip 产生绿色结果；显式 URL 由环境覆盖。
const DefaultDatabaseURL = "postgres://xhub:xhub_dev_password@127.0.0.1:5433/xhub?sslmode=disable"

// DatabaseURL 返回 XHUB_TEST_DATABASE_URL 或本地默认 URL；无参数、无网络副作用。
// 供隔离数据库夹具及其调用方使用；返回值包含凭据，不得打印到测试报告。
func DatabaseURL() string {
	if dsn := os.Getenv("XHUB_TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	return DefaultDatabaseURL
}

// Postgres 为 t 创建 prefix 标识的独立 schema，返回带 search_path 的 URL，并注册清理。
// IAM、Store、Gateway 与后台回归使用此夹具；地址、连接、创建及清理失败均使测试失败，禁止静默 skip。
// 只删除本例 schema；网络操作有超时，错误只报告阶段与类型，避免输出含密码的 DSN。
func Postgres(t *testing.T, prefix string) string {
	t.Helper()
	dsn := DatabaseURL()
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test database URL is invalid; check XHUB_TEST_DATABASE_URL (credentials omitted)")
	}
	schema := fmt.Sprintf("%s_test_%d", prefix, time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("test database connection failed (%T); check XHUB_TEST_DATABASE_URL or local PostgreSQL on 127.0.0.1:5433", err)
	}
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		root.Close(ctx)
		t.Fatalf("create test schema failed (%T)", err)
	}
	// 创建后立即注册清理，随后构建连接串或关闭连接失败也必须回收 schema。
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		c, err := pgx.Connect(cleanupCtx, dsn)
		if err != nil {
			t.Errorf("test schema cleanup connection failed for %s (%T)", schema, err)
			return
		}
		defer func() {
			if err := c.Close(cleanupCtx); err != nil {
				t.Errorf("test schema cleanup close failed for %s (%T)", schema, err)
			}
		}()
		if _, err := c.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("test schema cleanup DROP failed for %s (%T)", schema, err)
		}
	})
	if err := root.Close(ctx); err != nil {
		t.Fatalf("close test schema setup connection failed (%T)", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
