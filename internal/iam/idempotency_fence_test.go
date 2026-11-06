package iam

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestInferenceFenceZeroValueFailsClosed 验证未初始化的占位结果不能被误认为取得资格；无需外部资源或清理。
func TestInferenceFenceZeroValueFailsClosed(t *testing.T) {
	var result InferenceFenceResult
	if result == InferenceFenceAcquired {
		t.Fatal("未初始化的幂等结果不能放行上游")
	}
}

// TestInferenceFenceClaimIndependentOfCleanup 验证清理失败不会阻断新幂等键登记。
// 使用隔离 PostgreSQL 的删除触发器模拟清理故障；只写摘要，schema 在测试结束后自动清理。
func TestInferenceFenceClaimIndependentOfCleanup(t *testing.T) {
	db := testDB(t)
	if _, err := db.Engine.Exec(`CREATE FUNCTION reject_fence_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'cleanup fault'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.Exec(`CREATE TRIGGER reject_fence_delete BEFORE DELETE ON inference_idempotency_fences FOR EACH STATEMENT EXECUTE FUNCTION reject_fence_delete()`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	result, err := db.ClaimInferenceFence(t.Context(), strings.Repeat("d", 64), strings.Repeat("e", 64), now, now.Add(time.Hour))
	if err != nil || result != InferenceFenceAcquired {
		t.Fatalf("清理错误不应阻止新键登记: result=%v err=%v", result, err)
	}
}

// TestCleanupInferenceFencesExpiresOnly 验证后台清理仅删除到期标识、尊重批量限制并拒绝非法参数。
// 使用独立 PostgreSQL 保存测试摘要；断言未过期标识仍被保护，schema 由测试自动清理。
func TestCleanupInferenceFencesExpiresOnly(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	for _, key := range []string{"a", "b", "c"} {
		if _, err := db.ClaimInferenceFence(t.Context(), strings.Repeat(key, 64), strings.Repeat("f", 64), now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Engine.Exec(`UPDATE inference_idempotency_fences SET expires_at = ? WHERE key_hash <> ?`, now.Add(-time.Second), strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CleanupInferenceFences(t.Context(), now, 0); err == nil {
		t.Fatal("清理批量为零应失败")
	}
	if n, err := db.CleanupInferenceFences(t.Context(), now, 1); err != nil || n != 1 {
		t.Fatalf("应仅清理一条过期标识: n=%d err=%v", n, err)
	}
	if n, err := db.CleanupInferenceFences(t.Context(), now, 10); err != nil || n != 1 {
		t.Fatalf("应清理剩余过期标识: n=%d err=%v", n, err)
	}
	result, err := db.ClaimInferenceFence(t.Context(), strings.Repeat("c", 64), strings.Repeat("f", 64), now, now.Add(time.Hour))
	if err != nil || result != InferenceFenceExisting {
		t.Fatalf("未过期标识必须保留: result=%v err=%v", result, err)
	}
}

// TestInferenceFenceClaimsAndExpiry 验证同键并发只允许一个实例取得资格、载荷冲突、过期重用和数据库失败。
// 前置私有 PostgreSQL；只写不可逆摘要，测试库由 testDB 清理，不使用外部身份或供应商。
func TestInferenceFenceClaimsAndExpiry(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	key := strings.Repeat("a", 64)
	payload := strings.Repeat("b", 64)
	const n = 12
	results := make(chan InferenceFenceResult, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := db.ClaimInferenceFence(context.Background(), key, payload, now, now.Add(time.Hour))
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发登记失败: %v", err)
		}
	}
	var acquired, existing int
	for result := range results {
		if result == InferenceFenceAcquired {
			acquired++
		} else if result == InferenceFenceExisting {
			existing++
		} else {
			t.Fatalf("同载荷产生冲突: %v", result)
		}
	}
	if acquired != 1 || existing != n-1 {
		t.Fatalf("并发登记不唯一: acquired=%d existing=%d", acquired, existing)
	}
	if result, err := db.ClaimInferenceFence(t.Context(), key, strings.Repeat("c", 64), now, now.Add(time.Hour)); err != nil || result != InferenceFenceConflict {
		t.Fatalf("不同载荷未冲突: %v %v", result, err)
	}
	if result, err := db.ClaimInferenceFence(t.Context(), key, payload, now.Add(2*time.Hour), now.Add(3*time.Hour)); err != nil || result != InferenceFenceAcquired {
		t.Fatalf("过期标识不能重新使用: %v %v", result, err)
	}
	if _, err := db.ClaimInferenceFence(t.Context(), "raw-secret", payload, now, now.Add(time.Hour)); err == nil {
		t.Fatal("原始标识不应写入数据库")
	}
	if _, err := db.ClaimInferenceFence(t.Context(), key, payload, now, now); err == nil {
		t.Fatal("失效时间不应被接受")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ClaimInferenceFence(ctx, key, payload, now, now.Add(time.Hour)); err == nil {
		t.Fatal("数据库查询取消不能伪造成功")
	}
	rows, err := db.Engine.Context(t.Context()).QueryString(`SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='inference_idempotency_fences' ORDER BY column_name`)
	if err != nil || len(rows) != 3 {
		t.Fatalf("仅应存储三个摘要/过期字段: rows=%v err=%v", rows, err)
	}
}
