package regression

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSeedanceSettlementUsesMeasuredBandAndDeduplicates 验证官方 Ark 任务只按成功实测用量结算一次。
// 参数 t：测试上下文；返回：无。前置本地上游与真实数据库，核对状态边界和账单；schema 自动删除。
func TestSeedanceSettlementUsesMeasuredBandAndDeduplicates(t *testing.T) {
	var response atomic.Value
	response.Store(map[string]any{"status": "running", "usage": map[string]any{"completion_tokens": 40594}})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, map[string]any{"id": "billing-task"})
			return
		}
		writeJSON(w, response.Load())
	}))
	defer up.Close()
	model := "volcengine/doubao-seedance-2-0-fast-260128"
	dep := seedanceDeployment(model, "ark_contents_generation")
	dep.LiteLLMParams["api_base"] = up.URL
	dep.LiteLLMParams["output_cost_per_token"] = 3.33333e-6
	h := newHarness(t, dep)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "seedance-billing")
	before := h.moneyOf(t, owner)
	created := h.ok(http.MethodPost, "/api/v3/contents/generations/tasks", owner.key, map[string]any{"model": model, "resolution": "480p", "content": []any{map[string]any{"type": "text", "text": "apple"}}})
	originalID := created.header("x-litellm-call-id")
	rowsAtCreate := h.spendLogs(t, admin)
	if len(rowsAtCreate) != 1 || stringField(rowsAtCreate[0], "status") != "executing" {
		t.Fatalf("create lifecycle: %+v", rowsAtCreate)
	}
	for _, status := range []string{"queued", "creating_assets", "running", "failed", "expired", "unknown", ""} {
		response.Store(map[string]any{"status": status, "usage": map[string]any{"completion_tokens": 40594}})
		h.ok(http.MethodGet, "/api/v3/contents/generations/tasks/billing-task", owner.key, nil)
		rows := h.spendLogs(t, admin)
		if len(rows) != 1 || stringField(rows[0], "request_id") != originalID {
			t.Fatalf("poll created a second log: %+v", rows)
		}
		if status == "queued" && stringField(rows[0], "status") != "polling" {
			t.Fatalf("missing polling status: %+v", rows)
		}
		if status == "failed" && stringField(rows[0], "status") != "failed" {
			t.Fatalf("missing failure status: %+v", rows)
		}
		if status == "failed" {
			errors := h.ok(http.MethodGet, "/spend/logs/ui?status_filter=error", admin, nil)
			if len(rowsOf(errors, "data", "logs")) != 1 {
				t.Fatal("task failure missing from error tab", errors.describe())
			}
		}
		if !h.moneyOf(t, owner).same(before) {
			t.Fatalf("charged status %q", status)
		}
	}
	response.Store(map[string]any{"id": "billing-task", "status": "succeeded", "resolution": "480p", "usage": map[string]any{"completion_tokens": 40594, "total_tokens": 40594}})
	const want = 40594 * 3.33333e-6
	for i := 0; i < 3; i++ {
		r := h.ok(http.MethodGet, "/api/v3/contents/generations/tasks/billing-task", owner.key, nil)
		if !nearlyEqual(parseFloatOrZero(r.header("x-litellm-response-cost")), want) {
			t.Fatal("wrong measured cost", r.describe())
		}
		if got := h.moneyOf(t, owner); !got.grewBy(before, want) {
			t.Fatalf("poll %d lost/duplicate settlement: %+v", i, got)
		}
	}
	var settled []map[string]any
	rows := h.spendLogs(t, admin)
	for _, row := range rows {
		if stringField(row, "model") != model {
			continue
		}
		if numberOrZero(row["spend"]) > 0 {
			settled = append(settled, row)
		}
	}
	if len(settled) != 1 || len(rows) != 1 || stringField(settled[0], "request_id") != originalID {
		t.Fatalf("expected one paid event: %+v", settled)
	}
	id := stringField(settled[0], "request_id")
	if id == "" {
		id = stringField(settled[0], "id")
	}
	if strings.HasPrefix(id, "official-settlement:") || stringField(settled[0], "status") != "completed" {
		t.Fatal("settlement must update the original completed task", settled[0])
	}
	bill := breakdownOf(t, h, admin, id)
	raw, _ := json.Marshal(bill)
	if strings.Contains(string(raw), "\"fallback\":true") {
		t.Fatalf("wrong snapshot: %s", raw)
	}
}

// TestTaskLogsPreserveUnknownAndFreePricing 验证任务零账单在列表与详情保留未定价和真实免费区别。
// 参数 t 为测试上下文，无返回值；前置本地上游、真实网关和隔离数据库，无外部服务凭据。
// 成功任务重复查询不得增加日志或费用，未认证查询应失败；harness 和 defer 清理 schema 与本地服务。
func TestTaskLogsPreserveUnknownAndFreePricing(t *testing.T) {
	for _, free := range []bool{false, true} {
		t.Run(map[bool]string{false: "unpriced", true: "free"}[free], func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, map[string]any{"id": "pricing-task", "status": "succeeded", "usage": map[string]any{"completion_tokens": 100}})
			}))
			defer up.Close()
			model := "volcengine/regression-unknown-price"
			dep := seedanceDeployment(model, "ark_contents_generation")
			dep.LiteLLMParams["api_base"] = up.URL
			if free {
				dep.LiteLLMParams["output_cost_per_token"] = 0.0
			}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "task-pricing")
			before := h.moneyOf(t, owner)
			created := h.ok(http.MethodPost, "/api/v3/contents/generations/tasks", owner.key, map[string]any{
				"model": model, "content": []any{map[string]any{"type": "text", "text": "Verify request pricing."}},
			})
			callID := created.header("x-litellm-call-id")
			for i := 0; i < 2; i++ {
				h.ok(http.MethodGet, "/api/v3/contents/generations/tasks/pricing-task", owner.key, nil)
			}
			rows := h.spendLogs(t, admin)
			if len(rows) != 1 || numberOrZero(rows[0]["spend"]) != 0 || rows[0]["status"] != "completed" {
				t.Fatalf("任务应仅保留一条完成的零账单: %#v", rows)
			}
			want := "unpriced"
			if free {
				want = "priced"
			}
			metadata, _ := rows[0]["metadata"].(map[string]any)
			listBill, _ := metadata["cost_breakdown"].(map[string]any)
			bill := breakdownOf(t, h, admin, callID)
			if listBill["pricing_status"] != want || bill["pricing_status"] != want {
				t.Fatalf("列表与详情定价状态应为 %s: list=%#v detail=%#v", want, listBill, bill)
			}
			if !h.moneyOf(t, owner).same(before) {
				t.Fatal("零账单不应增加归属方支出")
			}
			if got := h.do(http.MethodGet, "/spend/logs/ui/"+callID, "", nil); got.status != http.StatusUnauthorized {
				t.Fatalf("未认证日志查询应拒绝: %s", got.describe())
			}
		})
	}
}
