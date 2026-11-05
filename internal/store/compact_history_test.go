package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCompactHistoryPersistence 验证真实 PostgreSQL 中历史去重、顺序和调用方隔离；使用独立 schema 并自动清理。
func TestCompactHistoryPersistence(t *testing.T) {
	t.Setenv("XHUB_COMPACT_HISTORY", "1")
	s, _ := openModelWeightStores(t)
	ctx := t.Context()
	a := `{"Role":"user","Text":"` + strings.Repeat("same long content ", 200) + `"}`
	b := `{"Role":"assistant","Native":{"encrypted_content":"signed-opaque","call_id":"tool-1"}}`
	first, second := "["+a+","+b+"]", "["+a+","+b+","+a+"]"
	if err := s.WriteHistory(ctx, "h1", "scope-a", first); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteHistory(ctx, "h2", "scope-a", second); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadHistory(ctx, "h2", "scope-a"); err != nil || got != second {
		t.Fatalf("历史还原错误: %q %v", got, err)
	}
	var blocks int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM conversation_history_blocks`).Scan(&blocks); err != nil || blocks != 2 {
		t.Fatalf("重复消息未去重: %d %v", blocks, err)
	}
	if _, err := s.ReadHistory(ctx, "h2", "scope-b"); err == nil {
		t.Fatal("跨作用域读取成功")
	}
	if err := s.WriteHistory(ctx, "h3", "scope-b", first); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM conversation_history_blocks`).Scan(&blocks); err != nil || blocks != 4 {
		t.Fatalf("不同作用域共用内容块: %d %v", blocks, err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM conversation_history_blocks WHERE scope=$1`, "scope-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadHistory(ctx, "h2", "scope-a"); err == nil {
		t.Fatal("缺失内容块被当成完整历史")
	}
	if err := s.WriteHistory(ctx, "bad", "scope-a", "broken-json"); err == nil {
		t.Fatal("损坏历史被接受")
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ReadHistory(c, "h3", "scope-b"); err == nil {
		t.Fatal("取消的读取被当成成功")
	}
}

// TestCompactHistoryLegacyMigration 验证旧数据安全迁移、重入和降级写入；独立 schema 由测试夹具清理。
func TestCompactHistoryLegacyMigration(t *testing.T) {
	s, _ := openModelWeightStores(t)
	ctx := t.Context()
	keys := []string{"session_route:v1:history:a", "session_route:v1:history:b"}
	for i, key := range keys {
		if err := s.WriteConversation(ctx, key, fmt.Sprintf(`[{"turn":%d}]`, i)); err != nil {
			t.Fatal(err)
		}
	}
	next, n, err := s.MigrateLegacyHistories(ctx, "", 1)
	if err != nil || n != 1 || next != keys[0] {
		t.Fatalf("首批迁移失败: %d %q %v", n, next, err)
	}
	if got, err := s.ReadHistory(ctx, keys[0], "current-scope"); err != nil || got != `[{"turn":0}]` {
		t.Fatalf("旧数据不可读: %q %v", got, err)
	}
	if got, err := s.ReadConversation(ctx, keys[0]); err != nil || got != `[{"turn":0}]` {
		t.Fatal("旧副本未保留", got, err)
	}
	_, n, err = s.MigrateLegacyHistories(ctx, next, 1)
	if err != nil || n != 1 {
		t.Fatalf("续跑失败: %d %v", n, err)
	}
	_, n, err = s.MigrateLegacyHistories(ctx, "", 5)
	if err != nil || n != 0 {
		t.Fatalf("重跑重复迁移: %d %v", n, err)
	}
	t.Setenv("XHUB_COMPACT_HISTORY", "0")
	value := `[{"turn":"updated"}]`
	if err := s.WriteHistory(ctx, keys[0], "current-scope", value); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadHistory(ctx, keys[0], "current-scope")
	if err != nil || got != value {
		t.Fatal("旧格式回退读取了过期新记录", got, err)
	}
	var decoded []any
	if json.Unmarshal([]byte(got), &decoded) != nil {
		t.Fatal("历史无效")
	}
}

// TestCompactHistoryStorageGrowth 测量多轮长会话的真实数据库占用；数据为随机合成文本，私有 schema 自动清理。
func TestCompactHistoryStorageGrowth(t *testing.T) {
	t.Setenv("XHUB_COMPACT_HISTORY", "1")
	s, _ := openModelWeightStores(t)
	ctx := t.Context()
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE synthetic_legacy_history (body TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	rnd := rand.New(rand.NewSource(74))
	var items []json.RawMessage
	var oldBytes int
	start := time.Now()
	for i := 0; i < 30; i++ {
		raw := make([]byte, 4096)
		if _, err := rnd.Read(raw); err != nil {
			t.Fatal(err)
		}
		item, err := json.Marshal(map[string]any{"role": "user", "content": hex.EncodeToString(raw)})
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
		history, err := json.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		oldBytes += len(history)
		if _, err = s.DB.ExecContext(ctx, `INSERT INTO synthetic_legacy_history(body) VALUES($1)`, string(history)); err != nil {
			t.Fatal(err)
		}
		if err = s.WriteHistory(ctx, fmt.Sprintf("test-%d", i), "isolated-scope", string(history)); err != nil {
			t.Fatal(err)
		}
	}
	var payload, manifests, blocks int
	var legacyPhysical int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(sum(pg_column_size(body)),0) FROM synthetic_legacy_history`).Scan(&legacyPhysical); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT (SELECT COALESCE(sum(octet_length(payload)),0) FROM conversation_history_blocks), (SELECT COALESCE(sum(octet_length(digests::text)),0) FROM conversation_history_manifests), (SELECT count(*) FROM conversation_history_blocks)`).Scan(&payload, &manifests, &blocks); err != nil {
		t.Fatal(err)
	}
	if blocks != 30 || payload+manifests >= oldBytes/3 {
		t.Fatalf("长会话去重无效: blocks=%d stored=%d legacy=%d", blocks, payload+manifests, oldBytes)
	}
	t.Logf("合成 30 轮：旧格式逻辑=%d bytes，旧字段物理占用=%d bytes，新内容块及有序引用=%d bytes，消息块=%d，写入耗时=%s；未计表/索引开销", oldBytes, legacyPhysical, payload+manifests, blocks, time.Since(start))
}

// TestCompactHistoryEncodingBoundaries 验证空列表、损坏压缩块和内容摘要；纯内存测试，无需清理。
func TestCompactHistoryEncodingBoundaries(t *testing.T) {
	blocks, ids, err := encodeHistory(`[]`)
	if err != nil || len(blocks) != 0 || len(ids) != 0 {
		t.Fatal(blocks, ids, err)
	}
	for _, input := range []string{`null`, `{}`, `[`, ``} {
		if _, _, err := encodeHistory(input); err == nil {
			t.Fatalf("非法历史未拒绝: %q", input)
		}
	}
	blocks, _, err = encodeHistory(`["` + strings.Repeat("a", 4000) + `"]`)
	if err != nil || !blocks[0].Compressed {
		t.Fatal("大块未压缩", err)
	}
	b := blocks[0]
	if _, err = decodeHistoryBlock(b); err != nil {
		t.Fatal(err)
	}
	b.Payload = []byte("corrupt")
	if _, err = decodeHistoryBlock(b); err == nil {
		t.Fatal("损坏压缩内容被接受")
	}
	b = blocks[0]
	b.Digest = strings.Repeat("0", 64)
	if _, err = decodeHistoryBlock(b); err == nil {
		t.Fatal("错误摘要被接受")
	}
	b = blocks[0]
	b.Size--
	if _, err = decodeHistoryBlock(b); err == nil {
		t.Fatal("错误长度被接受")
	}
}

// TestCompactMigrationConcurrentWrite 验证迁移与同一响应的实时新写入并发时不能覆盖新值；隔离 schema 自动清理。
func TestCompactMigrationConcurrentWrite(t *testing.T) {
	t.Setenv("XHUB_COMPACT_HISTORY", "1")
	s, _ := openModelWeightStores(t)
	ctx := t.Context()
	for i := 0; i < 12; i++ {
		key := fmt.Sprintf("session_route:v1:history:race-%02d", i)
		if err := s.WriteConversation(ctx, key, `[{"value":"old"}]`); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		go func() { _, _, err := s.MigrateLegacyHistories(ctx, "", 1000); errs <- err }()
		go func() { errs <- s.WriteHistory(ctx, key, "current-scope", `[{"value":"new"}]`) }()
		for j := 0; j < 2; j++ {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		value, err := s.ReadHistory(ctx, key, "current-scope")
		if err != nil || value != `[{"value":"new"}]` {
			t.Fatalf("迁移覆盖了最新响应: %q %v", value, err)
		}
	}
}

// TestCompactMigrationRejectsInvalidLegacy 验证无效旧历史不生成成功索引并可安全重试；独立 schema 自动清理。
func TestCompactMigrationRejectsInvalidLegacy(t *testing.T) {
	s, _ := openModelWeightStores(t)
	ctx := t.Context()
	key := "session_route:v1:history:bad"
	if err := s.WriteConversation(ctx, key, `not-json`); err != nil {
		t.Fatal(err)
	}
	_, count, err := s.MigrateLegacyHistories(ctx, "", 10)
	if err == nil || count != 0 {
		t.Fatalf("错误旧数据被迁移: %d %v", count, err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM conversation_history_manifests`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("失败仍写入索引: %d %v", n, err)
	}
	got, err := s.ReadConversation(ctx, key)
	if err != nil || got != "not-json" {
		t.Fatal("旧副本被更改", got, err)
	}
}

// TestCompactConcurrentSharedBlocks 验证不同响应并发写入相同消息块时保持顺序并避免相反加锁顺序；私有 schema 自动清理。
func TestCompactConcurrentSharedBlocks(t *testing.T) {
	t.Setenv("XHUB_COMPACT_HISTORY", "1")
	s, _ := openModelWeightStores(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := `[{"v":"a"},{"v":"b"}]`
			if i%2 == 1 {
				value = `[{"v":"b"},{"v":"a"}]`
			}
			key := fmt.Sprintf("parallel-%d", i)
			if err := s.WriteHistory(ctx, key, "same-scope", value); err != nil {
				t.Errorf("并发写入失败: %v", err)
				return
			}
			got, err := s.ReadHistory(ctx, key, "same-scope")
			if err != nil || got != value {
				t.Errorf("并发还原结果不正确: %q %v", got, err)
			}
		}(i)
	}
	wg.Wait()
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM conversation_history_blocks`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("并发产生重复内容块: %d %v", count, err)
	}
}

// TestCompactHistoryLargeItem 验证大消息的无损压缩及真实 PostgreSQL 往返；合成随机输入和私有 schema 自动清理。
func TestCompactHistoryLargeItem(t *testing.T) {
	t.Setenv("XHUB_COMPACT_HISTORY", "1")
	s, _ := openModelWeightStores(t)
	rnd := rand.New(rand.NewSource(89))
	raw := make([]byte, 8<<20)
	if _, err := rnd.Read(raw); err != nil {
		t.Fatal(err)
	}
	history, err := json.Marshal([]map[string]string{{"role": "user", "content": hex.EncodeToString(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = s.WriteHistory(t.Context(), "large", "scope", string(history)); err != nil {
		t.Fatal(err)
	}
	writeTime := time.Since(start)
	start = time.Now()
	got, err := s.ReadHistory(t.Context(), "large", "scope")
	if err != nil || got != string(history) {
		t.Fatalf("大历史还原失败: %v", err)
	}
	t.Logf("合成 %d 字节历史：写入=%s 读取=%s", len(history), writeTime, time.Since(start))
}
