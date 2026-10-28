package regression

import (
	"reflect"
	"strings"
	"testing"
)

// TestProviderBatchBoundary 通过真实HTTP/PG验证供应商前缀的旧批处理接口不把公共配置当作任务。
// 前置独立schema、存量同名配置与可计数本地模型；验证原鉴权、明确错误、零副作用及正常计费和零预算拦截。
// 配置夹具显式清理，用户、Key与账单由harness回收；不访问外部供应商或生产。
func TestProviderBatchBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("batch-boundary"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "batch-boundary")
	for _, id := range []string{"batches", "fixture-job", "cancel"} {
		if err := h.store.PutKV("fixture-provider", id, `{"id":"`+id+`","name":"keep-config","private_fixture":"must-not-echo"}`); err != nil {
			t.Fatal(err)
		}
	}
	before, err := h.store.ListKV("fixture-provider")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, method, path string }{
		{"list", "GET", "/fixture-provider/v1/batches"},
		{"create", "POST", "/fixture-provider/v1/batches"},
		{"retrieve", "GET", "/fixture-provider/v1/batches/fixture-job"},
		{"cancel", "POST", "/fixture-provider/v1/batches/fixture-job/cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {owner.key, 403}, {admin, 501}} {
				out := h.do(tc.method, tc.path, actor.token, map[string]any{"input_file_id": "input-fixture", "private_fixture": "must-not-echo"})
				if out.status != actor.status {
					t.Errorf("旧批处理状态错误: %s", out.describe())
				}
				if actor.token == admin && (!strings.Contains(out.text(), "unsupported_operation") || strings.Contains(out.text(), "must-not-echo") || out.header("Cache-Control") != "no-store") {
					t.Errorf("旧批处理伪成功或泄漏配置: %s", out.describe())
				}
			}
			after, err := h.store.ListKV("fixture-provider")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Errorf("旧批处理误写公共配置: %v err=%v", after, err)
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("未实现批处理不能调用供应商")
			}
		})
	}
	for _, invalid := range []any{[]any{}, "invalid-object", map[string]any{"jsonrpc": "2.0", "method": "submit"}} {
		if out := h.do("POST", "/fixture-provider/v1/batches", admin, invalid); out.status != 400 {
			t.Fatal("JSON校验改变", out.describe())
		}
	}
	// OpenAI前缀与标准批处理原本走LLM鉴权，维持501而非改成管理权限。
	for _, path := range []string{"/v1/batches", "/batches", "/openai/v1/batches", "/openai/v1/batches/fixture-job/cancel"} {
		if out := h.do("POST", path, owner.key, map[string]any{}); out.status != 501 {
			t.Errorf("原数据面批处理边界改变: %s", out.describe())
		}
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("batch-boundary", "Verify chat remains available after rejected batch operations."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if len(h.upstreamCalls()) != 1 || cost <= 0 {
		t.Fatal("正常推理与计费异常")
	}
	blocked := h.openScope(t, admin, "batch-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("batch-boundary", "Verify zero budget remains blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("预算拦截改变", out.describe())
	}
	for _, id := range []string{"batches", "fixture-job", "cancel"} {
		if err := h.store.DeleteKV("fixture-provider", id); err != nil {
			t.Fatal(err)
		}
	}
}
