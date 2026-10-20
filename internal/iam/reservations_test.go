package iam

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

// TestBudgetReservationsAtomic 验证两个数据库实例同时预占时只有可支付的请求成功，项目也参与同一原子限制。
// 前置私有 PostgreSQL schema；八请求并发、重复/冲突/非法输入、释放及结算回滚均有断言，夹具自动清理。
func TestBudgetReservationsAtomic(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	other, err := Open(ctx, db.Engine.DataSourceName())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	user, err := db.CreateUser(ctx, Actor{}, UserInput{Email: "budget-holder@example.com", Password: "password123", MaxBudget: quotaLimit(10)})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := db.CreateKey(ctx, Actor{}, KeyInput{UserID: user.ID, OwnerType: OwnerPersonal, MaxBudget: quotaLimit(10)})
	if err != nil {
		t.Fatal(err)
	}
	if required, err := db.RequiresBudget(ctx, "key", key.ID); err != nil || !required {
		t.Fatalf("有限额身份判断失败: %t %v", required, err)
	}
	var wg sync.WaitGroup
	success := make(chan *BudgetReservation, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			instance := db
			if i%2 == 1 {
				instance = other
			}
			h, err := instance.ReserveBudget(ctx, fmt.Sprintf("attempt-%d", i), "key", key.ID, 6, BudgetReservationOptions{CallID: "call", Snapshot: "{}"})
			if err != nil {
				errs <- err
			} else {
				success <- h
			}
		}(i)
	}
	wg.Wait()
	close(success)
	close(errs)
	var held *BudgetReservation
	wins := 0
	for h := range success {
		held = h
		wins++
	}
	if wins != 1 {
		t.Fatalf("跨实例八次并发应只有一次预占成功，实际 %d", wins)
	}
	for err := range errs {
		if !errors.Is(err, ErrBudgetUnavailable) {
			t.Fatalf("并发余额错误: %v", err)
		}
	}
	if _, err := db.ReserveBudget(ctx, held.RequestID, "key", key.ID, 6, BudgetReservationOptions{CallID: "call", Snapshot: "{}"}); err != nil {
		t.Fatalf("相同尝试重放不幂等: %v", err)
	}
	if _, err := db.ReserveBudget(ctx, held.RequestID, "key", key.ID, 5, BudgetReservationOptions{CallID: "call", Snapshot: "{}"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("不同金额同 ID 未拒绝: %v", err)
	}
	if _, err := db.ReserveBudget(ctx, "invalid", "key", key.ID, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("负金额未拒绝: %v", err)
	}
	if err := db.SetBudgetReservationState(ctx, held.RequestID, "uncertain"); err != nil {
		t.Fatal(err)
	}
	pending, err := db.PendingKeyBudget(ctx, key.ID)
	if err != nil || len(pending) != 1 || pending[0].State != "uncertain" || pending[0].Amount != 6 {
		t.Fatalf("核对查询丢失占用: %+v %v", pending, err)
	}
	record := UsageRecord{BudgetReservationID: held.RequestID, RequestID: "call", KeyID: key.ID, UserID: user.ID, Cost: 7, TS: time.Now()}
	if err := db.RecordUsage(ctx, []UsageRecord{record}); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("超出声明费用不得静默穿透: %v", err)
	}
	record.Cost = 2
	record.UserID = "different"
	if err := db.RecordUsage(ctx, []UsageRecord{record}); !errors.Is(err, ErrConflict) {
		t.Fatalf("不同归属不得结算: %v", err)
	}
	record.UserID = user.ID
	if err := db.RecordUsage(ctx, []UsageRecord{record}); err != nil {
		t.Fatal(err)
	}
	pending, err = db.PendingKeyBudget(ctx, key.ID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("成功结算未原子释放: %+v %v", pending, err)
	}
	if err := db.RecordUsage(ctx, []UsageRecord{record}); err != nil {
		t.Fatalf("重复结算失败: %v", err)
	}
	updated, err := db.GetKey(ctx, key.ID)
	if err != nil || updated.Spend != 2 {
		t.Fatalf("重复结算重复扣费: %+v %v", updated, err)
	}
	if _, err := db.ReserveBudget(ctx, "already-settled", "key", key.ID, 1, BudgetReservationOptions{CallID: "call"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("已结算请求不得重新预占: %v", err)
	}
	next, err := db.ReserveBudget(ctx, "unsent", "key", key.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseBudget(ctx, next.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseBudget(ctx, next.RequestID); err != nil {
		t.Fatal(err)
	}
	// 删除凭据只能阻止新访问，不能退回已提交未知费用；新Key仍受同一用户占用约束。
	retained, err := db.ReserveBudget(ctx, "deleted-key-hold", "key", key.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetBudgetReservationState(ctx, retained.RequestID, "uncertain"); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteKey(ctx, Actor{}, key.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := db.PendingKeyBudget(ctx, key.ID)
	if err != nil || len(remaining) != 1 || remaining[0].Amount != 8 {
		t.Fatalf("删除Key不应退回未知费用: %+v %v", remaining, err)
	}
	replacement, _, err := db.CreateKey(ctx, Actor{}, KeyInput{UserID: user.ID, OwnerType: OwnerPersonal})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ReserveBudget(ctx, "replacement-attempt", "key", replacement.ID, 1); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("删除Key绕过用户预占: %v", err)
	}
}

// TestBudgetReservationProjectAndMigration 验证项目限制不能被宽松 Key/用户额度绕过，启动迁移保留占用。
// 前置私有 PostgreSQL，建立组织团队用户项目；正常/边界/失败输入均真实事务，夹具清理全部数据。
func TestBudgetReservationProjectAndMigration(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	actor := Actor{}
	org, err := db.CreateOrg(ctx, actor, "reservation-org", quotaLimit(100))
	if err != nil {
		t.Fatal(err)
	}
	team, err := db.CreateTeam(ctx, actor, TeamInput{OrganizationID: org.ID, Name: "reservation-team"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, actor, UserInput{Email: "project-holder@example.com", Password: "password123", TeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.CreateProject(ctx, actor, ProjectInput{TeamID: team.ID, Name: "reservation-project", MaxBudget: quotaLimit(3)})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := db.CreateKey(ctx, actor, KeyInput{OwnerType: OwnerPersonal, UserID: user.ID, TeamID: team.ID, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.ReserveBudget(ctx, "project-first", "key", key.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProjectID != project.ID || first.TeamID != team.ID || first.OrgID != org.ID || first.UserID != user.ID {
		t.Fatalf("预占五层身份不一致: %+v", first)
	}
	if _, err := db.ReserveBudget(ctx, "project-over", "key", key.ID, 0.01); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("项目限额被绕过: %v", err)
	}
	if _, err := db.UpdateProject(ctx, actor, project.ID, ProjectInput{MaxBudget: quotaLimit(1)}); !errors.Is(err, ErrQuotaAllocation) {
		t.Fatalf("项目调额不得低于已承诺费用: %v", err)
	}
	if _, err := db.UpdateProject(ctx, actor, project.ID, ProjectInput{MaxBudget: quotaLimit(3)}); err != nil {
		t.Fatalf("项目额度等于占用应允许: %v", err)
	}
	reopened, err := Open(ctx, db.Engine.DataSourceName())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	holds, err := reopened.PendingKeyBudget(ctx, key.ID)
	if err != nil || len(holds) != 1 || holds[0].Amount != 3 {
		t.Fatalf("重启迁移不得释放未知费用: %+v %v", holds, err)
	}
	if err := reopened.SetBudgetReservationState(ctx, first.RequestID, "expired"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("不支持自动过期状态: %v", err)
	}
	if err = db.DeleteKey(ctx, actor, key.ID); err != nil {
		t.Fatal(err)
	}
	replacement, _, err := db.CreateKey(ctx, actor, KeyInput{OwnerType: OwnerPersonal, UserID: user.ID, TeamID: team.ID, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ReserveBudget(ctx, "project-replacement", "key", replacement.ID, 1); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("删除Key绕过项目预占: %v", err)
	}
}

// TestBudgetReservationOrganizationPeriod 验证旧周期未知费用只保留非周期层占用，不能永久封锁新组织周期。
// 前置私有数据库与固定周期组织；跨周期重试保留原请求时间，真实结算不污染新周期，testDB 清理。
func TestBudgetReservationOrganizationPeriod(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	actor := Actor{}
	org, err := db.CreateOrgWithConfig(ctx, actor, "hold-period", quotaLimit(10), OrgConfig{BudgetDuration: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	team, err := db.CreateTeam(ctx, actor, TeamInput{OrganizationID: org.ID, Name: "hold-team"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, actor, UserInput{Email: "hold-period@example.com", Password: "password123", TeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := db.CreateKey(ctx, actor, KeyInput{OwnerType: OwnerPersonal, UserID: user.ID, TeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Microsecond)
	old, err := db.ReserveBudget(ctx, "old-attempt", "key", key.ID, 10, BudgetReservationOptions{CallID: "old-call", StartedAt: start})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetBudgetReservationState(ctx, old.RequestID, "uncertain"); err != nil {
		t.Fatal(err)
	}
	boundary := start.Add(time.Second)
	if _, err = db.Engine.Exec("UPDATE organizations SET budget_reset_at = ? WHERE id = ?", boundary, org.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.ResetOrganizationBudgets(ctx, boundary.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	next, err := db.ReserveBudget(ctx, "new-attempt", "key", key.ID, 10, BudgetReservationOptions{CallID: "new-call", StartedAt: boundary.Add(time.Second)})
	if err != nil {
		t.Fatalf("旧周期占用不应封锁新周期: %v", err)
	}
	if _, err = db.ReserveBudget(ctx, "new-over", "key", key.ID, 1, BudgetReservationOptions{CallID: "new-over", StartedAt: boundary.Add(time.Second)}); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("当前周期占用未计入: %v", err)
	}
	holds, err := db.PendingKeyBudget(ctx, key.ID)
	if err != nil || len(holds) != 2 {
		t.Fatalf("旧未知占用不得被自动删除: %+v %v", holds, err)
	}
	if err = db.RecordUsage(ctx, []UsageRecord{{RequestID: "old-call", BudgetReservationID: old.RequestID, KeyID: key.ID, UserID: user.ID, TeamID: team.ID, OrganizationID: org.ID, Cost: 2, TS: start}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetOrg(ctx, org.ID)
	if err != nil || got.Spend != 0 {
		t.Fatalf("旧周期结算污染组织新周期: %+v %v", got, err)
	}
	if err = db.ReleaseBudget(ctx, next.RequestID); err != nil {
		t.Fatal(err)
	}
}

// TestSoftBudgetBalance 验证仅检查余额同时尊重预占/uncertain/热费用且不修改历史记录；私有PG夹具创建身份后自动清理。
func TestSoftBudgetBalance(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	user, err := db.CreateUser(ctx, Actor{}, UserInput{Email: "soft-check@example.com", Password: "password123", MaxBudget: quotaLimit(1)})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := db.CreateKey(ctx, Actor{}, KeyInput{UserID: user.ID, OwnerType: OwnerPersonal, MaxBudget: quotaLimit(1)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.CheckSoftBudgetBalance(ctx, "key", key.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.CheckSoftBudgetBalance(ctx, "key", key.ID, func(string, string, ...time.Time) float64 { return 1 }); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("热费用未计入 %v", err)
	}
	if err = db.CheckSoftBudgetBalance(ctx, "key", key.ID, func(string, string, ...time.Time) float64 { return math.NaN() }); !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatalf("热费用不可读取时必须拒绝: %v", err)
	}
	if _, err = db.ReserveBudget(ctx, "old-hold", "key", key.ID, 1); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"reserved", "uncertain"} {
		if state == "uncertain" {
			if err = db.SetBudgetReservationState(ctx, "old-hold", state, "keep unchanged"); err != nil {
				t.Fatal(err)
			}
		}
		if err = db.CheckSoftBudgetBalance(ctx, "key", key.ID, nil); !errors.Is(err, ErrBudgetUnavailable) {
			t.Fatalf("未计入%s占用: %v", state, err)
		}
		holds, err := db.PendingKeyBudget(ctx, key.ID)
		if err != nil || len(holds) != 1 || holds[0].State != state || holds[0].Amount != 1 {
			t.Fatalf("历史占用被改写 %+v %v", holds, err)
		}
	}
	if err = db.CheckSoftBudgetBalance(ctx, "key", "missing", nil); err == nil {
		t.Fatal("未知身份不得放行")
	}
}
