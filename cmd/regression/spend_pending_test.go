package regression

import (
	"net/http"
	"testing"

	"github.com/sunqirui1987/xhub/internal/gateway"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestSpendPendingFailureRecovery 验证无 Redis 及 Redis 入队失败后的真实 HTTP 调用仍成功，账单事务失败可由新网关恢复。
// 前置私有 PostgreSQL schema/本地供应商，Redis 场景创建独占实例；覆盖唯一外发、零部分扣款、完整日志和五层一次结算。
// CHECK 只拒绝本例模型的事件，恢复后重建运行时并重复 Flush；最后删除 schema、停止 Redis 与所有 HTTP 服务。
func TestSpendPendingFailureRecovery(t *testing.T) {
	for _, useRedis := range []bool{false, true} {
		t.Run(map[bool]string{false: "无Redis", true: "Redis入队失败"}[useRedis], func(t *testing.T) {
			t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
			if useRedis {
				rdb := isolatedSpendRedis(t)
				if err := rdb.Set(t.Context(), "xhub:spendlog", "broken-queue", 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			const model = "regression-pending-recovery"
			h := newHarness(t, chatDeployment(model))
			admin := h.adminSession()
			c := h.openScope(t, admin, "pending-recovery")
			before := h.moneyOf(t, c)
			if _, err := h.db.Engine.Exec("ALTER TABLE usage_events ADD CONSTRAINT reject_pending_model CHECK (model <> 'regression-pending-recovery')"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = h.db.Engine.Exec("ALTER TABLE usage_events DROP CONSTRAINT IF EXISTS reject_pending_model")
			})
			r := h.ok(http.MethodPost, "/v1/chat/completions", c.key, chatRequest(model, "Verify durable usage recovery."))
			callID, cost := r.header("x-litellm-call-id"), parseFloatOrZero(r.header("x-litellm-response-cost"))
			if callID == "" || cost <= 0 || len(h.upstreamCalls()) != 1 {
				t.Fatalf("调用未保持唯一上游成功: call=%s cost=%v calls=%d", callID, cost, len(h.upstreamCalls()))
			}
			h.flushSpend()
			pending, err := h.db.Engine.Table("usage_pending").Where("request_id = ?", callID).Count()
			if err != nil || pending != 1 {
				t.Fatalf("事务失败后丢失待结算快照: n=%d err=%v", pending, err)
			}
			if after := h.moneyOf(t, c); !after.grewBy(before, 0) {
				t.Fatalf("失败事务改变费用: before=%+v after=%+v", before, after)
			}
			if result := h.do(http.MethodGet, "/spend/logs/ui/"+callID, admin, nil); result.status != http.StatusNotFound {
				t.Fatalf("失败事务发布账单: %d %s", result.status, result.body)
			}
			if blocked := h.do(http.MethodPost, "/v1/chat/completions", c.key, chatRequest(model, "Wait until pending billing settles.")); blocked.status != 503 {
				t.Fatalf("待结算费用未阻止读取旧余额: %s", blocked.describe())
			}
			if _, err := h.db.Engine.Exec("ALTER TABLE usage_events DROP CONSTRAINT reject_pending_model"); err != nil {
				t.Fatal(err)
			}
			// 使用新的数据库连接和网关实例恢复，不能靠原请求对象或进程内重试列表侥幸通过。
			reopened, err := iam.Open(t.Context(), h.gw.Cfg.GeneralSettings.DatabaseURL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			fresh := gateway.New(h.gw.Cfg, h.store, reopened)
			if fresh.Live != nil {
				t.Cleanup(func() { _ = fresh.Live.Close() })
			}
			for range 3 {
				fresh.FlushSpend()
			}
			pending, err = reopened.Engine.Table("usage_pending").Count()
			if err != nil || pending != 0 {
				t.Fatalf("恢复后待结算未清理: n=%d err=%v", pending, err)
			}
			if after := h.moneyOf(t, c); !after.grewBy(before, cost) {
				t.Fatalf("恢复后五层费用未唯一结算: before=%+v after=%+v cost=%v", before, after, cost)
			}
			if len(h.upstreamCalls()) != 1 {
				t.Fatal("账单恢复重新调用了付费上游")
			}
			if logs := h.successRows(t, admin, model); len(logs) != 1 {
				t.Fatalf("恢复后日志数=%d，预期1", len(logs))
			}
			log := h.ok(http.MethodGet, "/spend/logs/ui/"+callID, admin, nil).json()
			if log["status"] != "success" || !nearlyEqual(log["spend"].(float64), cost) {
				t.Fatalf("恢复后日志不完整: %+v", log)
			}
			usage := h.ok(http.MethodGet, "/user/daily/activity/aggregated?user_id="+c.userID, admin, nil).json()
			if got := usage["metadata"].(map[string]any)["total_api_requests"]; got != float64(2) {
				t.Fatalf("恢复后汇总次数=%v，预期2（成功和存储拒绝各一次）", got)
			}
			monitor := h.ok(http.MethodGet, "/models/monitor?model="+model, admin, nil).json()
			if got := monitor["summary"].(map[string]any)["requests"]; got != float64(1) {
				t.Fatalf("恢复后供应商监控次数=%v，预期1（拒绝请求未出站）", got)
			}
		})
	}
}
