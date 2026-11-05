package iam

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

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
