package iam

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestQuotaAndRatesScope 验证请求内共用额度及速率归属；前置两个隔离组织和一个个人，验证兄弟保留及跨组织隔离。
// 参数 t 为测试上下文，无返回；testDB 自动清理 schema，不修改共享数据或分钟计数。
func TestQuotaAndRatesScope(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	actor := Actor{}
	org, err := db.CreateOrg(ctx, actor, "caller", quotaLimit(100))
	if err != nil {
		t.Fatal(err)
	}
	team, err := db.CreateTeam(ctx, actor, TeamInput{OrganizationID: org.ID, Name: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := db.CreateTeam(ctx, actor, TeamInput{OrganizationID: org.ID, Name: "reserved", MaxBudget: quotaLimit(100)})
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateOrg(ctx, actor, "unrelated", nil)
	if err != nil {
		t.Fatal(err)
	}
	otherTeam, err := db.CreateTeam(ctx, actor, TeamInput{OrganizationID: other.ID, Name: "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, actor, UserInput{Email: "scope@example.com", Password: "password123", TeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := db.CreateKey(ctx, actor, KeyInput{OwnerType: OwnerPersonal, UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	hotIDs := map[string]bool{}
	tid, oid, scope, empty, err := db.QuotaAndRates(ctx, "key", key.ID, func(kind, id string, _ ...time.Time) float64 { hotIDs[kind+":"+id] = true; return 0 })
	if err != nil || tid != team.ID || oid != org.ID || scope != "org" || len(empty.Nodes) != 0 {
		t.Fatalf("兄弟固定额度应阻止共享调用并不生成速率计划: team=%s org=%s scope=%s plan=%+v err=%v", tid, oid, scope, empty, err)
	}
	if !hotIDs["team:"+sibling.ID] || hotIDs["org:"+other.ID] || hotIDs["team:"+otherTeam.ID] {
		t.Fatalf("热消费读取范围错误: %+v", hotIDs)
	}
	// 释放兄弟保留后，合并读取与独立速率计划必须一致。
	if _, err := db.Engine.Exec("UPDATE teams SET max_budget = NULL WHERE id = ?", sibling.ID); err != nil {
		t.Fatal(err)
	}
	_, _, scope, plan, err := db.QuotaAndRates(ctx, "key", key.ID, nil)
	want, wantErr := db.RatePlan(ctx, "key", key.ID)
	if err != nil || wantErr != nil || scope != "" || !reflect.DeepEqual(plan, want) {
		t.Fatalf("共享读取计划不一致: scope=%s plan=%+v want=%+v errors=%v/%v", scope, plan, want, err, wantErr)
	}
	personal, err := db.CreateUser(ctx, actor, UserInput{Email: "personal-scope@example.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	personalKey, _, err := db.CreateKey(ctx, actor, KeyInput{OwnerType: OwnerPersonal, UserID: personal.ID})
	if err != nil {
		t.Fatal(err)
	}
	hotIDs = map[string]bool{}
	tid, oid, scope, plan, err = db.QuotaAndRates(ctx, "user", personal.ID, func(kind, id string, _ ...time.Time) float64 { hotIDs[kind+":"+id] = true; return 0 })
	if err != nil || tid != "" || oid != "" || scope != "" || len(plan.Nodes) != 2 || len(hotIDs) != 2 || !hotIDs["user:"+personal.ID] || !hotIDs["key:"+personalKey.TokenHash] {
		t.Fatalf("无团队个人必须只读取自己及个人密钥: %s %s %s %+v %+v %v", tid, oid, scope, plan, hotIDs, err)
	}
	for _, tc := range []struct {
		kind, id string
		want     error
	}{{"key", "missing", ErrNotFound}, {"user", "missing", ErrNotFound}, {"org", org.ID, ErrInvalid}} {
		_, _, _, _, err := db.QuotaAndRates(ctx, tc.kind, tc.id, nil)
		if !errors.Is(err, tc.want) {
			t.Fatalf("非法主体 %s/%s: got=%v want=%v", tc.kind, tc.id, err, tc.want)
		}
	}
}
