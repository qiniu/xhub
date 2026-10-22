package iam

import (
	"errors"
	"fmt"
	"testing"
)

// TestUsageOwnerDefaults 验证只有用户 Key 和会话两种请求来源，而非两种用户。
// 参数 t 为测试上下文；前置纯输入表，验证默认、显式值与空记录，不连接数据库，无需清理。
func TestUsageOwnerDefaults(t *testing.T) {
	for _, tc := range []struct {
		record UsageRecord
		want   string
	}{
		{UsageRecord{}, "session"},
		{UsageRecord{KeyID: "key"}, OwnerPersonal},
		{UsageRecord{OwnerType: OwnerPersonal}, OwnerPersonal},
		{UsageRecord{OwnerType: "session"}, "session"},
	} {
		if got := ownerType(tc.record); got != tc.want {
			t.Fatalf("日志默认归属错误: got=%s want=%s", got, tc.want)
		}
	}
}

// TestUnifiedUserAllocationExamples 验证组织/团队/用户/Key 三种上限相同的 1000=300+300+400 分配规则。
// 参数 t 为测试上下文，无返回；正常、超配、共享、零、缩减、释放和独立用户均使用隔离数据库，testDB 清理 schema。
func TestUnifiedUserAllocationExamples(t *testing.T) {
	for _, dimension := range []string{"budget", "rpm", "tpm"} {
		t.Run(dimension, func(t *testing.T) {
			db := testDB(t)
			ctx, by := t.Context(), Actor{}
			cap, part, remainder, overflow, zero := 1000, 300, 400, 401, 0
			orgBudget, teamBudget := (*float64)(nil), (*float64)(nil)
			rates := RateLimits{}
			teamInput := TeamInput{Name: "team"}
			// 每次只启用一个维度，防止另一维度先拒绝而掩盖当前维度错误。
			switch dimension {
			case "budget":
				orgBudget, teamBudget = quotaLimit(1000), quotaLimit(1000)
				teamInput.MaxBudget = teamBudget
			case "rpm":
				rates.RPMLimit, teamInput.RPMLimit = &cap, &cap
			case "tpm":
				rates.TPMLimit, teamInput.TPMLimit = &cap, &cap
			}
			org, err := db.CreateOrg(ctx, by, "organization", orgBudget, rates)
			if err != nil {
				t.Fatal(err)
			}
			teamInput.OrganizationID = org.ID
			team, err := db.CreateTeam(ctx, by, teamInput)
			if err != nil {
				t.Fatal(err)
			}
			// 构造用户输入仅为本表驱动用例服务，不引入第二种用户。
			userInput := func(name string, limit *int) UserInput {
				in := UserInput{Email: name + "@example.invalid", Password: "password123", TeamID: team.ID}
				if limit != nil {
					switch dimension {
					case "budget":
						in.MaxBudget = quotaLimit(float64(*limit))
					case "rpm":
						in.RPMLimit = limit
					case "tpm":
						in.TPMLimit = limit
					}
				}
				return in
			}
			users := []*User{}
			for _, name := range []string{"alice", "bob"} {
				u, e := db.CreateUser(ctx, by, userInput(name, &part))
				if e != nil {
					t.Fatal(e)
				}
				users = append(users, u)
			}
			shared, err := db.CreateUser(ctx, by, userInput("shared", nil))
			if err != nil {
				t.Fatal(err)
			}
			blocked, err := db.CreateUser(ctx, by, userInput("blocked", &zero))
			if err != nil {
				t.Fatal(err)
			}
			expected := ErrRateAllocation
			if dimension == "budget" {
				expected = ErrQuotaAllocation
			}
			if _, e := db.CreateUser(ctx, by, userInput("overflow", &overflow)); !errors.Is(e, expected) {
				t.Fatalf("300+300 后最多分配400，401必须回滚: %v", e)
			}
			last, err := db.CreateUser(ctx, by, userInput("last", &remainder))
			if err != nil {
				t.Fatalf("剩余400应可恰好分满: %v", err)
			}
			for _, u := range []*User{shared, blocked, users[0]} {
				k, _, e := db.CreateKey(ctx, by, KeyInput{UserID: u.ID, OwnerType: OwnerPersonal})
				if e != nil {
					t.Fatal(e)
				}
				if dimension == "budget" {
					_, _, scope, e := db.QuotaPath(ctx, "key", k.ID, nil)
					if e != nil || (scope != "") != (u.ID != users[0].ID) {
						t.Fatalf("固定成员可用，共享池分满和零成员禁用: user=%s scope=%s err=%v", u.Email, scope, e)
					}
				}
			}
			if err := db.DeleteUser(ctx, by, last.ID); err != nil {
				t.Fatal(err)
			}
			if _, e := db.CreateUser(ctx, by, userInput("replacement", &remainder)); e != nil {
				t.Fatalf("删除用户应释放全部未用分配: %v", e)
			}
			// 金额 Key 不预占个人资金；RPM/TPM 继续按固定分配校验。
			for i, n := range []int{100, 200, 1} {
				in := KeyInput{UserID: users[0].ID, OwnerType: OwnerPersonal, Name: fmt.Sprint(i)}
				switch dimension {
				case "budget":
					in.MaxBudget = quotaLimit(float64(n))
				case "rpm":
					in.RPMLimit = &n
				case "tpm":
					in.TPMLimit = &n
				}
				_, _, e := db.CreateKey(ctx, by, in)
				if (i < 2 || dimension == "budget") && e != nil {
					t.Fatal(e)
				}
				if i == 2 && dimension != "budget" && !errors.Is(e, expected) {
					t.Fatalf("Key100+200分满用户300，再分1必须失败: %v", e)
				}
			}
		})
	}
}

// TestTeamDeletionPreservesUserKeys 验证删除团队解除唯一归属且所有旧 Key 保留，用户仍可新建普通 Key。
// 参数 t 为测试上下文；前置隔离组织、团队和用户，验证独立链与异常模型输入，testDB 自动清理全部数据。
func TestTeamDeletionPreservesUserKeys(t *testing.T) {
	db := testDB(t)
	ctx, by := t.Context(), Actor{}
	o, err := db.CreateOrg(ctx, by, "delete-team", nil)
	if err != nil {
		t.Fatal(err)
	}
	team, err := db.CreateTeam(ctx, by, TeamInput{OrganizationID: o.ID, Name: "team", Models: []string{"allowed"}})
	if err != nil {
		t.Fatal(err)
	}
	u, err := db.CreateUser(ctx, by, UserInput{Email: "member@example.invalid", Password: "password123", TeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := db.CreateKey(ctx, by, KeyInput{OwnerType: OwnerPersonal, UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	k.TeamID = "wrong-cache-hint"
	models, err := db.AllowedModelsForKey(ctx, k)
	if err != nil || len(models) != 1 || models[0] != "allowed" {
		t.Fatalf("模型权限必须实时派生团队而非信任缓存提示: %v %v", models, err)
	}
	if _, err := db.AllowedModelsForKey(ctx, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空 Key 不应获得不限模型权限: %v", err)
	}
	if err := db.DeleteTeam(ctx, by, team.ID); err != nil {
		t.Fatal(err)
	}
	old, err := db.GetKey(ctx, k.ID)
	if err != nil || old.Status != StatusActive || old.TeamID != "" {
		t.Fatalf("团队删除后旧Key应保留且无归属: %+v %v", old, err)
	}
	fresh, _, err := db.CreateKey(ctx, by, KeyInput{OwnerType: OwnerPersonal, UserID: u.ID})
	if err != nil || fresh.TeamID != "" {
		t.Fatalf("删除团队后的用户可新建独立Key: %+v %v", fresh, err)
	}
}
