package gateway

import (
	"net"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestOrganizationMaintenanceWithoutRedis 以生产启动路径验证无 Redis 的空闲组织自动充值。
// 参数 t 为测试上下文，无返回；前置隔离 PostgreSQL，不发送组织读取或推理以免触发惰性重置。
// 监听关闭后等待服务退出，testGatewayStores 清理数据库；无外部模型依赖。
func TestOrganizationMaintenanceWithoutRedis(t *testing.T) {
	cfg, st, db := testGatewayStores(t)
	cfg.GeneralSettings.RedisURL = ""
	cfg.GeneralSettings.SpendLogFlushIntervalSeconds = 1
	org, err := db.CreateOrgWithConfig(t.Context(), iam.Actor{}, "idle-budget", nil, iam.OrgConfig{BudgetDuration: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Engine.Exec("UPDATE organizations SET spend=8,budget_reset_at=? WHERE id=?", time.Now().Add(-time.Hour), org.ID); err != nil {
		t.Fatal(err)
	}
	s := New(cfg, st, db)
	if s.Live != nil {
		t.Fatal("测试必须禁用 Redis")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- s.serveListener(ln) }()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			t.Error("服务未退出")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var got iam.Organization
		if _, err = db.Engine.ID(org.ID).Get(&got); err != nil {
			t.Fatal(err)
		}
		if got.BudgetPeriodStart != nil && got.Spend == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("纯数据库空闲组织未自动重置: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	count, err := db.Engine.Where("action=? AND object_id=?", "org.budget_reset", org.ID).Count(&iam.AuditEntry{})
	if err != nil || count != 1 {
		t.Fatalf("空闲重置审计必须恰好一次: %d %v", count, err)
	}
}

// TestOrganizationPeriod 验证未充值、已充值组织的热消费起点；纯内存输入，无副作用或清理。
func TestOrganizationPeriod(t *testing.T) {
	if got := organizationPeriod(&iam.Organization{}); !got.IsZero() {
		t.Fatal("缺失起点不应推断")
	}
	now := time.Now()
	if got := organizationPeriod(&iam.Organization{BudgetPeriodStart: &now}); !got.Equal(now) {
		t.Fatal("丢失周期起点")
	}
}
