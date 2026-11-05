// history-compact 以小批次复制旧 Responses 历史，保留旧副本以便回退。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sunqirui1987/xhub/internal/store"
)

// run 执行一批迁移；参数为控制台参数和数据库地址，返回进度游标及错误。
// 调用方必须显式传入 --execute；不打印数据库地址、请求正文或认证材料。
func run(args []string, dsn string) (string, error) {
	fs := flag.NewFlagSet("history-compact", flag.ContinueOnError)
	execute := fs.Bool("execute", false, "explicitly run a migration batch")
	noLegacyWriters := fs.Bool("confirm-no-legacy-writers", false, "confirm every running gateway understands the new format")
	limit := fs.Int("limit", 50, "maximum records per batch (1-1000)")
	after := fs.String("after", "", "resume after this history key")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments")
	}
	if !*execute {
		return "", fmt.Errorf("migration requires --execute")
	}
	if !*noLegacyWriters {
		return "", fmt.Errorf("migration requires --confirm-no-legacy-writers")
	}
	if dsn == "" {
		return "", fmt.Errorf("XHUB_DATABASE_URL is required")
	}
	if *limit < 1 || *limit > 1000 {
		return "", fmt.Errorf("limit must be between 1 and 1000")
	}
	st, err := store.Open(dsn)
	if err != nil {
		return "", fmt.Errorf("database unavailable")
	}
	defer st.Engine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	next, count, err := st.MigrateLegacyHistories(ctx, *after, *limit)
	result := fmt.Sprintf("migrated=%d next=%s", count, next)
	if err != nil {
		return result, fmt.Errorf("batch failed; old records remain available")
	}
	return result, nil
}

// main 读取专用环境变量并执行一批迁移；失败以非零退出码表示，供人工续跑。
func main() {
	out, err := run(os.Args[1:], os.Getenv("XHUB_DATABASE_URL"))
	if out != "" {
		fmt.Println(out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
