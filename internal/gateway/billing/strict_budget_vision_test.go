package billing

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// visionBudgetBody构造三协议图文及Messages工具结果图片；参数为协议/工具开关，返回独立内存fixture，无外部读取。
func visionBudgetBody(protocol string, tool bool) map[string]any {
	part := map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AQ=="}}
	if protocol == "openai-responses" {
		part = map[string]any{"type": "input_image", "image_url": "https://example.invalid/test.png", "detail": "high"}
	}
	if protocol == "anthropic-messages" {
		part = map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AQ=="}}
	}
	content := []any{map[string]any{"type": "text", "text": "Describe the image."}, part}
	if protocol == "openai-responses" {
		content[0].(map[string]any)["type"] = "input_text"
	}
	if tool {
		content = []any{map[string]any{"type": "tool_result", "tool_use_id": "read_image", "content": content}}
	}
	body := strictBudgetBody(protocol)
	key := "messages"
	if protocol == "openai-responses" {
		key = "input"
	}
	body[key] = []any{map[string]any{"role": "user", "content": content}}
	return body
}

// TestStrictBudgetVisionPreservesHardBound验证三协议和原生Read结果完整整窗预占、冻结图像语义及同价token只收一次；纯内存结束自动清理。
func TestStrictBudgetVisionPreservesHardBound(t *testing.T) {
	for _, protocol := range []string{"openai-chat", "openai-responses", "anthropic-messages"} {
		for _, tool := range []bool{false, true} {
			if tool && protocol != "anthropic-messages" {
				continue
			}
			name := protocol
			if tool {
				name += "/read-tool"
			}
			t.Run(name, func(t *testing.T) {
				dep := strictBudgetDeployment()
				text, _ := strictBudgetMustPlan(t, dep, strictBudgetBody(protocol), protocol)
				body := visionBudgetBody(protocol, tool)
				before := string(strictBudgetEncode(t, body))
				plan, out := strictBudgetMustPlan(t, dep, body, protocol)
				if plan.InputBound != text.InputBound || plan.Amount != text.Amount || plan.OutputCap != text.OutputCap {
					t.Fatalf("图片不能减少整窗预占: text=%+v image=%+v", text, plan)
				}
				if string(strictBudgetEncode(t, body)) != before || out == nil {
					t.Fatal("图片准入修改了原请求")
				}
				var snapshot map[string]any
				json.Unmarshal([]byte(plan.PriceSnapshot), &snapshot)
				if snapshot["input_images_included"] != true {
					t.Fatal("未冻结图片总输入语义")
				}
				dep.LiteLLMParams["input_cost_per_token"] = 99.0
				for _, details := range []string{"input_tokens_details", "prompt_tokens_details"} {
					raw := map[string]any{"input_tokens": 80, "output_tokens": 10, details: map[string]any{"text_tokens": 20, "image_tokens": 60}}
					normalized := catalog.NormalizeUsage(llmdialogue.NormalizeDialogueUsage(raw))
					if normalized.InputImageTokens != 60 {
						t.Fatalf("规范化丢图片token: %+v", normalized)
					}
					charge, err := plan.Charge(normalized, raw)
					if err != nil || math.Abs(charge.Total-(80e-6+20e-6)) > 1e-12 {
						t.Fatalf("图片token应计入冻结输入一次: %+v %v", charge, err)
					}
				}
			})
		}
	}
}

// TestStrictBudgetVisionRejectsUnreservedPrices验证独立图片费率、条件图片token价、未知输入窗仍在出站前拒绝；无外部副作用。
func TestStrictBudgetVisionRejectsUnreservedPrices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"picture", func(p, m map[string]any) { p["input_cost_per_image"] = 0.1 }},
		{"free_picture_still_separate", func(p, m map[string]any) { p["input_cost_per_image"] = 0 }},
		{"unknown_window", func(p, m map[string]any) { delete(m, "max_input_tokens") }},
		{"image_token_variant", func(p, m map[string]any) {
			p["rates"] = []catalog.Rate{{Measure: "token", Side: "input", Variant: "image", UnitSize: 1, USD: 0.01, Window: "all"}, {Measure: "token", Side: "output", UnitSize: 1, USD: 0.02, Window: "all"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := strictBudgetDeployment()
			tc.change(d.LiteLLMParams, d.ModelInfo)
			_, _, err := StrictBudgetPlan(d, strictBudgetEncode(t, visionBudgetBody("anthropic-messages", false)), "anthropic-messages", time.Now())
			strictBudgetExpectError(t, err, "unsupported_budget")
		})
	}
}

// TestStrictBudgetVisionUsageFailures验证图片细分非法数、超过总量、别名矛盾及未知维度不能释放硬额度；每例独立用量，无持久化。
func TestStrictBudgetVisionUsageFailures(t *testing.T) {
	plan, _ := strictBudgetMustPlan(t, strictBudgetDeployment(), visionBudgetBody("anthropic-messages", false), "anthropic-messages")
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"split_alias_total_exceeds_with_cache", func(u map[string]any) {
			u["input_tokens"] = 50
			u["cache_read_input_tokens"] = 20
			u["input_tokens_details"] = map[string]any{"image_tokens": 40}
			u["prompt_tokens_details"] = map[string]any{"text_tokens": 20}
		}},
		{"fractional_aliases_same_truncated", func(u map[string]any) {
			u["input_tokens_details"] = map[string]any{"image_tokens": 1.2}
			u["prompt_tokens_details"] = map[string]any{"image_tokens": 1.3}
		}},
		{"malformed_details", func(u map[string]any) { u["input_tokens_details"] = "invalid" }},
		{"fraction", func(u map[string]any) { u["input_tokens_details"] = map[string]any{"image_tokens": 0.5} }},
		{"negative", func(u map[string]any) { u["input_tokens_details"] = map[string]any{"image_tokens": -1} }},
		{"string", func(u map[string]any) { u["input_tokens_details"] = map[string]any{"image_tokens": "1"} }},
		{"sum_over_total", func(u map[string]any) {
			u["input_tokens_details"] = map[string]any{"text_tokens": 50, "image_tokens": 51}
		}},
		{"aliases_conflict", func(u map[string]any) {
			u["input_tokens_details"] = map[string]any{"image_tokens": 30}
			u["prompt_tokens_details"] = map[string]any{"image_tokens": 20}
		}},
		{"image_count", func(u map[string]any) { u["input_image_count"] = 1 }},
		{"fraction_image_count", func(u map[string]any) { u["input_image_count"] = 0.5 }},
		{"output_image", func(u map[string]any) { u["output_tokens_details"] = map[string]any{"image_tokens": 1} }},
		{"unknown_image", func(u map[string]any) { u["input_image_tokens"] = 1 }},
		{"audio", func(u map[string]any) { u["input_tokens_details"] = map[string]any{"audio_tokens": 1} }},
		{"missing_total", func(u map[string]any) { delete(u, "input_tokens") }},
		{"over_window", func(u map[string]any) { u["input_tokens"] = 101 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"input_tokens": 100, "output_tokens": 10}
			tc.change(raw)
			_, err := plan.Charge(catalog.NormalizeUsage(llmdialogue.NormalizeDialogueUsage(raw)), raw)
			strictBudgetExpectError(t, err, "invalid_usage")
		})
	}
	// 旧/纯文本计划没有图片契约，不能因新代码允许图片而接受未预占的维度。
	text, _ := strictBudgetMustPlan(t, strictBudgetDeployment(), strictBudgetBody("anthropic-messages"), "anthropic-messages")
	_, err := text.Charge(catalog.Usage{PromptTokens: 100, CompletionTokens: 10, InputImageTokens: 50})
	strictBudgetExpectError(t, err, "invalid_usage")
}

// TestStrictBudgetVisionCache验证图片总量与缓存读写共用冻结费率、不重复收取模态token；缓存未知费率仍拒绝，无外部资源。
func TestStrictBudgetVisionCache(t *testing.T) {
	dep := strictBudgetDeployment()
	dep.LiteLLMParams["cache_read_input_token_cost"] = 0.5e-6
	dep.LiteLLMParams["cache_creation_input_token_cost"] = 1.25e-6
	plan, _ := strictBudgetMustPlan(t, dep, visionBudgetBody("anthropic-messages", true), "anthropic-messages")
	raw := map[string]any{"input_tokens": 50, "output_tokens": 10, "cache_read_input_tokens": 20, "cache_creation_input_tokens": 10, "input_tokens_details": map[string]any{"text_tokens": 20, "image_tokens": 30}}
	usage := catalog.NormalizeUsage(llmdialogue.NormalizeDialogueUsage(raw))
	charge, err := plan.Charge(usage, raw)
	if err != nil || usage.InputImageTokens != 30 || math.Abs(charge.Total-(50e-6+20e-6+10e-6+12.5e-6)) > 1e-12 {
		t.Fatalf("图像缓存事实或金额错误: usage=%+v charge=%+v err=%v", usage, charge, err)
	}
}

// TestStrictBudgetVisionTiers验证图片仍按完整输入窗最高档预占，按实测总量选冻结档，并拒绝不明确共享边界；纯内存，无外部清理。
func TestStrictBudgetVisionTiers(t *testing.T) {
	low, high := tierRule(0, 50, 1), tierRule(50, 200, 2)
	dep := tierDeployment(low, high)
	body := visionBudgetBody("openai-chat", false)
	body["max_tokens"] = 10
	plan, _ := strictBudgetMustPlan(t, dep, body, "openai-chat")
	text := tierPlan(t, dep)
	if plan.Amount != text.Amount {
		t.Fatalf("图像改变分档整窗预占: image=%v text=%v", plan.Amount, text.Amount)
	}
	high["details_v2"].(map[string]any)["ncache"] = tierUnit(99)
	for _, tc := range []struct {
		input   int
		want    float64
		blocked bool
	}{{40, 0.044, false}, {80, 0.168, false}, {50, 0, true}} {
		raw := map[string]any{"input_tokens": tc.input, "output_tokens": 2, "input_tokens_details": map[string]any{"image_tokens": tc.input}}
		charge, err := plan.Charge(catalog.NormalizeUsage(llmdialogue.NormalizeDialogueUsage(raw)), raw)
		if tc.blocked {
			strictBudgetExpectError(t, err, "invalid_usage")
			continue
		}
		if err != nil || math.Abs(charge.Total-tc.want) > 1e-12 {
			t.Fatalf("图片分档未冻结/重复收费 input=%d charge=%+v err=%v", tc.input, charge, err)
		}
	}
}
