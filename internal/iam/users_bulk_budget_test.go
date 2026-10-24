package iam

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

// TestNormalizeBulkUserBudgets 验证命令正常、复制、排序与非法边界；纯函数测试，不连接数据库或遗留对象。
func TestNormalizeBulkUserBudgets(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   BulkUserBudgetInput
		want error
	}{
		{"selected", BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: "b", MaxBudget: quotaLimit(0)}, {UserID: "a", MaxBudget: nil}}}, nil},
		{"all", BulkUserBudgetInput{AllUsers: true, MaxBudget: quotaLimit(1)}, nil},
		{"all clear", BulkUserBudgetInput{AllUsers: true}, nil},
		{"empty", BulkUserBudgetInput{}, ErrInvalid},
		{"duplicate", BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: "a", MaxBudget: nil}, {UserID: "a", MaxBudget: nil}}}, ErrInvalid},
		{"empty ID", BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: "", MaxBudget: nil}}}, ErrInvalid},
		{"whitespace ID", BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: " a", MaxBudget: nil}}}, ErrInvalid},
		{"negative", BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: "a", MaxBudget: quotaLimit(-1)}}}, ErrInvalid},
		{"infinite", BulkUserBudgetInput{AllUsers: true, MaxBudget: quotaLimit(math.Inf(1))}, ErrInvalid},
		{"NaN", BulkUserBudgetInput{AllUsers: true, MaxBudget: quotaLimit(math.NaN())}, ErrInvalid},
		{"mixed", BulkUserBudgetInput{AllUsers: true, Users: []UserBudgetUpdate{{UserID: "a", MaxBudget: nil}}}, ErrInvalid},
		{"implicit uniform", BulkUserBudgetInput{MaxBudget: quotaLimit(1), Users: []UserBudgetUpdate{{UserID: "a", MaxBudget: nil}}}, ErrInvalid},
		{"too many", BulkUserBudgetInput{Users: make([]UserBudgetUpdate, MaxBulkUserBudgets+1)}, ErrBulkUserBudgetTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeBulkUserBudgets(tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("边界判断错误: %+v %v", got, err)
			}
			if err == nil && !got.AllUsers {
				if got.Users[0].UserID != "a" || tc.in.Users[0].UserID != "b" {
					t.Fatal("排序修改了原始命令")
				}
				*got.Users[1].MaxBudget = 9
				if *tc.in.Users[0].MaxBudget != 0 {
					t.Fatal("预算指针未复制")
				}
			}
		})
	}
}

// TestBulkUserBudgetTree 验证批量最终树的父级/子级/消费约束；构造内存节点，不写数据库，含预占的Spend遵守同一规则。
func TestBulkUserBudgetTree(t *testing.T) {
	for _, tc := range []struct {
		name                string
		limit, spend, child float64
		parent              string
		want                error
	}{
		{"exact allocation", 10, 2, 8, "", nil}, {"below spend and holds", 1, 2, 0, "", ErrQuotaAllocation},
		{"key cap does not reserve", 9, 2, 8, "", nil}, {"ambiguous membership", 10, 0, 0, "ambiguous", ErrMultipleTeams}, {"orphan", 10, 0, 0, "team:missing", ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := &quotaNode{Kind: "key", ID: "key", Limit: quotaLimit(tc.child)}
			user := &quotaNode{Kind: "user", ID: "u", Parent: tc.parent, Limit: quotaLimit(tc.limit), Spend: tc.spend, Children: []*quotaNode{child}}
			err := validateBulkUserBudgetTree(map[string]*quotaNode{"user:u": user}, []string{"u"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("额度路径校验错误: %v", err)
			}
		})
	}
	if err := validateBulkUserBudgetTree(map[string]*quotaNode{}, []string{"missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// TestAdminBulkUserBudgetsAtomic 验证真实事务支持同批释放/分配、取消上限、审计与消费保留，混合不存在目标、下级保留及硬预占拒绝并回滚。
// 前置私有PostgreSQL schema与两名自建成员；testDB在结束时删除所有资源，不访问生产库。
func TestAdminBulkUserBudgetsAtomic(t *testing.T) {
	db := testDB(t)
	ctx, by := t.Context(), Actor{Kind: "master"}
	org, err := db.CreateOrg(ctx, by, "bulk", quotaLimit(100))
	if err != nil {
		t.Fatal(err)
	}
	team, err := db.CreateTeam(ctx, by, TeamInput{Name: "bulk", OrganizationID: org.ID, MaxBudget: quotaLimit(100)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.CreateUser(ctx, by, UserInput{Email: "bulk-a@example.invalid", Password: "password123", TeamID: team.ID, MaxBudget: quotaLimit(80)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateUser(ctx, by, UserInput{Email: "bulk-b@example.invalid", Password: "password123", TeamID: team.ID, MaxBudget: quotaLimit(20)})
	if err != nil {
		t.Fatal(err)
	}
	// 增加者放在数组最前，同一事务应按最终分配判断，不能受逐用户调用顺序影响。
	result, err := db.AdminBulkUserBudgets(ctx, by, BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: b.ID, MaxBudget: quotaLimit(70)}, {UserID: a.ID, MaxBudget: quotaLimit(30)}}})
	if err != nil || len(result.Users) != 2 || result.BatchID == "" {
		t.Fatalf("额度转移失败: %+v %v", result, err)
	}
	assertBudgets := func(av, bv float64) {
		t.Helper()
		ar, e := db.GetUser(ctx, a.ID)
		if e != nil {
			t.Fatal(e)
		}
		br, e := db.GetUser(ctx, b.ID)
		if e != nil {
			t.Fatal(e)
		}
		if ar.MaxBudget == nil || br.MaxBudget == nil || *ar.MaxBudget != av || *br.MaxBudget != bv {
			t.Fatalf("批次未原子保存/回滚: a=%+v b=%+v", ar, br)
		}
	}
	assertBudgets(30, 70)
	for _, in := range []BulkUserBudgetInput{{Users: []UserBudgetUpdate{{UserID: a.ID, MaxBudget: quotaLimit(40)}, {UserID: b.ID, MaxBudget: quotaLimit(70)}}}, {Users: []UserBudgetUpdate{{UserID: a.ID, MaxBudget: quotaLimit(20)}, {UserID: "missing", MaxBudget: nil}}}} {
		if _, err := db.AdminBulkUserBudgets(ctx, by, in); err == nil {
			t.Fatal("非法混合批次未拒绝")
		}
		assertBudgets(30, 70)
	}
	if _, err := db.ReserveBudget(ctx, "bulk-hold", "user", a.ID, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AdminBulkUserBudgets(ctx, by, BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: a.ID, MaxBudget: quotaLimit(9)}, {UserID: b.ID, MaxBudget: quotaLimit(60)}}}); !errors.Is(err, ErrQuotaAllocation) {
		t.Fatalf("降额绕过硬预占: %v", err)
	}
	assertBudgets(30, 70)
	if err := db.ReleaseBudget(ctx, "bulk-hold"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.ID(a.ID).Cols("spend").Update(&User{Spend: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AdminBulkUserBudgets(ctx, by, BulkUserBudgetInput{Users: []UserBudgetUpdate{{UserID: a.ID, MaxBudget: quotaLimit(1)}, {UserID: b.ID, MaxBudget: quotaLimit(60)}}}); !errors.Is(err, ErrQuotaAllocation) {
		t.Fatalf("降额低于已有消费: %v", err)
	}
	result, err = db.AdminBulkUserBudgets(ctx, by, BulkUserBudgetInput{AllUsers: true})
	if err != nil || len(result.Users) != 2 {
		t.Fatalf("全部明确取消个人上限失败: %+v %v", result, err)
	}
	ar, err := db.GetUser(ctx, a.ID)
	if err != nil || ar.MaxBudget != nil || ar.Spend != 2 || ar.Role != a.Role || ar.SessionVersion != a.SessionVersion {
		t.Fatalf("取消上限影响消费/角色/会话: %+v %v", ar, err)
	}
	count, err := db.Engine.Where("action = ?", "user.bulk_budget").Count(new(AuditEntry))
	if err != nil || count != 4 {
		t.Fatalf("失败批次留下审计或成功批次遗漏: count=%d %v", count, err)
	}
}

// TestAdminBulkUserBudgetsAllLimit 验证全部模式空集、1000人精确边界和1001人拒绝；直接写合成账号避免无关密码哈希开销。
// 前置私有PostgreSQL schema；超限不得截断保存或留下审计，成功边界应逐人保存，testDB结束清理所有数据。
func TestAdminBulkUserBudgetsAllLimit(t *testing.T) {
	db := testDB(t)
	ctx, by := t.Context(), Actor{Kind: "master"}
	if _, err := db.AdminBulkUserBudgets(ctx, by, BulkUserBudgetInput{AllUsers: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("全部空集合未拒绝: %v", err)
	}
	users := make([]User, MaxBulkUserBudgets+1)
	for i := range users {
		users[i] = User{ID: fmt.Sprintf("bulk-limit-%04d", i), Email: fmt.Sprintf("bulk-limit-%04d@example.invalid", i), Role: RoleUser, Status: StatusActive, MaxBudget: quotaLimit(5)}
	}
	if _, err := db.Engine.Insert(&users); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AdminBulkUserBudgets(ctx, by, BulkUserBudgetInput{AllUsers: true, MaxBudget: quotaLimit(0)}); !errors.Is(err, ErrBulkUserBudgetTooLarge) {
		t.Fatalf("超过1000人未明确拒绝: %v", err)
	}
	count, err := db.Engine.Where("max_budget = ?", 5).Count(new(User))
	if err != nil || count != MaxBulkUserBudgets+1 {
		t.Fatalf("超限被部分保存: %d %v", count, err)
	}
	audits, err := db.Engine.Where("action = ?", "user.bulk_budget").Count(new(AuditEntry))
	if err != nil || audits != 0 {
		t.Fatalf("拒绝批次留下审计: %d %v", audits, err)
	}
	if _, err := db.Engine.ID(users[0].ID).Delete(new(User)); err != nil {
		t.Fatal(err)
	}
	result, err := db.AdminBulkUserBudgets(ctx, by, BulkUserBudgetInput{AllUsers: true, MaxBudget: quotaLimit(0)})
	if err != nil || len(result.Users) != MaxBulkUserBudgets {
		t.Fatalf("精确1000人边界错误: %d %v", len(result.Users), err)
	}
	count, err = db.Engine.Where("max_budget = ?", 0).Count(new(User))
	if err != nil || count != MaxBulkUserBudgets {
		t.Fatalf("精确边界未逐人保存: %d %v", count, err)
	}
}

// TestBulkUserBudgetOverspentSibling 验证批量修改不受无关超支用户阻塞，但直接对超支目标降额仍拒绝。
// 前置纯内存组织树；覆盖合法分配、真实父级超配和目标消费下限，函数无外部副作用，无需清理。
func TestBulkUserBudgetOverspentSibling(t *testing.T) {
	old := &quotaNode{Kind: "user", ID: "old", Parent: "org:o", Limit: quotaLimit(500), Spend: 501}
	target := &quotaNode{Kind: "user", ID: "new", Parent: "org:o", Limit: quotaLimit(499)}
	org := &quotaNode{Kind: "org", ID: "o", Limit: quotaLimit(1000), Spend: 501, Children: []*quotaNode{old, target}}
	nodes := map[string]*quotaNode{"org:o": org, "user:old": old, "user:new": target}
	if err := validateBulkUserBudgetTree(nodes, []string{"new"}); err != nil {
		t.Fatalf("无关超支阻塞合法分配: %v", err)
	}
	target.Limit = quotaLimit(500)
	if err := validateBulkUserBudgetTree(nodes, []string{"new"}); !errors.Is(err, ErrQuotaAllocation) {
		t.Fatalf("父级超配未拒绝: %v", err)
	}
	target.Limit = quotaLimit(499)
	if err := validateBulkUserBudgetTree(nodes, []string{"old"}); !errors.Is(err, ErrQuotaAllocation) {
		t.Fatalf("修改目标不得低于消费: %v", err)
	}
}
