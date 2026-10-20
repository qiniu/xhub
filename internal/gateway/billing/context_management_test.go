package billing

import (
	"testing"
	"time"
)

// TestStrictBudgetKeepAllContext 验证 Claude Code 无操作字段不改变费用上界、真实输出限制或原正文。
// 前置内存可信价目；正常/空数组放行、未知编辑和其他协议拒绝，无网络与持久化资源需要清理。
func TestStrictBudgetKeepAllContext(t *testing.T) {
	for _, protocol := range []string{"anthropic-messages", "openai-chat", "openai-responses"} {
		for _, kind := range []string{"keep_all", "empty", "clear", "invalid"} {
			t.Run(protocol+"/"+kind, func(t *testing.T) {
				dep := strictBudgetDeployment()
				body := strictBudgetBody(protocol)
				baseline, _ := strictBudgetMustPlan(t, dep, body, protocol)
				edits := []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}
				switch kind {
				case "empty":
					edits = []any{}
				case "clear":
					edits[0].(map[string]any)["keep"] = "none"
				case "invalid":
					edits = []any{"bad"}
				}
				body["context_management"] = map[string]any{"edits": edits}
				raw := strictBudgetEncode(t, body)
				got, final, err := StrictBudgetPlan(dep, raw, protocol, time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC))
				if protocol != "anthropic-messages" || kind == "clear" || kind == "invalid" {
					strictBudgetExpectError(t, err, "unsupported_budget")
					return
				}
				if err != nil || got.Amount != baseline.Amount || got.OutputCap != baseline.OutputCap || len(final) == 0 {
					t.Fatalf("无操作配置改变费用上界或被拒绝: baseline=%+v got=%+v err=%v", baseline, got, err)
				}
				if string(strictBudgetEncode(t, body)) != string(raw) {
					t.Fatal("额度计划修改原正文")
				}
			})
		}
	}
}
