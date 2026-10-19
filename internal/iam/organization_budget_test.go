package iam

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestOrganizationBudgetConfiguration 前置内存组织；验证固定周期、非法值、重复设置、取消及修改不退回消费，无持久化资源。
func TestOrganizationBudgetConfiguration(t *testing.T) {
	now := time.Date(2026, 1, 31, 17, 0, 0, 0, time.FixedZone("CST", 8*3600))
	for _, tc := range []struct {
		value string
		hours int
		bad   bool
	}{{"", 0, false}, {"24h", 24, false}, {"7d", 168, false}, {"30d", 720, false}, {"monthly", 0, true}, {"1d", 0, true}, {"-7d", 0, true}} {
		o := Organization{Spend: 9}
		err := configureOrganizationBudget(&o, tc.value, now)
		if tc.bad {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("非法周期被接受: %q %v", tc.value, err)
			}
			continue
		}
		if err != nil || o.Spend != 9 {
			t.Fatalf("设置改变消费: %+v %v", o, err)
		}
		if tc.hours > 0 {
			want := now.Add(time.Duration(tc.hours) * time.Hour)
			if o.BudgetResetAt == nil || !o.BudgetResetAt.Equal(want) {
				t.Fatalf("周期不是固定时长: %+v", o)
			}
			if err := configureOrganizationBudget(&o, tc.value, now.Add(time.Hour)); err != nil || !o.BudgetResetAt.Equal(want) {
				t.Fatal("重复保存推迟周期")
			}
		}
		if err := configureOrganizationBudget(&o, "", now); err != nil || o.BudgetResetAt != nil || o.Spend != 9 {
			t.Fatal("取消周期改变余额或未取消")
		}
	}
}

// TestOrganizationBudgetResetPersistence 前置隔离 PostgreSQL；并发跨期重置，核对审计、晚到及重放账单和回读。
// 异常审计约束验证整体回滚；testDB 清理 schema，不依赖外部模型。
func TestOrganizationBudgetResetPersistence(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	limit := 10.0
	o, err := db.CreateOrgWithConfig(ctx, Actor{}, "periodic", &limit, OrgConfig{BudgetDuration: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if _, err = db.Engine.Exec("UPDATE organizations SET spend=10,budget_reset_at=? WHERE id=?", anchor, o.ID); err != nil {
		t.Fatal(err)
	}
	now := anchor.Add(49 * time.Hour)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.ResetOrganizationBudgets(ctx, now); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := db.GetOrg(ctx, o.ID)
	if err != nil || got.Spend != 0 || got.BudgetPeriodStart == nil || !got.BudgetPeriodStart.Equal(anchor.Add(48*time.Hour)) || !got.BudgetResetAt.Equal(anchor.Add(72*time.Hour)) {
		t.Fatalf("跨期推进错误: %+v %v", got, err)
	}
	audits, err := db.Engine.Where("action=? AND object_id=?", "org.budget_reset", o.ID).Count(&AuditEntry{})
	if err != nil || audits != 1 {
		t.Fatalf("并发重复充值或审计: %d %v", audits, err)
	}
	for _, tc := range []struct {
		id   string
		ts   time.Time
		cost float64
	}{{"late", anchor.Add(-time.Minute), 3}, {"current", now, 2}} {
		r := usageRecord(tc.id, "", tc.cost)
		r.OrganizationID = o.ID
		r.TS = tc.ts
		for range 2 {
			if err := db.RecordUsage(ctx, []UsageRecord{r}); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err = db.GetOrg(ctx, o.ID)
	if err != nil || got.Spend != 2 {
		t.Fatalf("旧账单或重放污染周期: %+v %v", got, err)
	}
	var total struct{ Cost float64 }
	if _, err := db.Engine.SQL("SELECT SUM(cost) AS cost FROM usage_events WHERE organization_id=?", o.ID).Get(&total); err != nil || total.Cost != 5 {
		t.Fatalf("历史账单丢失: %+v %v", total, err)
	}
	if _, err = db.Engine.Exec("ALTER TABLE audit_logs ADD CONSTRAINT reject_budget_reset CHECK (action <> 'org.budget_reset') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	next := *got.BudgetResetAt
	if err = db.ResetOrganizationBudgets(ctx, next); err == nil {
		t.Fatal("审计失败仍假报成功")
	}
	var unchanged Organization
	if _, err = db.Engine.ID(o.ID).Get(&unchanged); err != nil || unchanged.Spend != 2 || !unchanged.BudgetResetAt.Equal(next) {
		t.Fatalf("失败未回滚: %+v %v", unchanged, err)
	}
	if _, err = db.Engine.Exec("ALTER TABLE audit_logs DROP CONSTRAINT reject_budget_reset"); err != nil {
		t.Fatal(err)
	}
	if err = db.ResetOrganizationBudgets(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.ResetOrganizationBudgets(ctx, next); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetOrg(ctx, o.ID)
	if err != nil || got.Spend != 0 || got.BudgetDuration != "24h" {
		t.Fatalf("重启/重复执行丢失状态: %+v %v", got, err)
	}
	// 重新建立生产 Open 连接，验证状态由持久化与启动迁移恢复，而非复用内存对象。
	reopened, err := Open(ctx, db.Engine.DataSourceName())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.GetOrg(ctx, o.ID)
	if err != nil || restored.BudgetDuration != got.BudgetDuration || restored.Spend != got.Spend || !restored.BudgetResetAt.Equal(*got.BudgetResetAt) {
		t.Fatalf("新进程启动回读丢失周期: %+v %v", restored, err)
	}
}

// TestOrganizationBudgetUpgrade 前置旧组织及已用额度；真实迁移两次补列且默认不充值，testDB 清理 schema。
func TestOrganizationBudgetUpgrade(t *testing.T) {
	db := testDB(t)
	o, err := db.CreateOrg(t.Context(), Actor{}, "old", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Engine.Exec("UPDATE organizations SET spend=4 WHERE id=?", o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Engine.Exec("ALTER TABLE organizations DROP COLUMN budget_duration, DROP COLUMN budget_reset_at, DROP COLUMN budget_period_start"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = db.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.GetOrg(t.Context(), o.ID)
	if err != nil || got.Spend != 4 || got.BudgetDuration != "" || got.BudgetResetAt != nil || got.BudgetPeriodStart != nil {
		t.Fatalf("旧库补列破坏数据: %+v %v", got, err)
	}
}
