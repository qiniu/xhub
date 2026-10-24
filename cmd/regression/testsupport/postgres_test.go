package testsupport

import (
	"context"
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

// TestPostgresFailureModes 在独立测试进程复现地址、连接及清理失败被当作通过的问题。
// 参数 t 为测试上下文；正常分支需要隔离 PostgreSQL，子进程只创建本例 schema；父进程确认失败退出且清除故障遗留，未接触线上。
func TestPostgresFailureModes(t *testing.T) {
	if mode := os.Getenv("XHUB_GATE_CHILD"); mode != "" {
		postgresFailureChild(t, mode)
		return
	}
	rootDSN := DatabaseURL()
	for _, mode := range []string{"invalid", "invalid_scheme", "unreachable", "cleanup_connect", "cleanup_drop"} {
		t.Run(mode, func(t *testing.T) {
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
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPostgresFailureModes$", "-test.v")
			cmd.Env = append(os.Environ(), "XHUB_GATE_CHILD="+mode, "XHUB_GATE_SCHEMA_RECORD="+record)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Errorf("%s 被错误地计为通过或跳过:\n%s", mode, output)
			}
			expected := map[string]string{
				"invalid":         "test database URL is invalid",
				"invalid_scheme":  "test database URL is invalid",
				"unreachable":     "test database connection failed",
				"cleanup_connect": "test schema cleanup connection failed",
				"cleanup_drop":    "test schema cleanup DROP failed",
			}[mode]
			if !strings.Contains(string(output), expected) || ctx.Err() != nil {
				t.Errorf("%s 没有触发预期门禁 %q，不能把夹具启动故障算作覆盖:\n%s", mode, expected, output)
			}
			if strings.Contains(string(output), "private-fixture-password") {
				t.Error("失败消息泄漏测试 DSN 凭据")
			}
		})
	}
}

// postgresFailureChild 仅由上方测试的子进程调用；mode 指定可逆故障，无返回值。
// 临时代理/阻塞连接由进程退出关闭；故意无法删除的 schema 写入父进程记录文件，由父进程负责回收。
func postgresFailureChild(t *testing.T, mode string) {
	t.Helper()
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
		u.Host = proxy.Addr().String()
		t.Setenv("XHUB_TEST_DATABASE_URL", u.String())
	}
	if mode == "cleanup_drop" {
		u, err := url.Parse(DatabaseURL())
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("lock_timeout", "100ms")
		u.RawQuery = q.Encode()
		t.Setenv("XHUB_TEST_DATABASE_URL", u.String())
	}
	dsn := Postgres(t, "gate_failure")
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
		if _, err = c.Exec(context.Background(), "CREATE TABLE cleanup_probe(id int); BEGIN; LOCK TABLE cleanup_probe IN ACCESS SHARE MODE"); err != nil {
			t.Fatal(err)
		}
		// 不提前释放锁：让注册的 DROP 确实遭遇 lock_timeout，连接在本子进程退出时关闭。
	}
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
