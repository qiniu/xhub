package iam

import (
	"context"
	"testing"
)

// TestListKeysOrganization 验证 ListKeys 的组织条件和已有权限条件取交集；前置独立 PostgreSQL schema。
// 参数 t 管理测试生命周期；覆盖无团队、管理成员身份、空范围、注入字符串、转团队及取消错误，testDB 清理全部夹具。
func TestListKeysOrganization(t *testing.T) {
	db := testDB(t)
	for _, id := range []string{"a", "b", "none"} {
		u := User{ID: id, Email: id + "@example.com", Name: id, Role: RoleUser, Status: StatusActive}
		if _, err := db.Engine.Insert(&u); err != nil {
			t.Fatal(err)
		}
		k := Key{ID: "key-" + id, UserID: &u.ID, OwnerType: OwnerPersonal, TokenHash: HashKey(id), Status: StatusActive}
		if _, err := db.Engine.Insert(&k); err != nil {
			t.Fatal(err)
		}
		if id == "none" {
			continue
		}
		if _, err := db.Engine.Insert(&Organization{ID: "org-" + id, Name: id, Status: StatusActive}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Engine.Insert(&Team{ID: "team-" + id, OrganizationID: "org-" + id, Name: id, Status: StatusActive}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Engine.Insert(&TeamMembership{TeamID: "team-" + id, UserID: id, Role: TeamMember}); err != nil {
			t.Fatal(err)
		}
	}
	// 组织管理员身份不代表个人 Key 归属，避免把管理授权误用为资源所属组织。
	if _, err := db.Engine.Insert(&OrganizationMembership{OrganizationID: "org-b", UserID: "a", Role: OrgAdmin}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		f    KeyFilter
		want int
	}{
		{"empty", KeyFilter{}, 3},
		{"matching", KeyFilter{OrganizationID: "org-a"}, 1},
		{"missing", KeyFilter{OrganizationID: "missing"}, 0},
		{"injection", KeyFilter{OrganizationID: "' OR 1=1 --"}, 0},
		{"other owner", KeyFilter{OrganizationID: "org-b", UserID: "a"}, 0},
		{"teamless", KeyFilter{OrganizationID: "org-a", UserID: "none"}, 0},
		{"matching scope", KeyFilter{OrganizationID: "org-a", TeamIDs: []string{"team-a"}, KeyIDs: []string{"key-a"}}, 1},
		{"foreign scope", KeyFilter{OrganizationID: "org-b", TeamIDs: []string{"team-a"}}, 0},
		{"foreign key", KeyFilter{OrganizationID: "org-a", KeyIDs: []string{"key-b"}}, 0},
		{"empty teams", KeyFilter{OrganizationID: "org-a", TeamIDs: []string{}}, 0},
		{"empty keys", KeyFilter{OrganizationID: "org-a", KeyIDs: []string{}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.ListKeys(t.Context(), tc.f)
			if err != nil || len(rows) != tc.want {
				t.Fatalf("组织条件 %+v: got=%d want=%d err=%v", tc.f, len(rows), tc.want, err)
			}
		})
	}
	if _, err := db.Engine.Where("user_id = ?", "a").Cols("team_id").Update(&TeamMembership{TeamID: "team-b"}); err != nil {
		t.Fatal(err)
	}
	for org, want := range map[string]int{"org-a": 0, "org-b": 1} {
		rows, err := db.ListKeys(t.Context(), KeyFilter{OrganizationID: org, UserID: "a"})
		if err != nil || len(rows) != want {
			t.Fatalf("转团队后必须使用当前组织 %s: %d %v", org, len(rows), err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := db.ListKeys(ctx, KeyFilter{OrganizationID: "org-a"}); err == nil {
		t.Fatal("取消的组织查询不能成功")
	}
}
