package iam

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

// TestQuotaHotPeriod 验证额度树仅向组织热消费读取器传入周期起点，其他层级仍累计。
// 参数 t 为测试上下文，无返回；正常/边界和耗尽分支使用内存节点，不写数据库，无需清理。
func TestQuotaHotPeriod(t *testing.T) {
	start := time.Now().UTC()
	for _, spent := range []float64{0, 10} {
		limit := 10.0
		org := &quotaNode{ID: "org", Kind: "org", Limit: &limit, PeriodStart: &start}
		user := &quotaNode{ID: "user", Kind: "user", Parent: "org:org"}
		nodes := map[string]*quotaNode{"org:org": org, "user:user": user}
		_, _, scope, err := quotaPath(nodes, "user:user", func(kind, id string, since ...time.Time) float64 {
			if kind == "org" {
				if len(since) != 1 || !since[0].Equal(start) {
					t.Fatal("组织热消费缺少周期")
				}
				return spent
			}
			if len(since) != 0 {
				t.Fatal("非组织不应按组织周期清空")
			}
			return 0
		})
		if err != nil || (scope == "org") != (spent == 10) {
			t.Fatalf("周期额度判断错误: %s %v", scope, err)
		}
	}
}

// quotaLimit 构造测试额度指针；参数 value 为美元，返回独立指针，无副作用，供边界表使用。
func quotaLimit(value float64) *float64 { return &value }

// TestQuotaUnused 验证逐级未消费保留；前置纯内存树，覆盖固定、共享、零、已消费和非法输入，无外部数据需清理。
// 参数 t 为测试上下文，无返回；包含个人重置恢复预留后超配拒绝、父级扩容恢复的边界。
func TestQuotaUnused(t *testing.T) {
	for _, tc := range []struct {
		name string
		node *quotaNode
		want float64
		fail bool
	}{
		{"固定", &quotaNode{Limit: quotaLimit(1000), Spend: 100}, 900, false},
		{"共享传递", &quotaNode{Children: []*quotaNode{{Limit: quotaLimit(400), Spend: 100}, {Limit: quotaLimit(300)}}}, 600, false},
		{"正好分满", &quotaNode{Limit: quotaLimit(1000), Children: []*quotaNode{{Limit: quotaLimit(500)}, {Limit: quotaLimit(500)}}}, 1000, false},
		{"消费后超配", &quotaNode{Limit: quotaLimit(1000), Spend: 100, Children: []*quotaNode{{Limit: quotaLimit(1000)}}}, 0, true},
		{"重置前消费抵消个人预留", &quotaNode{Limit: quotaLimit(1000), Spend: 100, Children: []*quotaNode{{Limit: quotaLimit(1000), Spend: 100}}}, 900, false},
		{"重置个人不得抹去父级消费", &quotaNode{Limit: quotaLimit(1000), Spend: 100, Children: []*quotaNode{{Limit: quotaLimit(1000), Spend: 0}}}, 0, true},
		{"父级扩容后允许个人重新预留", &quotaNode{Limit: quotaLimit(1100), Spend: 100, Children: []*quotaNode{{Limit: quotaLimit(1000), Spend: 0}}}, 1000, false},
		{"零", &quotaNode{Limit: quotaLimit(0)}, 0, false},
		{"负数", &quotaNode{Limit: quotaLimit(-1)}, 0, true},
		{"非数", &quotaNode{Limit: quotaLimit(math.NaN())}, 0, true},
		{"无穷", &quotaNode{Limit: quotaLimit(math.Inf(1))}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := quotaUnused(tc.node)
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("额度保留错误 got=%v err=%v want=%v fail=%v", got, err, tc.want, tc.fail)
			}
		})
	}
}

// TestQuotaHierarchy 验证真实数据库的分配、热消费、并发、单团队与退出；前置隔离 schema，自动删除所有数据。
// 参数 t 为测试上下文，无返回值；所有拒绝同时验证保存回滚，避免只验证提示信息。
func TestQuotaHierarchy(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	by := Actor{}
	org, err := db.CreateOrg(ctx, by, "quota", quotaLimit(1000))
	if err != nil {
		t.Fatal(err)
	}
	team, err := db.CreateTeam(ctx, by, TeamInput{OrganizationID: org.ID, Name: "fixed", MaxBudget: quotaLimit(600)})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := db.CreateTeam(ctx, by, TeamInput{OrganizationID: org.ID, Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CreateTeam(ctx, by, TeamInput{OrganizationID: org.ID, Name: "overflow", MaxBudget: quotaLimit(401)}); !errors.Is(err, ErrQuotaAllocation) {
		t.Fatalf("组织超配未拒绝: %v", err)
	}
	u, err := db.CreateUser(ctx, by, UserInput{Email: "quota@example.com", Name: "Quota", Password: "quota-password", TeamID: team.ID, MaxBudget: quotaLimit(400)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.AddMember(ctx, by, shared.ID, u.Email, TeamMember); !errors.Is(err, ErrMultipleTeams) {
		t.Fatalf("第二团队未拒绝: %v", err)
	}
	if _, err = db.Engine.Exec("INSERT INTO team_members(team_id,user_id) VALUES (?,?)", shared.ID, u.ID); !errors.Is(mapErr(err), ErrMultipleTeams) {
		t.Fatalf("数据库单归属未生效: %v", err)
	}
	k, _, err := db.CreateKey(ctx, by, KeyInput{OwnerType: OwnerPersonal, UserID: u.ID, MaxBudget: quotaLimit(400)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.CreateKey(ctx, by, KeyInput{OwnerType: OwnerPersonal, UserID: u.ID, MaxBudget: quotaLimit(1)}); err != nil {
		t.Fatalf("密钥单项上限不应预占个人预算: %v", err)
	}
	teamID, orgID, scope, err := db.QuotaPath(ctx, "key", k.ID, nil)
	if err != nil || scope != "" || teamID != team.ID || orgID != org.ID {
		t.Fatalf("无团队绑定密钥计费路径: %s %s %s %v", teamID, orgID, scope, err)
	}
	_, _, scope, err = db.QuotaPath(ctx, "user", u.ID, nil)
	if err != nil || scope != "" {
		t.Fatalf("会话应共享个人余额: %s %v", scope, err)
	}
	// 热消费已占用个人金额，但相同消费也减少密钥保留额，不能重复占用。
	_, _, scope, err = db.QuotaPath(ctx, "key", k.ID, func(kind, id string, _ ...time.Time) float64 {
		if kind == "key" && id == k.TokenHash {
			return 100
		}
		if kind == "user" && id == u.ID {
			return 100
		}
		return 0
	})
	if err != nil || scope != "" {
		t.Fatalf("热消费重复占用: %s %v", scope, err)
	}
	// 两个并发分配各 300，组织仅剩 400，必须恰好成功一个。
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, e := db.CreateTeam(ctx, by, TeamInput{OrganizationID: org.ID, Name: fmt.Sprintf("concurrent-%d", index), MaxBudget: quotaLimit(300)})
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	passed, refused := 0, 0
	for e := range results {
		if e == nil {
			passed++
		} else if errors.Is(e, ErrQuotaAllocation) {
			refused++
		} else {
			t.Fatal(e)
		}
	}
	if passed != 1 || refused != 1 {
		t.Fatalf("并发超配 passed=%d refused=%d", passed, refused)
	}
	if err = db.RemoveMember(ctx, by, team.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if tid, oid, got, e := db.QuotaPath(ctx, "user", u.ID, nil); e != nil || tid != "" || oid != "" || got != "" {
		t.Fatalf("退出后应成为独立个人且不预留密钥额度: %s %s %s %v", tid, oid, got, e)
	}
	old, err := db.GetKey(ctx, k.ID)
	if err != nil || old.Status != StatusActive {
		t.Fatalf("退出团队必须保留个人密钥: %+v %v", old, err)
	}
	if _, err = db.AddMember(ctx, by, shared.ID, u.Email, TeamMember); !errors.Is(err, ErrQuotaAllocation) {
		t.Fatalf("转入团队超配未拒绝: %v", err)
	}
}

// TestQuotaSharedAncestorsAndBusiness 验证 Key 上限不预占共享祖先资金，兄弟 Key 同样可用。
// 前置独立 schema；同时验证不限额业务和成员编辑失败回滚，testDB 自动清理数据。
func TestQuotaSharedAncestorsAndBusiness(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	by := Actor{}
	org, err := db.CreateOrg(ctx, by, "shared-chain", quotaLimit(100))
	if err != nil {
		t.Fatal(err)
	}
	team, err := db.CreateTeam(ctx, by, TeamInput{OrganizationID: org.ID, Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := db.CreateUser(ctx, by, UserInput{Email: "shared-chain@example.com", Password: "shared-password", Name: "Shared", TeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := db.CreateKey(ctx, by, KeyInput{OwnerType: OwnerPersonal, UserID: u.ID, MaxBudget: quotaLimit(100)})
	if err != nil {
		t.Fatal(err)
	}
	shared, _, err := db.CreateKey(ctx, by, KeyInput{OwnerType: OwnerPersonal, UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if tid, oid, scope, e := db.QuotaPath(ctx, "key", k.ID, nil); e != nil || scope != "" || tid != team.ID || oid != org.ID {
		t.Fatalf("固定密钥穿过共享祖先: %s %s %s %v", tid, oid, scope, e)
	}
	if _, _, scope, e := db.QuotaPath(ctx, "key", shared.ID, nil); e != nil || scope != "" {
		t.Fatalf("共享密钥不应被固定兄弟上限拦截: %s %v", scope, e)
	}
	limit := quotaLimit(101)
	if e := db.SetMemberRoleBudget(ctx, by, team.ID, u.ID, TeamAdmin, &limit); !errors.Is(e, ErrQuotaAllocation) {
		t.Fatalf("超配成员修改应回滚: %v", e)
	}
	members, e := db.ListMembers(ctx, team.ID)
	if e != nil || len(members) != 1 || members[0].Role != TeamMember || members[0].MaxBudget != nil {
		t.Fatalf("角色与额度未一起回滚: %+v %v", members, e)
	}
	unlimited, e := db.CreateOrg(ctx, by, "unlimited-business", nil)
	if e != nil {
		t.Fatal(e)
	}
	business, e := db.CreateTeam(ctx, by, TeamInput{OrganizationID: unlimited.ID, Name: "business"})
	if e != nil {
		t.Fatal(e)
	}
	account, e := db.CreateUser(ctx, by, UserInput{Email: "orders@example.com", Name: "Orders service", Password: "password123", TeamID: business.ID})
	if e != nil {
		t.Fatal(e)
	}
	service, _, e := db.CreateKey(ctx, by, KeyInput{OwnerType: OwnerPersonal, UserID: account.ID, MaxBudget: quotaLimit(1000000)})
	if e != nil {
		t.Fatal(e)
	}
	if service.UserID == nil || *service.UserID != account.ID {
		t.Fatal("应用密钥必须属于显式创建的普通用户")
	}
	if tid, oid, scope, e := db.QuotaPath(ctx, "key", service.ID, nil); e != nil || scope != "" || tid != business.ID || oid != unlimited.ID {
		t.Fatalf("不限额业务路径: %s %s %s %v", tid, oid, scope, e)
	}
}

// TestOrganizationBudgetValidation 验证组织根额度的合法和失败输入；前置隔离数据库，testDB 自动删除 schema。
// 参数 t 为测试上下文，无返回；失败不得产生组织，null 与零合法。
func TestOrganizationBudgetValidation(t *testing.T) {
	db := testDB(t)
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, err := db.CreateOrg(t.Context(), Actor{}, "invalid", &value); !errors.Is(err, ErrQuotaAllocation) {
			t.Fatalf("非法组织额度未拒绝: %v", err)
		}
	}
	for i, cap := range []*float64{nil, quotaLimit(0), quotaLimit(100)} {
		if _, err := db.CreateOrg(t.Context(), Actor{}, fmt.Sprintf("valid-%d", i), cap); err != nil {
			t.Fatal(err)
		}
	}
}

// TestQuotaKeyPendingUnavailable 验证有限额 Key 的热费用故障明确失败，无限额 Key 不依赖累计展示。
// 前置纯内存额度树；覆盖正常、恰好用尽和非数失败，无外部状态，无需清理。
func TestQuotaKeyPendingUnavailable(t *testing.T) {
	for _, tc := range []struct {
		limit *float64
		hot   float64
		scope string
		fail  bool
	}{
		{quotaLimit(10), 2, "", false}, {quotaLimit(10), 10, "key", false}, {quotaLimit(10), math.NaN(), "", true}, {nil, math.NaN(), "", false},
	} {
		key := &quotaNode{ID: "stable", HotID: "stable", Kind: "key", Limit: tc.limit}
		_, _, scope, err := quotaPath(map[string]*quotaNode{"key:stable": key}, "key:stable", func(kind, id string, _ ...time.Time) float64 {
			if kind != "key" || id != "stable" {
				t.Fatal("热费用未使用稳定 KeyID")
			}
			return tc.hot
		})
		if scope != tc.scope || errors.Is(err, ErrQuotaUnavailable) != tc.fail {
			t.Fatalf("额度结果 scope=%s err=%v 期望 scope=%s failure=%v", scope, err, tc.scope, tc.fail)
		}
	}
}

// TestSoftQuotaPositiveBoundary 验证软预算不会把微小正余额视为耗尽；纯内存节点覆盖正数、零和超支，无需清理。
func TestSoftQuotaPositiveBoundary(t *testing.T) {
	limit := 1e-13
	for _, spent := range []float64{0, 1e-13, 2e-13} {
		nodes := map[string]*quotaNode{"user:soft": {ID: "soft", Kind: "user", Limit: &limit, Spend: spent}}
		_, _, scope, err := quotaPath(nodes, "user:soft", nil)
		if err != nil || (scope != "") != (spent >= limit) {
			t.Fatalf("余额边界错误: spend=%g scope=%s err=%v", spent, scope, err)
		}
	}
}

// TestUserQuotaHotPeriod 验证个人预算独立向热费用读取器传递周期，包含余额耗尽边界；纯内存，无资源清理。
func TestUserQuotaHotPeriod(t *testing.T) {
	start := time.Now()
	for _, spent := range []float64{0, 10} {
		u := &quotaNode{ID: "u", Kind: "user", PeriodStart: &start, Limit: quotaLimit(10)}
		_, _, scope, err := quotaPath(map[string]*quotaNode{"user:u": u}, "user:u", func(kind, id string, since ...time.Time) float64 {
			if kind != "user" || id != "u" || len(since) != 1 || !since[0].Equal(start) {
				t.Fatal("个人热账单丢失周期")
			}
			return spent
		})
		if err != nil || (scope == "user") != (spent == 10) {
			t.Fatalf("个人余额判断错误: %s %v", scope, err)
		}
	}
}
