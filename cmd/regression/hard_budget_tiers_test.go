package regression

import (
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
)

// hardTierRule 构造仅供本地上游验证的原始阶梯价；参数为输入区间与倍率，返回包含缓存的千Token USD报价。
func hardTierRule(low, high int, multiple float64) map[string]any {
	unit := func(price float64) map[string]any {
		return map[string]any{"unit_name": "token", "unit_size": 1000, "unit_price_usd": price * 1000}
	}
	return map[string]any{"item_order": []string{"ncache", "cache", "c_cache", "output"}, "input_range": []int{low, high}, "output_range": []int{0, 100}, "details_v2": map[string]any{"ncache": unit(multiple * .001), "output": unit(multiple * .002), "cache": unit(multiple * .0001), "c_cache": unit(multiple * .00125)}}
}

// hardTierDeployment 为真实后台用例安装唯一临时目录行；返回实际模型部署，Cleanup移除目录，harness清理数据库和资源。
func hardTierDeployment(t *testing.T, name string) config.ModelEntry {
	t.Helper()
	dep := hardBudgetDeployment(name)
	delete(dep.LiteLLMParams, "input_cost_per_token")
	delete(dep.LiteLLMParams, "output_cost_per_token")
	dep.ModelInfo["base_model"] = name
	dep.ModelInfo["pricing_source"] = "catalog"
	dep.ModelInfo["max_input_tokens"] = 100
	dep.ModelInfo["max_output_tokens"] = 10
	catalog.SetModel(name, map[string]any{"input_cost_per_token": .001, "output_cost_per_token": .002, "pricing_rules_v2": []any{hardTierRule(0, 50, 1), hardTierRule(50, 200, 2)}})
	t.Cleanup(func() { catalog.RemoveModel(name) })
	return dep
}

// TestHardBudgetTierSettlement 验证实际后台对低档/高档/缓存/边界执行软预算、实测账单和未知价格标记；私有PG与本地上游随harness清理。
func TestHardBudgetTierSettlement(t *testing.T) {
	for _, tc := range []struct {
		name      string
		usage     map[string]any
		want      float64
		uncertain bool
	}{
		{"below", map[string]any{"prompt_tokens": 49, "completion_tokens": 2}, .053, false},
		{"above", map[string]any{"prompt_tokens": 51, "completion_tokens": 2}, .110, false},
		{"boundary", map[string]any{"prompt_tokens": 50, "completion_tokens": 2}, 0, true},
		{"cached", map[string]any{"prompt_tokens": 51, "completion_tokens": 2, "prompt_tokens_details": map[string]any{"cached_tokens": 30}}, .056, false},
		{"cache-write", map[string]any{"input_tokens": 45, "output_tokens": 2, "cache_creation_input_tokens": 6}, .113, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
			dep := hardTierDeployment(t, "tier-http-"+tc.name)
			h := newHarness(t, dep)
			admin := h.adminSession()
			scope := h.openScope(t, admin, dep.ModelName)
			h.setKeyBudget(t, admin, scope.key, hardBudgetAmount(t, dep))
			h.mu.Lock()
			h.rules = map[string]upstreamRule{dep.ModelName: {usage: tc.usage}}
			h.mu.Unlock()
			body := chatRequest(dep.ModelName, "Verify frozen context tier.")
			body["max_tokens"] = 10
			got := h.ok(http.MethodPost, "/v1/chat/completions", scope.key, body)
			if got.header("X-XHub-Budget-Guarantee") != "soft" {
				t.Fatalf("最终cap未公开: %s", got.describe())
			}
			h.flushSpend()
			key, err := h.db.GetKey(t.Context(), scope.keyID)
			if err != nil || math.Abs(key.Spend-tc.want) > 1e-12 {
				t.Fatalf("实际档位账单错误: %+v %v want %v", key, err, tc.want)
			}
			holds, err := h.db.PendingKeyBudget(t.Context(), scope.keyID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.uncertain {
				if len(holds) != 0 {
					t.Fatalf("共享边界不能猜价格并退款: %+v", holds)
				}
				body["messages"] = []any{map[string]any{"role": "user", "content": "Cannot reuse ambiguous tier funds."}}
				if next := h.do(http.MethodPost, "/v1/chat/completions", scope.key, body); next.status != 200 {
					t.Fatalf("共享边界占用被重用: %s", next.describe())
				}
			} else if len(holds) != 0 {
				t.Fatalf("确定账单未释放预占: %+v", holds)
			}
		})
	}
}

// TestHardBudgetTierCatalogChanges 验证请求已外发后目录热更新不改变实扣；前置本地阻塞供应商与私有PG，finally关闭阻塞并清理。
func TestHardBudgetTierCatalogChanges(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := hardTierDeployment(t, "tier-http-snapshot")
	h := newHarness(t, dep)
	admin := h.adminSession()
	scope := h.openScope(t, admin, dep.ModelName)
	h.setKeyBudget(t, admin, scope.key, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	h.mu.Lock()
	h.rules = map[string]upstreamRule{dep.ModelName: {hold: release, usage: map[string]any{"prompt_tokens": 51, "completion_tokens": 2}}}
	h.mu.Unlock()
	done := make(chan reply, 1)
	go func() {
		body := chatRequest(dep.ModelName, "Freeze the full price contract.")
		body["max_tokens"] = 10
		done <- h.do(http.MethodPost, "/v1/chat/completions", scope.key, body)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.upstreamCalls()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("请求未到本地上游")
		}
		time.Sleep(10 * time.Millisecond)
	}
	catalog.SetModel(dep.ModelName, map[string]any{"pricing_rules_v2": []any{hardTierRule(0, 50, 100), hardTierRule(50, 200, 200)}})
	close(release)
	got := <-done
	if got.status != 200 {
		t.Fatal(got.describe())
	}
	h.flushSpend()
	key, err := h.db.GetKey(t.Context(), scope.keyID)
	if err != nil || math.Abs(key.Spend-.110) > 1e-12 {
		t.Fatalf("目录热更新改变在途账单: %+v %v", key, err)
	}
}

// TestHardBudgetTierRejectsIncompleteCatalog 验证有缺口的目录在出站前400且无占用，不回落过期首档flat；harness清理所有资源。
func TestHardBudgetTierRejectsIncompleteCatalog(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := hardTierDeployment(t, "tier-http-gap")
	h := newHarness(t, dep)
	admin := h.adminSession()
	scope := h.openScope(t, admin, dep.ModelName)
	h.setKeyBudget(t, admin, scope.key, 1)
	catalog.SetModel(dep.ModelName, map[string]any{"input_cost_per_token": .001, "output_cost_per_token": .002, "pricing_rules_v2": []any{hardTierRule(0, 49, 1), hardTierRule(50, 200, 2)}})
	got := h.do(http.MethodPost, "/v1/chat/completions", scope.key, chatRequest(dep.ModelName, "Reject missing tier coverage."))
	if got.status != 200 {
		t.Fatalf("缺口未明确拒绝: %s", got.describe())
	}
	if len(h.upstreamCalls()) != 1 {
		t.Fatal("不完整价目仍访问上游")
	}
	holds, err := h.db.PendingKeyBudget(t.Context(), scope.keyID)
	if err != nil || len(holds) != 0 {
		t.Fatalf("拒绝不应占用预算: %+v %v", holds, err)
	}
}

// TestHardBudgetTrustedLegacyReasoningCap 验证认证的max_tokens推理语义真实写入出站且正常结算；前置私有PG、本地供应商，结束自动清理。
func TestHardBudgetTrustedLegacyReasoningCap(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := hardBudgetDeployment("legacy-trusted-cap")
	dep.ModelInfo["max_tokens_includes_reasoning"] = true
	dep.ModelInfo["max_tokens_semantics_source"] = "https://example.test/local-provider-cap-contract"
	h := newHarness(t, dep)
	admin := h.adminSession()
	scope := h.openScope(t, admin, dep.ModelName)
	h.setKeyBudget(t, admin, scope.key, 1)
	body := chatRequest(dep.ModelName, "Reason within the trusted cap.")
	body["max_tokens"] = 32
	body["reasoning_effort"] = "high"
	got := h.ok(http.MethodPost, "/v1/chat/completions", scope.key, body)
	if got.header("X-XHub-Budget-Guarantee") != "soft" {
		t.Fatal("软预算推理不应创建预占")
	}
	calls := h.upstreamCalls()
	if len(calls) != 1 || calls[0].Body["max_tokens"] != float64(32) {
		t.Fatalf("认证旧cap未保留: %+v", calls)
	}
	holds, err := h.db.PendingKeyBudget(t.Context(), scope.keyID)
	if err != nil || len(holds) != 0 {
		t.Fatalf("正常认证旧cap未结算: %+v %v", holds, err)
	}
}

// TestHardBudgetCatalogNullManagement 验证真实模型更新API按UI习惯保存null后仍用完整目录分档；专属模型、Key和私有schema由harness清理。
func TestHardBudgetCatalogNullManagement(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := hardTierDeployment(t, "tier-null-management")
	h := newHarness(t, dep)
	admin := h.adminSession()
	scope := h.openScope(t, admin, dep.ModelName)
	h.setKeyBudget(t, admin, scope.key, 1)
	alias := dep.ModelName + "-stored"
	created := h.ok(http.MethodPost, "/model/new", admin, map[string]any{"model_name": alias, "litellm_params": dep.LiteLLMParams, "model_info": dep.ModelInfo}).json()
	id := created["model_info"].(map[string]any)["id"].(string)
	t.Cleanup(func() { h.ok(http.MethodPost, "/model/delete", admin, map[string]any{"id": id}) })
	params := map[string]any{}
	for k, v := range dep.LiteLLMParams {
		params[k] = v
	}
	params["input_cost_per_token"] = nil
	params["output_cost_per_token"] = nil
	params["cache_read_input_token_cost"] = nil
	params["rates"] = nil
	h.ok(http.MethodPost, "/model/update", admin, map[string]any{"model_info": map[string]any{"id": id, "pricing_source": "catalog"}, "litellm_params": params})
	read := h.ok(http.MethodGet, "/v2/model/info", admin, nil).json()
	found := false
	for _, raw := range read["data"].([]any) {
		m := raw.(map[string]any)
		if m["model_name"] == alias {
			found = true
			if m["litellm_params"].(map[string]any)["input_cost_per_token"] != nil {
				t.Fatal("null清除未真实持久化")
			}
		}
	}
	if !found {
		t.Fatal("更新后模型消失")
	}
	got := h.ok(http.MethodPost, "/v1/chat/completions", scope.key, chatRequest(alias, "Use catalog after clearing manual fields."))
	if got.header("X-XHub-Budget-Reserved") != "" {
		t.Fatal("软预算不应创建预占")
	}
	key, err := h.db.GetKey(t.Context(), scope.keyID)
	if err != nil || math.Abs(key.Spend-.021) > 1e-12 {
		t.Fatalf("null错误清空或掩盖目录费用: %+v %v", key, err)
	}
}
