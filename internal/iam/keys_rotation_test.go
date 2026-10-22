package iam

import (
	"context"
	"testing"
	"time"
)

// TestRotateKeyAtomicSettings 验证持久层轮换事务；前置隔离数据库及一美元用户预算，testDB 清理 schema。
// 正常更新同时保存额度/限流/期限；负预算或输入数量异常必须回滚哈希及所有字段，缺省更新保留已存配置。
func TestRotateKeyAtomicSettings(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ceiling := 1.0
	u, err := db.CreateUser(ctx, Actor{}, UserInput{Email: "rotate@example.com", Password: "password123", Role: RoleUser, MaxBudget: &ceiling})
	if err != nil {
		t.Fatal(err)
	}
	by := Actor{ID: u.ID, Kind: "session"}
	original, secret, err := db.CreateKey(ctx, by, KeyInput{OwnerType: OwnerPersonal, UserID: u.ID, Name: "before"})
	if err != nil {
		t.Fatal(err)
	}
	tooMuch := -1.0
	for _, inputs := range [][]KeyInput{{{Name: "invalid", MaxBudget: &tooMuch}}, {{}, {}}} {
		if _, _, err := db.RotateKey(ctx, by, original.ID, inputs...); err == nil {
			t.Fatal("非法轮换未失败")
		}
		current, err := db.KeyByHash(ctx, HashKey(secret))
		if err != nil || current.Name != "before" || current.MaxBudget != nil {
			t.Fatalf("失败轮换修改了旧凭据: %+v %v", current, err)
		}
	}
	budget, rpm, tpm := 0.5, 5, 1000
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	rotated, next, err := db.RotateKey(ctx, by, original.ID, KeyInput{Name: "after", MaxBudget: &budget, RPMLimit: &rpm, TPMLimit: &tpm, ExpiresAt: &expiry})
	if err != nil || rotated.Name != "after" || rotated.MaxBudget == nil || *rotated.MaxBudget != budget || rotated.RPMLimit == nil || *rotated.RPMLimit != rpm || rotated.ExpiresAt == nil || !rotated.ExpiresAt.Equal(expiry) {
		t.Fatalf("轮换设置不完整: %+v %v", rotated, err)
	}
	if _, err := db.KeyByHash(ctx, HashKey(secret)); err != ErrNotFound {
		t.Fatalf("旧凭据未立即撤销: %v", err)
	}
	if _, err := db.KeyByHash(ctx, HashKey(next)); err != nil {
		t.Fatalf("新凭据无效: %v", err)
	}
	unchanged, _, err := db.RotateKey(ctx, by, original.ID)
	if err != nil || unchanged.MaxBudget == nil || *unchanged.MaxBudget != budget || !unchanged.ExpiresAt.Equal(expiry) {
		t.Fatalf("缺省轮换丢失设置: %+v %v", unchanged, err)
	}
}
