package testsupport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestDatabaseURL 验证默认及显式地址选择；参数 t 为测试上下文，无连接；Setenv 自动恢复环境。
func TestDatabaseURL(t *testing.T) {
	t.Setenv("XHUB_TEST_DATABASE_URL", "")
	if DatabaseURL() != DefaultDatabaseURL {
		t.Fatal("缺省地址必须指向本地测试库")
	}
	t.Setenv("XHUB_TEST_DATABASE_URL", "postgres://fixture.invalid/test")
	if DatabaseURL() != "postgres://fixture.invalid/test" {
		t.Fatal("显式测试库地址被忽略")
	}
}

// TestPostgresFailureModes 通过进程内与独立子进程验证地址、连接、创建及清理失败不能当作通过。
// 参数 t 为测试上下文；真实 PostgreSQL 故障共用场景，进程内捕获报告并统计覆盖，子进程确认失败退出。
// 每例只创建独立 schema，外层清除故障遗留；不替换 SQL 结果、不接触线上。
func TestPostgresFailureModes(t *testing.T) {
	if mode := os.Getenv("XHUB_GATE_CHILD"); mode != "" {
		postgresFailureChild(t, mode, Postgres)
		return
	}
	rootDSN := DatabaseURL()
	for _, mode := range []string{"invalid", "invalid_scheme", "unreachable", "create_schema", "cleanup_connect", "cleanup_drop"} {
		for _, execution := range []string{"in_process", "subprocess"} {
			t.Run(mode+"/"+execution, func(t *testing.T) {
				record := filepath.Join(t.TempDir(), "schema")
				// 子进程可能故意阻止自身清理，父进程在结束后回收该进程唯一记录的 schema。
				t.Cleanup(func() {
					raw, err := os.ReadFile(record)
					if os.IsNotExist(err) {
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					c, err := pgx.Connect(ctx, rootDSN)
					if err != nil {
						t.Fatalf("清理故障夹具连接失败: %T", err)
					}
					defer c.Close(ctx)
					if _, err = c.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{string(raw)}.Sanitize()+" CASCADE"); err != nil {
						t.Fatalf("清理故障夹具失败: %T", err)
					}
				})
				expected := map[string]string{
					"invalid":         "test database URL is invalid",
					"invalid_scheme":  "test database URL is invalid",
					"unreachable":     "test database connection failed",
					"create_schema":   "create test schema failed",
					"cleanup_connect": "test schema cleanup connection failed",
					"cleanup_drop":    "test schema cleanup DROP failed",
				}[mode]
				if execution == "in_process" {
					t.Setenv("XHUB_GATE_SCHEMA_RECORD", record)
					reporter := &capturedPostgresReporter{}
					capturePostgresFailure(t, mode, reporter)
					output := strings.Join(reporter.messages, "\n")
					if !strings.Contains(output, expected) {
						t.Fatalf("%s 未报告预期故障 %q: %s", mode, expected, output)
					}
					if strings.Contains(output, "private-fixture-password") {
						t.Fatal("进程内失败报告泄漏数据库凭据")
					}
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPostgresFailureModes$", "-test.v")
				cmd.Env = append(os.Environ(), "XHUB_GATE_CHILD="+mode, "XHUB_GATE_SCHEMA_RECORD="+record)
				output, err := cmd.CombinedOutput()
				if err == nil {
					t.Errorf("%s 被错误地计为通过或跳过:\n%s", mode, output)
				}
				if !strings.Contains(string(output), expected) || ctx.Err() != nil {
					t.Errorf("%s 没有触发预期门禁 %q，不能把夹具启动故障算作覆盖:\n%s", mode, expected, output)
				}
				if strings.Contains(string(output), "private-fixture-password") {
					t.Error("失败消息泄漏测试 DSN 凭据")
				}
			})
		}
	}
}

// postgresFailureChild 为两种执行模式安排真实数据库故障；mode 指定故障，fixture 仅选择报告方式，无返回值。
// 临时代理和锁连接由测试清理关闭；无法删除的 schema 写入记录文件，由外层测试回收。
func postgresFailureChild(t *testing.T, mode string, fixture func(*testing.T, string) string) {
	t.Helper()
	var blocker *pgx.Conn
	// 先注册关闭锁连接，使真实夹具的清理先运行并遇到锁超时；外层兜底删 schema 在解锁后执行。
	t.Cleanup(func() {
		if blocker != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := blocker.Close(ctx); err != nil {
				t.Errorf("释放故障测试锁连接失败: %T", err)
			}
		}
	})
	if mode == "invalid" {
		t.Setenv("XHUB_REGRESSION_STRICT", "1")
		t.Setenv("XHUB_TEST_DATABASE_URL", "postgres://user:private-fixture-password@%zz/test")
	} else if mode == "invalid_scheme" {
		t.Setenv("XHUB_TEST_DATABASE_URL", "https://user:private-fixture-password@localhost/test")
	} else if mode == "unreachable" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		listener.Close()
		t.Setenv("XHUB_REGRESSION_STRICT", "")
		t.Setenv("XHUB_TEST_DATABASE_URL", "postgres://user:private-fixture-password@"+address+"/test?sslmode=disable&connect_timeout=1")
	}
	var proxy net.Listener
	if mode == "cleanup_connect" {
		u, err := url.Parse(DatabaseURL())
		if err != nil {
			t.Fatal(err)
		}
		port := u.Port()
		if port == "" {
			port = "5432"
		}
		upstream := net.JoinHostPort(u.Hostname(), port)
		proxy, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go forwardPostgres(proxy, upstream)
		t.Cleanup(func() { _ = proxy.Close() })
		u.Host = proxy.Addr().String()
		t.Setenv("XHUB_TEST_DATABASE_URL", u.String())
	}
	if mode == "cleanup_drop" || mode == "create_schema" {
		u, err := url.Parse(DatabaseURL())
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if mode == "cleanup_drop" {
			q.Set("lock_timeout", "100ms")
		} else {
			q.Set("default_transaction_read_only", "on")
		}
		u.RawQuery = q.Encode()
		t.Setenv("XHUB_TEST_DATABASE_URL", u.String())
	}
	dsn := fixture(t, "gate_failure")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("XHUB_GATE_SCHEMA_RECORD"), []byte(u.Query().Get("search_path")), 0600); err != nil {
		t.Fatal(err)
	}
	if proxy != nil {
		proxy.Close()
	}
	if mode == "cleanup_drop" {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		blocker = c
		if _, err = c.Exec(context.Background(), "CREATE TABLE cleanup_probe(id int); BEGIN; LOCK TABLE cleanup_probe IN ACCESS SHARE MODE"); err != nil {
			t.Fatal(err)
		}
		// 不提前释放锁：让夹具注册的 DROP 确实遭遇 lock_timeout，再由外层清理关闭连接。
	}
}

// capturedPostgresReporter 仅捕获测试报告与清理钩子，不模拟数据库或 SQL 执行结果。
// 子进程测试继续验证 testing.T 的非零退出；此报告器让同样的真实故障分支计入当前进程覆盖率。
type capturedPostgresReporter struct {
	messages []string
	cleanups []func()
}

// Helper 满足夹具报告接口；捕获器没有 testing.T 的调用栈需要调整，无参数及副作用。
func (*capturedPostgresReporter) Helper() {}

// Errorf 保存夹具真实产生的失败报告；参数为格式与值，无返回值，不改变数据库结果。
func (r *capturedPostgresReporter) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// Fatalf 保存失败并中止本次夹具；专用标记只由 capturePostgresFailure 恢复，不吞掉其他 panic。
func (r *capturedPostgresReporter) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	panic(r)
}

// Cleanup 记录夹具清理操作；参数为真实回调，供捕获函数按 testing.T 的逆序语义执行，无返回值。
func (r *capturedPostgresReporter) Cleanup(fn func()) { r.cleanups = append(r.cleanups, fn) }

// capturePostgresFailure 在当前进程运行真实故障夹具，参数为测试、故障类型及报告器，无返回值。
// 仅恢复报告器的预期致命标记，保留真正 panic；随后执行真实清理，便于断言失败报告和覆盖计数。
func capturePostgresFailure(t *testing.T, mode string, reporter *capturedPostgresReporter) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil && recovered != reporter {
			panic(recovered)
		}
		for i := len(reporter.cleanups) - 1; i >= 0; i-- {
			reporter.cleanups[i]()
		}
	}()
	postgresFailureChild(t, mode, func(_ *testing.T, prefix string) string {
		return postgresFixture(reporter, prefix)
	})
}

// forwardPostgres 将本地代理连接转发给隔离测试库；参数为监听器和目标地址，无返回值。
// 供清理重连失败用例使用；停止监听后不再接受连接，已完成连接会关闭两个方向。
func forwardPostgres(listener net.Listener, upstream string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			target, err := net.DialTimeout("tcp", upstream, time.Second)
			if err != nil {
				return
			}
			defer target.Close()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(target, conn); target.Close(); close(done) }()
			_, _ = io.Copy(conn, target)
			conn.Close()
			<-done
		}()
	}
}

// TestPostgresLifecycle 验证真实 schema 创建、search_path 隔离和测试结束删除；参数 t 为测试上下文。
// 使用本地测试库，不依赖供应商；子测试清理后独立连接确认 schema 不再存在。
func TestPostgresLifecycle(t *testing.T) {
	var schema string
	t.Run("fixture", func(t *testing.T) {
		dsn := Postgres(t, "gate_lifecycle")
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		schema = u.Query().Get("search_path")
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close(context.Background())
		var current string
		if err = c.QueryRow(context.Background(), "SELECT current_schema()").Scan(&current); err != nil || current != schema {
			t.Fatalf("schema 隔离错误: %v", err)
		}
		if _, err = c.Exec(context.Background(), "CREATE TABLE lifecycle_probe(id int)"); err != nil {
			t.Fatal(err)
		}
	})
	c, err := pgx.Connect(context.Background(), DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var exists bool
	if err = c.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname=$1)", schema).Scan(&exists); err != nil || exists {
		t.Fatalf("schema 清理未生效: exists=%v err=%v", exists, err)
	}
}
