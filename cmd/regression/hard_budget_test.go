package regression

import (
	"fmt"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/gateway/billing"
	"net/http"
	"testing"
	"time"
)

// hardBudgetDeployment 声明本地供应商执行且测试可核对的完整输入/输出范围；不依赖外部模型目录。
// 参数为模型名，返回明确单价和 cap 能力的部署，无持久副作用。
func hardBudgetDeployment(model string) config.ModelEntry {
	dep := chatDeployment(model)
	dep.ModelInfo["max_input_tokens"] = 1024
	dep.ModelInfo["max_output_tokens"] = 8192
	dep.ModelInfo["supported_parameters"] = []string{"max_tokens"}
	return dep
}

// hardBudgetAmount 计算夹具公开的预占上界；参数为部署，返回金额，失败终止测试，不调用供应商。
func hardBudgetAmount(t *testing.T, dep config.ModelEntry) float64 {
	t.Helper()
	plan, _, err := billing.StrictBudgetPlan(dep, []byte(`{"messages":[{"role":"user","content":"Test budget."}]}`), "openai-chat", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return plan.Amount
}

// TestHardBudgetFinalProtocolCaps 验证三协议标准/原生入口保留客户端明确输出上限，不注入预算cap。
// 前置每例私有 schema 和本地供应商；响应不带预占cap，完整用量落账且无预占，不调用付费上游。
func TestHardBudgetFinalProtocolCaps(t *testing.T) {
	for _, tc := range []struct{ protocol, transport, path, native, cap string }{
		{"chat", "bypass_openai_chat", "/v1/chat/completions", "/bypass/openai/v1/chat/completions", "max_tokens"},
		{"responses", "bypass_openai_responses", "/v1/responses", "/bypass/openai/v1/responses", "max_output_tokens"},
		{"messages", "bypass_anthropic_messages", "/v1/messages", "/bypass/anthropic/v1/messages", "max_tokens"},
	} {
		for _, native := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/native=%t", tc.protocol, native), func(t *testing.T) {
				t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
				dep := hardBudgetDeployment("hard-" + tc.protocol)
				dep.ModelInfo["transport"] = tc.transport
				dep.ModelInfo["endpoint_types"] = []string{tc.protocol, "bypass:" + map[string]string{"chat": "openai-chat", "responses": "openai-responses", "messages": "anthropic-messages"}[tc.protocol]}
				h := newHarness(t, dep)
				if tc.protocol == "responses" {
					h.scriptBody(dep.ModelName, []byte(`{"id":"resp_budget","object":"response","status":"completed","output":[{"id":"msg_budget","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"budget-ok","annotations":[]}]}],"usage":{"input_tokens":11,"output_tokens":5,"total_tokens":16}}`))
				}
				admin := h.adminSession()
				c := h.openScope(t, admin, "caps")
				h.setKeyBudget(t, admin, c.key, 1)
				body := chatRequest(dep.ModelName, "Verify explicit output limit.")
				body[tc.cap] = 32
				if tc.protocol == "responses" {
					delete(body, "messages")
					body["input"] = "Verify default output limit."
				}
				path := tc.path
				if native {
					path = tc.native
				}
				got := h.ok(http.MethodPost, path, c.key, body)
				if got.header("X-XHub-Max-Output-Tokens") != "" {
					t.Fatalf("协议未公开输出 cap: %s", got.describe())
				}
				calls := h.upstreamCalls()
				if len(calls) != 1 || calls[0].Body[tc.cap] != float64(32) {
					t.Fatalf("协议实际出站 cap 不一致: %+v", calls)
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), c.keyID)
				if err != nil || len(holds) != 0 {
					t.Fatalf("正常完整 usage 没有释放占用: %+v %v", holds, err)
				}
			})
		}
	}
}
