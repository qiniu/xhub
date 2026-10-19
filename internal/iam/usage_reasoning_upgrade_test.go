package iam

import (
	"testing"

	"github.com/sunqirui1987/xhub/cmd/regression/testsupport"
)

// TestUsageReasoningEffortUpgrade 验证空旧库及历史账单通过真实 Open 自动补齐推理强度字段。
// 前置专属 schema，删除新列模拟旧结构；旧记录保持未知，新记录与重放保持强度及唯一计费，验证非空约束。
// 测试自己关闭连接，testsupport 删除 schema，无外部供应商依赖。
func TestUsageReasoningEffortUpgrade(t *testing.T) {
	for _, history := range []bool{false, true} {
		name := "空旧库"
		if history {
			name = "保留历史账单"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			dsn := testsupport.Postgres(t, "reasoning_upgrade")
			db, err := Open(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			old := usageRecord("historical", "", 1.25)
			old.ReasoningEffort = "high"
			if history {
				if err := db.RecordUsage(ctx, []UsageRecord{old}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Engine.Exec("ALTER TABLE usage_events DROP COLUMN reasoning_effort"); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, dsn)
			if err != nil {
				t.Fatalf("旧库无法通过生产启动补列: %v", err)
			}
			defer reopened.Close()
			if history {
				event, err := reopened.GetUsageEvent(ctx, UsageQuery{}, old.RequestID)
				if err != nil || event == nil || event.ReasoningEffort != "" || event.Cost != old.Cost {
					t.Fatalf("历史强度被伪造或账单改变: %+v %v", event, err)
				}
			}
			current := usageRecord("after-upgrade", "", 2.5)
			current.ReasoningEffort = "medium"
			for range 2 {
				if err := reopened.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
				if err := reopened.RecordUsage(ctx, []UsageRecord{current}); err != nil {
					t.Fatal(err)
				}
			}
			event, err := reopened.GetUsageEvent(ctx, UsageQuery{}, current.RequestID)
			if err != nil || event == nil || event.ReasoningEffort != "medium" || countEvents(t, reopened, current.RequestID) != 1 {
				t.Fatalf("推理强度或幂等入库错误: %+v %v", event, err)
			}
			if _, err := reopened.Engine.Exec("UPDATE usage_events SET reasoning_effort=NULL WHERE request_id=?", current.RequestID); err == nil {
				t.Fatal("补列遗漏 NOT NULL 约束")
			}
			wantRequests, wantCost := int64(1), current.Cost
			if history {
				wantRequests++
				wantCost += old.Cost
			}
			if dailyRequests(t, reopened, "") != wantRequests || dailyCost(t, reopened, "") != wantCost {
				t.Fatal("升级或重放改变历史请求数/费用")
			}
		})
	}
}
