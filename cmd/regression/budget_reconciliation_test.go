package regression

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestBudgetReconciliationHTTP 验证升级前历史uncertain记录保留后，只有管理会话可按证据核销且费用/次数/审计一致。
// 前置独立PG、无Redis、违约用量的本地供应商；分别确认免费和实际收费，重复/冲突/错误输入均有断言，harness清理schema与服务。
func TestBudgetReconciliationHTTP(t *testing.T) {
	for _, outcome := range []string{"no_charge", "charged"} {
		t.Run(outcome, func(t *testing.T) {
			t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
			const model = "reconcile-http"
			dep := hardBudgetDeployment(model)
			h := newHarness(t, dep)
			admin := h.adminSession()
			c := h.openScope(t, admin, model)
			h.setKeyBudget(t, admin, c.key, hardBudgetAmount(t, dep)*4)
			h.mu.Lock()
			h.rules = map[string]upstreamRule{model: {usage: map[string]any{"prompt_tokens": 11}}}
			h.mu.Unlock()
			call := h.ok(http.MethodPost, "/v1/chat/completions", c.key, chatRequest(model, "Preserve uncertain billing for manual reconciliation."))
			h.flushSpend()
			// 显式模拟升级前留下的未知预占；新软预算请求不能自动制造这种记录。
			var original iam.UsageEvent
			if _, err := h.db.Engine.Where("request_id = ?", call.header("x-litellm-call-id")).Get(&original); err != nil {
				t.Fatal(err)
			}
			_, err := h.db.Engine.Insert(&iam.BudgetReservation{RequestID: "legacy-hold", CallID: original.RequestID, Kind: "key", NodeID: c.keyID, KeyID: c.keyID, UserID: original.UserID, TeamID: original.TeamID, OrgID: original.OrganizationID, ProjectID: original.ProjectID, Amount: hardBudgetAmount(t, dep), Snapshot: `{"supplier":"local"}`, StartedAt: time.Now(), State: "uncertain", Reason: "Legacy provider outcome unknown"})
			if err != nil {
				t.Fatal(err)
			}

			before := h.moneyOf(t, c)
			listPath := "/budget/reservations?key_id=" + c.keyID + "&state=uncertain&page=1&page_size=50"
			for _, query := range []string{"page=0", "page_size=101", "page=bad", "state=resolved"} {
				if invalid := h.do(http.MethodGet, "/budget/reservations?"+query, admin, nil); invalid.status != 400 {
					t.Fatalf("非法列表参数被接受: %s", invalid.describe())
				}
			}
			list := h.ok(http.MethodGet, listPath, admin, nil).json()
			rows, ok := list["reservations"].([]any)
			if !ok || len(rows) != 1 || list["total"] != float64(1) {
				t.Fatalf("待核对列表不完整: %#v", list)
			}
			row := rows[0].(map[string]any)
			if row["can_reconcile"] != true || row["ledger_cost"] != float64(0) || row["blocked_reason"] != "" || row["call_id"] != call.header("x-litellm-call-id") {
				t.Fatalf("核对证据不准确: %#v", row)
			}
			if row["snapshot"] != nil || row["request_body"] != nil || row["price_snapshot"] != nil {
				t.Fatal("列表泄露不必要的内部证据")
			}
			cost := 0.0
			if outcome == "charged" {
				cost = 0.001
			}
			path := "/budget/reservations/" + row["reservation_id"].(string) + "/reconcile"
			input := map[string]any{"outcome": outcome, "verified_cost_usd": cost, "evidence": "Synthetic provider invoice checked", "expected_fingerprint": row["fingerprint"]}
			for _, credential := range []string{c.session, c.key} {
				if denied := h.do(http.MethodGet, listPath, credential, nil); denied.status != 403 {
					t.Fatalf("普通身份读取对账列表: %s", denied.describe())
				}
				if denied := h.do(http.MethodPost, path, credential, input); denied.status != 403 {
					t.Fatalf("普通身份修改对账: %s", denied.describe())
				}
			}
			bad := map[string]any{"outcome": outcome, "evidence": "Missing amount", "expected_fingerprint": row["fingerprint"]}
			if invalid := h.do(http.MethodPost, path, admin, bad); invalid.status != 400 || invalid.json()["error"].(map[string]any)["type"] != "budget_reconciliation_invalid" {
				t.Fatalf("遗漏金额未拒绝: %s", invalid.describe())
			}
			bad = map[string]any{"outcome": outcome, "verified_cost_usd": cost, "evidence": "stale", "expected_fingerprint": strings.Repeat("a", 64)}
			if stale := h.do(http.MethodPost, path, admin, bad); stale.status != 409 || stale.json()["error"].(map[string]any)["type"] != "budget_reconciliation_conflict" {
				t.Fatalf("陈旧证据未拒绝: %s", stale.describe())
			}
			got := h.ok(http.MethodPost, path, admin, input).json()
			resolution := got["resolution"].(map[string]any)
			if got["already_resolved"] != false || resolution["verified_cost_usd"] != cost || resolution["updated_cost_usd"] != cost || resolution["actor_id"] == "" {
				t.Fatalf("核对结果不完整: %#v", got)
			}
			repeated := h.ok(http.MethodPost, path, admin, input).json()
			if repeated["already_resolved"] != true {
				t.Fatalf("重复操作不幂等: %#v", repeated)
			}
			input["evidence"] = "Different evidence"
			if conflict := h.do(http.MethodPost, path, admin, input); conflict.status != 409 {
				t.Fatalf("不同结论未拒绝: %s", conflict.describe())
			}
			after := h.moneyOf(t, c)
			if !after.grewBy(before, cost) {
				t.Fatalf("五层费用不一致: before=%+v after=%+v delta=%v", before, after, cost)
			}
			holds, err := h.db.PendingKeyBudget(t.Context(), c.keyID)
			if err != nil || len(holds) != 0 {
				t.Fatalf("核对后仍占用: %+v %v", holds, err)
			}
			var event iam.UsageEvent
			if found, err := h.db.Engine.Where("request_id = ?", row["call_id"]).Get(&event); err != nil || !found || !nearlyEqual(event.Cost, cost) {
				t.Fatalf("单笔原调用费用未更新: %+v %v", event, err)
			}
			count, err := h.db.Engine.Where("request_id = ?", row["call_id"]).Count(new(iam.UsageEvent))
			if err != nil || count != 1 {
				t.Fatalf("核销制造重复推理事件: %d %v", count, err)
			}
			audit := h.ok(http.MethodGet, "/audit/logs?action=budget.reconcile&object_id="+row["reservation_id"].(string), admin, nil).json()
			if audit["total"] != float64(1) {
				t.Fatalf("核对审计丢失或重复: %#v", audit)
			}
			empty := h.ok(http.MethodGet, listPath, admin, nil).json()
			if empty["total"] != float64(0) {
				t.Fatalf("已核销仍列为待处理: %#v", empty)
			}
			if len(h.upstreamCalls()) != 1 {
				t.Fatal("核对流程额外调用付费上游")
			}
		})
	}
}
