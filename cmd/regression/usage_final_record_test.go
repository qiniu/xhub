package regression

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

// TestUsageFinalErrorsDoNotPoisonAdmission 验证常规错误和早期错误只保存一份完整账单，后续软预算调用仍可用。
// 前置私有PG及本地上游，覆盖有无磁盘、入站护栏、鉴权、额度和执行链错误；验证日志唯一、pending为空，最后回收schema及磁盘。
func TestUsageFinalErrorsDoNotPoisonAdmission(t *testing.T) {
	for _, disk := range []bool{false, true} {
		for _, kind := range []string{"auth", "quota", "pre_call", "upstream", "idempotent"} {
			t.Run(fmt.Sprintf("disk=%v/%s", disk, kind), func(t *testing.T) {
				t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
				dir := ""
				if disk {
					dir = filepath.Join(t.TempDir(), "spool")
				}
				t.Setenv("XHUB_USAGE_SPOOL_DIR", dir)
				const model = "final-errors"
				h := newHarness(t, chatDeployment(model))
				t.Cleanup(func() { _ = h.gw.CloseUsageSpool() })
				admin := h.adminSession()
				scope := h.openScope(t, admin, "final-errors")
				token := scope.key
				want := 400
				switch kind {
				case "auth":
					token = "invalid-test-key"
					want = 401
				case "quota":
					// 软预算允许微小正余额请求完成；额度错误用零余额触发。
					h.setKeyBudget(t, admin, scope.key, 0)
					want = 403
				case "pre_call", "post_call":
					h.putGuardrail(t, "final-block", guardrailRow("final-block", "always_block", nil, kind))
				case "upstream", "idempotent":
					h.scriptStatus(model, 400)
				}
				headers := map[string]string{}
				if kind == "idempotent" {
					headers["Idempotency-Key"] = "failure-replay"
				}
				blocked := h.doHeaders(http.MethodPost, "/v1/chat/completions", token, chatRequest(model, "Check single failure record."), headers)
				if kind == "idempotent" {
					replayed := h.doHeaders(http.MethodPost, "/v1/chat/completions", token, chatRequest(model, "Check single failure record."), headers)
					if replayed.status != blocked.status || replayed.header("x-litellm-call-id") != blocked.header("x-litellm-call-id") {
						t.Fatal("错误幂等重放没有复用原响应")
					}
				}
				if blocked.status != want {
					t.Fatalf("%s错误状态: %s", kind, blocked.describe())
				}
				h.flushSpend()
				id := blocked.header("x-litellm-call-id")
				rows, err := h.db.Engine.Table("usage_events").Where("request_id = ?", id).Count()
				if err != nil || rows != 1 {
					t.Fatalf("错误日志非唯一: rows=%d err=%v", rows, err)
				}
				assertUsageQueuesEmpty(t, h, dir)
				h.setKeyBudget(t, admin, scope.key, 10)
				if kind == "pre_call" || kind == "post_call" {
					h.deleteGuardrail(t, "final-block")
				}
				h.scriptStatus(model, 0)
				h.ok(http.MethodPost, "/v1/chat/completions", scope.key, chatRequest(model, "Continue after the rejected request."))
				h.flushSpend()
				assertUsageQueuesEmpty(t, h, dir)
			})
		}
	}
}

// assertUsageQueuesEmpty 检查完整结算后的PG及本机队列没有冲突或残留，供错误恢复回归调用。
// 参数为测试、独立网关和可选目录；无返回，失败指出残留介质；不修改数据，harness负责清理。
func assertUsageQueuesEmpty(t *testing.T, h *harness, dir string) {
	t.Helper()
	pending, err := h.db.Engine.Table("usage_pending").Count()
	if err != nil || pending != 0 {
		t.Fatalf("重复错误快照污染PGpending: %d %v", pending, err)
	}
	if dir != "" {
		records, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		quarantined, _ := filepath.Glob(filepath.Join(dir, "quarantine", "*"))
		if len(records)+len(quarantined) != 0 {
			t.Fatalf("磁盘错误快照未清空: pending=%d quarantine=%d", len(records), len(quarantined))
		}
	}
}

// TestUsageSpoolFallbackErrors 验证本机队列启用时多部署最终成功、最终失败及额度中断仍唯一结算。
// 前置私有PG和本地供应商，复用完整回退业务场景并断言无残留；目录与schema由测试框架清理。
func TestUsageSpoolFallbackErrors(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", filepath.Join(t.TempDir(), "spool"))
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	fallbackSimulated(t)
}
