package iam

import (
	"math"
	"testing"
	"time"
)

// TestKeyCapsDoNotReserve 验证 Key 上限不预留、0 仅拦本 Key、个人耗尽拦全部及兄弟热费用故障隔离。
// 前置纯内存额度树；无外部资源，无需清理；正数、零、超支及非法金额都需明确结果。
func TestKeyCapsDoNotReserve(t *testing.T) {
	for _, spend := range []float64{0, 499, 500, 501} {
		for _, target := range []string{"user:u", "key:fixed", "key:shared", "key:zero"} {
			user := &quotaNode{ID: "u", Kind: "user", Limit: quotaLimit(500), Spend: spend}
			fixed := &quotaNode{ID: "fixed", Kind: "key", Parent: "user:u", Limit: quotaLimit(500)}
			shared := &quotaNode{ID: "shared", Kind: "key", Parent: "user:u"}
			zero := &quotaNode{ID: "zero", Kind: "key", Parent: "user:u", Limit: quotaLimit(0)}
			user.Children = []*quotaNode{fixed, shared, zero}
			nodes := map[string]*quotaNode{"user:u": user, "key:fixed": fixed, "key:shared": shared, "key:zero": zero}
			_, _, scope, err := quotaPath(nodes, target, func(kind, id string, _ ...time.Time) float64 {
				if kind == "key" && nodes[target].Kind != "key" {
					return math.NaN()
				}
				return 0
			})
			if err != nil || (scope != "") != (spend >= 500 || target == "key:zero") {
				t.Fatalf("共享预算错误 target=%s spend=%v scope=%s err=%v", target, spend, scope, err)
			}
		}
	}
	for _, amount := range []float64{0, 500, -1, math.NaN(), math.Inf(1)} {
		node := &quotaNode{Kind: "key", Limit: &amount, Spend: 600}
		value, err := quotaUnused(node)
		invalid := amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0)
		if (err != nil) != invalid || value != 0 || quotaReserved(node) != 0 {
			t.Fatalf("Key 上限校验/预留错误: %v %v %v", amount, value, err)
		}
	}
}

// TestKeyCapCannotBorrowSiblingUserReservation 验证固定 Key 不能越过共享祖先借用其他用户的预留。
// 纯内存组织额度已全部分给兄弟用户；Key 与控制台都应被组织拦截，无外部资源需清理。
func TestKeyCapCannotBorrowSiblingUserReservation(t *testing.T) {
	for _, target := range []string{"user:u", "key:k"} {
		org := &quotaNode{ID: "o", Kind: "org", Limit: quotaLimit(500)}
		user := &quotaNode{ID: "u", Kind: "user", Parent: "org:o"}
		sibling := &quotaNode{ID: "s", Kind: "user", Parent: "org:o", Limit: quotaLimit(500)}
		key := &quotaNode{ID: "k", Kind: "key", Parent: "user:u", Limit: quotaLimit(500)}
		user.Children = []*quotaNode{key}
		org.Children = []*quotaNode{user, sibling}
		_, _, scope, err := quotaPath(map[string]*quotaNode{"org:o": org, "user:u": user, "user:s": sibling, "key:k": key}, target, nil)
		if err != nil || scope != "org" {
			t.Fatalf("Key 借用了其他用户额度 target=%s scope=%s err=%v", target, scope, err)
		}
	}
}
