package billing

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
)

// strictBudgetDeployment 为严格额度单元测试创建独立可信部署；无外部服务、无全局修改，测试结束由GC清理。
// 返回完整输入窗、两侧单token价和声明的旧Chat输出cap；调用方可独立修改每次返回值以构造边界。
func strictBudgetDeployment() config.ModelEntry {
	return config.ModelEntry{ModelName: "strict-budget-test", LiteLLMParams: map[string]any{"model": "private-strict-budget-test", "input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}, ModelInfo: map[string]any{"pricing_source": "manual", "max_input_tokens": 100, "max_output_tokens": 8192, "supported_parameters": []string{"max_tokens"}}}
}

// strictBudgetBody 生成三协议等价文字请求；参数为协议，返回独立对象，不写配置或数据，测试结束自动回收。
func strictBudgetBody(protocol string) map[string]any {
	body := map[string]any{"model": "private-strict-budget-test"}
	if protocol == "openai-responses" {
		body["input"] = "Hello"
	} else {
		body["messages"] = []any{map[string]any{"role": "user", "content": "Hello"}}
	}
	return body
}

// strictBudgetEncode 将测试请求编码为实际JSON；参数为测试对象和请求，返回字节，编码失败立即定位fixture，无持久化。
func strictBudgetEncode(t *testing.T, body map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// strictBudgetMustPlan 在固定计价时刻生成计划；参数为fixture部署和协议正文，返回计划及解析后的最终请求，失败即终止子测试。
func strictBudgetMustPlan(t *testing.T, dep config.ModelEntry, body map[string]any, protocol string) (BudgetPlan, map[string]any) {
	t.Helper()
	p, raw, err := StrictBudgetPlan(dep, strictBudgetEncode(t, body), protocol, time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("应成功生成费用上界: %v", err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return p, out
}

// strictBudgetExpectError 断言固定错误分类，防止无定价能力被当作参数错误或免费；参数为实际错误/目标码，无外部副作用。
func strictBudgetExpectError(t *testing.T, err error, code string) {
	t.Helper()
	var got *BudgetPlanError
	if !errors.As(err, &got) || got.Code != code {
		t.Fatalf("应拒绝为%s，实际%v", code, err)
	}
	if got.Error() == "" {
		t.Fatal("错误缺少诊断原因")
	}
}

// TestStrictBudgetProtocolCaps 验证三协议同步/流式最终JSON都有公开缺省cap，输入使用完整可信窗且不修改原请求；所有fixture纯内存。
func TestStrictBudgetProtocolCaps(t *testing.T) {
	for _, protocol := range []string{"openai-chat", "openai-responses", "anthropic-messages"} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/sync"
			if stream {
				name = protocol + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				body := strictBudgetBody(protocol)
				body["stream"] = stream
				original := string(strictBudgetEncode(t, body))
				dep := strictBudgetDeployment()
				p, out := strictBudgetMustPlan(t, dep, body, protocol)
				field := "max_tokens"
				if protocol == "openai-responses" {
					field = "max_output_tokens"
				}
				if out[field] != float64(DefaultBudgetOutputTokens) || !p.Defaulted || p.OutputCap != 4096 || p.InputBound != 100 {
					t.Fatalf("最终JSON缺省输出cap/整窗预占错误: %+v %v", p, out)
				}
				if p.Amount < 0.008292 || p.Amount-0.008292 > 1e-12 {
					t.Fatalf("未按全窗和输出cap预占: %g", p.Amount)
				}
				if string(strictBudgetEncode(t, body)) != original {
					t.Fatal("修改了调用方原始请求")
				}
				if !strings.Contains(p.PriceSnapshot, "input_rate") || !strings.Contains(p.String(), "input<=100") {
					t.Fatal("冻结快照或诊断遗漏")
				}
			})
		}
	}
}

// TestStrictBudgetCapsAndLimits 验证cap正常、限制边界和失败；使用纯内存部署，覆盖旁路JSON仍必须是整数且仅支持声明的cap能力。
func TestStrictBudgetCapsAndLimits(t *testing.T) {
	cases := []struct {
		name, protocol, code string
		change               func(config.ModelEntry, map[string]any)
		want                 int
		field                string
		defaulted            bool
	}{
		{name: "explicit boundary", want: 8192, field: "max_tokens", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = 8192 }},
		{name: "one token", want: 1, field: "max_tokens", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = 1 }},
		{name: "default lowered by deployment", want: 24, field: "max_tokens", defaulted: true, change: func(d config.ModelEntry, b map[string]any) { d.ModelInfo["max_output_tokens"] = 24 }},
		{name: "total output declaration", want: 4096, field: "max_completion_tokens", defaulted: true, change: func(d config.ModelEntry, b map[string]any) {
			d.ModelInfo["supported_parameters"] = []string{"max_completion_tokens"}
			b["reasoning_effort"] = "high"
		}},
		{name: "legacy cap becomes total cap", want: 32, field: "max_completion_tokens", change: func(d config.ModelEntry, b map[string]any) {
			d.ModelInfo["supported_parameters"] = []string{"max_completion_tokens", "max_tokens"}
			b["max_tokens"] = 32
			b["reasoning_effort"] = "high"
		}},
		{name: "messages thinking inside total", protocol: "anthropic-messages", want: 32, field: "max_tokens", change: func(d config.ModelEntry, b map[string]any) {
			b["max_tokens"] = 32
			b["thinking"] = map[string]any{"type": "enabled", "budget_tokens": 31}
		}},
		{name: "zero", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = 0 }},
		{name: "negative", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = -1 }},
		{name: "fraction", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = 1.5 }},
		{name: "string", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = "32" }},
		{name: "null", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = nil }},
		{name: "overflow", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = int64(math.MaxInt32) + 1 }},
		{name: "two caps", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) {
			d.ModelInfo["supported_parameters"] = []string{"max_tokens", "max_completion_tokens"}
			b["max_tokens"] = 3
			b["max_completion_tokens"] = 3
		}},
		{name: "wrong protocol cap", protocol: "openai-responses", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = 3 }},
		{name: "unknown chat cap capability", code: "unsupported_budget", change: func(d config.ModelEntry, b map[string]any) { delete(d.ModelInfo, "supported_parameters") }},
		{name: "invalid chat cap declaration", code: "unsupported_budget", change: func(d config.ModelEntry, b map[string]any) { d.ModelInfo["supported_parameters"] = "max_tokens" }},
		{name: "legacy reasoning ambiguity", code: "unsupported_budget", change: func(d config.ModelEntry, b map[string]any) { b["reasoning_effort"] = "high" }},
		{name: "exceeds deployment", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["max_tokens"] = 8193 }},
		{name: "bad output declaration", code: "unsupported_budget", change: func(d config.ModelEntry, b map[string]any) { d.ModelInfo["max_output_tokens"] = 0 }},
		{name: "thinking consumes entire cap", protocol: "anthropic-messages", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) {
			b["max_tokens"] = 32
			b["thinking"] = map[string]any{"budget_tokens": 32}
		}},
		{name: "thinking numeric string", protocol: "anthropic-messages", code: "invalid_request", change: func(d config.ModelEntry, b map[string]any) { b["thinking"] = map[string]any{"budget_tokens": "1"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dep := strictBudgetDeployment()
			protocol := tc.protocol
			if protocol == "" {
				protocol = "openai-chat"
			}
			body := strictBudgetBody(protocol)
			tc.change(dep, body)
			p, raw, err := StrictBudgetPlan(dep, strictBudgetEncode(t, body), protocol, time.Now())
			if tc.code != "" {
				strictBudgetExpectError(t, err, tc.code)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			if p.OutputCap != tc.want || p.Defaulted != tc.defaulted || out[tc.field] != float64(tc.want) {
				t.Fatalf("cap结果错误: %+v %s", p, raw)
			}
			count := 0
			for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
				if _, ok := out[key]; ok {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("必须仅保留一种cap: %s", raw)
			}
		})
	}
}

// TestStrictBudgetInputBounds 验证可信输入窗口来源、非法声明拒绝和输入长度无启发式折扣；所有独立配置无全局污染。
func TestStrictBudgetInputBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(config.ModelEntry)
		want   int
		fail   bool
	}{
		{name: "trusted max input", want: 100, change: func(d config.ModelEntry) {}},
		{name: "trusted context fallback", want: 200, change: func(d config.ModelEntry) {
			delete(d.ModelInfo, "max_input_tokens")
			d.ModelInfo["context_length"] = "200"
		}},
		{name: "params context fallback", want: 300, change: func(d config.ModelEntry) {
			delete(d.ModelInfo, "max_input_tokens")
			d.LiteLLMParams["context_length"] = 300
		}},
		{name: "missing", fail: true, change: func(d config.ModelEntry) { delete(d.ModelInfo, "max_input_tokens") }},
		{name: "zero", fail: true, change: func(d config.ModelEntry) { d.ModelInfo["max_input_tokens"] = 0 }},
		{name: "negative", fail: true, change: func(d config.ModelEntry) { d.ModelInfo["max_input_tokens"] = -1 }},
		{name: "fraction", fail: true, change: func(d config.ModelEntry) { d.ModelInfo["max_input_tokens"] = 3.2 }},
		{name: "infinity", fail: true, change: func(d config.ModelEntry) { d.ModelInfo["max_input_tokens"] = math.Inf(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := strictBudgetDeployment()
			tc.change(d)
			b := strictBudgetBody("openai-responses")
			b["input"] = strings.Repeat("复杂代码", 200)
			p, _, err := StrictBudgetPlan(d, strictBudgetEncode(t, b), "openai-responses", time.Now())
			if tc.fail {
				strictBudgetExpectError(t, err, "unsupported_budget")
				return
			}
			if err != nil || p.InputBound != tc.want {
				t.Fatalf("完整可信窗错误: %+v %v", p, err)
			}
		})
	}
}

// TestStrictBudgetRejectsUnpricedFeatures 验证无法证明费用的输入在调用前被拒绝，正常客户端schema不会被误判为图片；所有请求只在内存编码。
func TestStrictBudgetRejectsUnpricedFeatures(t *testing.T) {
	cases := []struct {
		name, protocol, code string
		change               func(map[string]any)
	}{
		{name: "multiple choices", code: "invalid_request", change: func(b map[string]any) { b["n"] = 2 }},
		{name: "string choices", code: "invalid_request", change: func(b map[string]any) { b["n"] = "1" }},
		{name: "priority tier", change: func(b map[string]any) { b["service_tier"] = "priority" }},
		{name: "extended cache", change: func(b map[string]any) { b["prompt_cache_retention"] = "24h" }},
		{name: "unknown extension", change: func(b map[string]any) { b["unknown_billable_extension"] = true }},
		{name: "predicted output", change: func(b map[string]any) { b["prediction"] = map[string]any{"type": "content", "content": "hello"} }},
		{name: "audio output", change: func(b map[string]any) { b["modalities"] = []string{"audio"} }},
		{name: "context compaction", change: func(b map[string]any) {
			b["context_management"] = map[string]any{"edits": []any{map[string]any{"type": "compact_20260112"}}}
		}},
		{name: "remote history", protocol: "openai-responses", change: func(b map[string]any) { b["previous_response_id"] = "resp_remote" }},
		{name: "hosted search", change: func(b map[string]any) { b["tools"] = []any{map[string]any{"type": "web_search"}} }},
		{name: "server tool search", protocol: "openai-responses", change: func(b map[string]any) {
			b["tools"] = []any{map[string]any{"type": "tool_search", "execution": "server"}}
		}},
		{name: "malformed tools", code: "invalid_request", change: func(b map[string]any) { b["tools"] = map[string]any{} }},
		{name: "malformed tool entry", code: "invalid_request", change: func(b map[string]any) { b["tools"] = []any{3} }},
		{name: "unnamed anthropic tool", code: "invalid_request", change: func(b map[string]any) { b["tools"] = []any{map[string]any{}} }},
		{name: "image content", change: func(b map[string]any) {
			b["messages"] = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.invalid/test.png"}}}}}
		}},
		{name: "top message audio", change: func(b map[string]any) {
			b["messages"] = []any{map[string]any{"role": "assistant", "content": "", "audio": map[string]any{"id": "audio_test"}}}
		}},
		{name: "PDF content", protocol: "anthropic-messages", change: func(b map[string]any) {
			b["messages"] = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "document"}}}}
		}},
		{name: "tool result image", protocol: "anthropic-messages", change: func(b map[string]any) {
			b["messages"] = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "content": []any{map[string]any{"type": "image"}}}}}}
		}},
		{name: "server caller", protocol: "anthropic-messages", change: func(b map[string]any) {
			b["messages"] = []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "caller": map[string]any{"type": "code_execution"}}}}}
		}},
		{name: "missing history", code: "invalid_request", change: func(b map[string]any) { delete(b, "messages") }},
		{name: "nonobject history", code: "invalid_request", change: func(b map[string]any) { b["messages"] = []any{42} }},
		{name: "unknown history item", protocol: "openai-responses", change: func(b map[string]any) { b["input"] = []any{map[string]any{"type": "image_generation_call"}} }},
		{name: "bad content shape", change: func(b map[string]any) {
			b["messages"] = []any{map[string]any{"role": "user", "content": map[string]any{}}}
		}},
		{name: "bad content item", code: "invalid_request", change: func(b map[string]any) { b["messages"] = []any{map[string]any{"role": "user", "content": []any{7}}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			protocol := tc.protocol
			if protocol == "" {
				protocol = "openai-chat"
			}
			b := strictBudgetBody(protocol)
			tc.change(b)
			_, _, err := StrictBudgetPlan(strictBudgetDeployment(), strictBudgetEncode(t, b), protocol, time.Now())
			code := tc.code
			if code == "" {
				code = "unsupported_budget"
			}
			strictBudgetExpectError(t, err, code)
		})
	}
}

// TestStrictBudgetClientToolsAndNativeHistory 验证原生文字推理、namespace/custom及多轮客户端工具内容均由整窗覆盖；无需上游或持久化清理。
func TestStrictBudgetClientToolsAndNativeHistory(t *testing.T) {
	t.Run("Responses history", func(t *testing.T) {
		b := strictBudgetBody("openai-responses")
		b["input"] = []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Hello"}}},
			map[string]any{"type": "reasoning", "encrypted_content": "opaque"}, map[string]any{"type": "function_call", "call_id": "call_a", "name": "lookup", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call_a", "output": "result"}, map[string]any{"type": "custom_tool_call", "call_id": "call_b", "name": "custom", "input": "hello"}, map[string]any{"type": "custom_tool_call_output", "call_id": "call_b", "output": "result"},
			map[string]any{"type": "tool_search_call", "execution": "client"}, map[string]any{"type": "tool_search_output", "tools": []any{map[string]any{"type": "function", "name": "lookup"}}},
		}
		b["tools"] = []any{map[string]any{"type": "namespace", "name": "tools", "tools": []any{map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{"type": map[string]any{"const": "image"}}}}, map[string]any{"type": "custom", "name": "custom"}}}, map[string]any{"type": "tool_search", "execution": "client"}}
		p, _ := strictBudgetMustPlan(t, strictBudgetDeployment(), b, "openai-responses")
		if p.InputBound != 100 {
			t.Fatal("工具schema和历史必须仍预占整窗")
		}
	})
	t.Run("Claude signed history", func(t *testing.T) {
		b := strictBudgetBody("anthropic-messages")
		b["system"] = "You are helpful."
		b["tools"] = []any{map[string]any{"name": "lookup", "input_schema": map[string]any{"type": "object"}}}
		b["messages"] = []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "Reason", "signature": "opaque"}, map[string]any{"type": "redacted_thinking", "data": "opaque"}, map[string]any{"type": "tool_use", "id": "tool_a", "name": "lookup", "input": map[string]any{}, "caller": map[string]any{"type": "direct"}}}}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tool_a", "content": []any{map[string]any{"type": "text", "text": "result"}}}}}}
		_, out := strictBudgetMustPlan(t, strictBudgetDeployment(), b, "anthropic-messages")
		if !strings.Contains(string(strictBudgetEncode(t, out)), "opaque") {
			t.Fatal("签名历史被修改")
		}
	})
}

// TestStrictBudgetMalformedJSON 验证旁路也不能提交非对象、尾随JSON或未知协议；均为本地纯函数调用，无残留。
func TestStrictBudgetMalformedJSON(t *testing.T) {
	for _, raw := range []string{"{", "null", "[]", "{} {}"} {
		t.Run(raw, func(t *testing.T) {
			_, _, err := StrictBudgetPlan(strictBudgetDeployment(), []byte(raw), "openai-chat", time.Now())
			strictBudgetExpectError(t, err, "invalid_request")
		})
	}
	t.Run("unsupported protocol", func(t *testing.T) {
		_, _, err := StrictBudgetPlan(strictBudgetDeployment(), []byte(`{}`), "google-generative", time.Now())
		strictBudgetExpectError(t, err, "unsupported_budget")
	})
}

// TestStrictBudgetCacheRates 验证自动缓存保守预留、显式5m/1h费率、混合TTL及缺价拒绝，固定请求不访问真实缓存。
func TestStrictBudgetCacheRates(t *testing.T) {
	for _, tc := range []struct {
		name, ttl, code string
		rates           bool
		oneHour         bool
		mixed           bool
		wantWrite       float64
	}{
		{name: "automatic cache write reserve", rates: true, wantWrite: 0.00000125},
		{name: "five minute", ttl: "5m", rates: true, wantWrite: 0.00000125},
		{name: "one hour", ttl: "1h", rates: true, oneHour: true, wantWrite: 0.000002},
		{name: "missing five minute price", ttl: "5m", code: "unsupported_budget"},
		{name: "missing exact hour price", ttl: "1h", rates: true, code: "unsupported_budget"},
		{name: "unsupported TTL", ttl: "1d", rates: true, code: "invalid_request"},
		{name: "mixed TTL", ttl: "1h", rates: true, oneHour: true, mixed: true, code: "unsupported_budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := strictBudgetDeployment()
			if tc.rates {
				d.LiteLLMParams["cache_creation_input_token_cost"] = 0.00000125
				d.LiteLLMParams["cache_read_input_token_cost"] = 0.0000001
			}
			if tc.oneHour {
				d.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 0.000002
			}
			b := strictBudgetBody("anthropic-messages")
			b["max_tokens"] = 8
			if tc.ttl != "" {
				b["cache_control"] = map[string]any{"type": "ephemeral", "ttl": tc.ttl}
			}
			if tc.mixed {
				b["system"] = []any{map[string]any{"type": "text", "text": "system", "cache_control": map[string]any{"type": "ephemeral", "ttl": "5m"}}}
			}
			p, _, err := StrictBudgetPlan(d, strictBudgetEncode(t, b), "anthropic-messages", time.Now())
			if tc.code != "" {
				strictBudgetExpectError(t, err, tc.code)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := 100*0.000001 + 8*0.000002 + 100*tc.wantWrite
			if p.Amount < want || p.Amount-want > 1e-12 {
				t.Fatalf("缓存未按最坏写入量预占: %g want>=%g", p.Amount, want)
			}
			charge, err := p.Charge(catalog.Usage{PromptTokens: 100, CompletionTokens: 8, CachedTokens: 20, CacheWriteTokens: 30})
			if err != nil || math.Abs(charge.Total-(80*0.000001+8*0.000002+20*0.0000001+30*tc.wantWrite)) > 1e-12 {
				t.Fatalf("缓存费用分项错误 %+v %v", charge, err)
			}
		})
	}
	for _, hint := range []any{"ephemeral", map[string]any{"type": "persistent"}, map[string]any{"type": "ephemeral", "scope": "global"}} {
		t.Run("bad hint", func(t *testing.T) {
			b := strictBudgetBody("anthropic-messages")
			b["cache_control"] = hint
			_, _, err := StrictBudgetPlan(strictBudgetDeployment(), strictBudgetEncode(t, b), "anthropic-messages", time.Now())
			if err == nil {
				t.Fatal("非法缓存控制未拒绝")
			}
		})
	}
}

// TestStrictBudgetPriceValidation 验证两侧费率完整、零价明确有效、负值/歧义/条件价拒绝，纯内存配置无清理动作。
func TestStrictBudgetPriceValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(config.ModelEntry)
		free   bool
		valid  bool
	}{
		{name: "explicit free", valid: true, free: true, change: func(d config.ModelEntry) {
			d.LiteLLMParams["input_cost_per_token"] = 0
			d.LiteLLMParams["output_cost_per_token"] = 0
		}},
		{name: "missing input", change: func(d config.ModelEntry) { delete(d.LiteLLMParams, "input_cost_per_token") }},
		{name: "missing output", change: func(d config.ModelEntry) { delete(d.LiteLLMParams, "output_cost_per_token") }},
		{name: "negative", change: func(d config.ModelEntry) { d.LiteLLMParams["input_cost_per_token"] = -1 }},
		{name: "NaN", change: func(d config.ModelEntry) { d.LiteLLMParams["input_cost_per_token"] = math.NaN() }},
		{name: "infinite", change: func(d config.ModelEntry) { d.LiteLLMParams["output_cost_per_token"] = math.Inf(1) }},
		{name: "amount overflow", change: func(d config.ModelEntry) { d.LiteLLMParams["output_cost_per_token"] = 1e9 }},
		{name: "invalid cache price", change: func(d config.ModelEntry) { d.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = "invalid" }},
		{name: "invalid optional cache price", change: func(d config.ModelEntry) { d.LiteLLMParams["cache_read_input_token_cost"] = "unknown" }},
		{name: "unmodeled long-context tier", change: func(d config.ModelEntry) { d.LiteLLMParams["input_cost_per_token_above_200k"] = 0.01 }},
		{name: "empty rate table", change: func(d config.ModelEntry) { d.LiteLLMParams["rates"] = []catalog.Rate{} }},
		{name: "bad rate shape", change: func(d config.ModelEntry) { d.LiteLLMParams["rates"] = "rates" }},
		{name: "unqualified output absent", change: func(d config.ModelEntry) {
			d.LiteLLMParams["rates"] = []catalog.Rate{{Measure: "token", Side: "input", Window: "all", UnitSize: 1, USD: 1e-6}, {Measure: "token", Side: "output", Variant: "thinking", Window: "all", UnitSize: 1, USD: 2e-6}}
		}},
		{name: "generic price cannot hide thinking premium", change: func(d config.ModelEntry) {
			d.LiteLLMParams["rates"] = []catalog.Rate{{Measure: "token", Side: "input", Window: "all", UnitSize: 1, USD: 1e-6}, {Measure: "token", Side: "output", Window: "all", UnitSize: 1, USD: 2e-6}, {Measure: "token", Side: "output", Variant: "thinking", Window: "all", UnitSize: 1, USD: 4e-6}}
		}},
		{name: "unknown per-request fee", change: func(d config.ModelEntry) {
			d.LiteLLMParams["rates"] = []catalog.Rate{{Measure: "token", Side: "input", Window: "all", UnitSize: 1, USD: 1e-6}, {Measure: "token", Side: "output", Window: "all", UnitSize: 1, USD: 2e-6}, {Measure: "request", Side: "output", Window: "all", UnitSize: 1, USD: 1}}
		}},
		{name: "unknown rate window", change: func(d config.ModelEntry) {
			d.LiteLLMParams["rates"] = []catalog.Rate{{Measure: "token", Side: "input", Window: "holiday", UnitSize: 1, USD: 1e-6}}
		}},
		{name: "zero unit size", change: func(d config.ModelEntry) {
			d.LiteLLMParams["rates"] = []catalog.Rate{{Measure: "token", Side: "input", Window: "all", USD: 1e-6}}
		}},
		{name: "fallback price", change: func(d config.ModelEntry) {
			d.LiteLLMParams["rates"] = []catalog.Rate{{Measure: "token", Side: "input", Window: "all", UnitSize: 1, USD: 1e-6, Fallback: true}}
		}},
		{name: "duplicate rate", change: func(d config.ModelEntry) {
			r := catalog.Rate{Measure: "token", Side: "input", Window: "all", UnitSize: 1, USD: 1e-6}
			d.LiteLLMParams["rates"] = []catalog.Rate{r, r}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := strictBudgetDeployment()
			tc.change(d)
			p, _, err := StrictBudgetPlan(d, strictBudgetEncode(t, strictBudgetBody("openai-chat")), "openai-chat", time.Now())
			if !tc.valid {
				strictBudgetExpectError(t, err, "unsupported_budget")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.free {
				c, err := p.Charge(catalog.Usage{PromptTokens: 100, CompletionTokens: 4096})
				if err != nil || p.Amount != 0 || c.Total != 0 {
					t.Fatalf("明确零价应保留: %+v %+v %v", p, c, err)
				}
			}
		})
	}
}

// TestStrictBudgetCatalogAndPriceSnapshot 验证目录查找、区间覆盖和不可变计价；每个子测试用唯一目录ID并通过Cleanup删除，不覆盖既有模型。
func TestStrictBudgetCatalogAndPriceSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rules     any
		wantError bool
	}{
		{name: "no conditional rules"},
		{name: "single covering range", rules: []any{map[string]any{"input_range": []int{0, 100}, "output_range": []int{0, 8192}, "details_v2": map[string]any{"ncache": map[string]any{"unit_name": "token", "unit_size": 1, "unit_price_usd": 1e-6}, "output": map[string]any{"unit_name": "token", "unit_size": 1, "unit_price_usd": 2e-6}}}}},
		{name: "tiered", rules: []any{map[string]any{}, map[string]any{}}, wantError: true},
		{name: "short input range", rules: []any{map[string]any{"input_range": []int{0, 99}}}, wantError: true},
		{name: "short output range", rules: []any{map[string]any{"output_range": []int{0, 2}}}, wantError: true},
		{name: "nonzero lower bound", rules: []any{map[string]any{"input_range": []int{1, 1000}}}, wantError: true},
		{name: "malformed range", rules: []any{map[string]any{"input_range": []int{100}}}, wantError: true},
		{name: "condition", rules: []any{map[string]any{"condition": "premium"}}, wantError: true},
		{name: "item type", rules: []any{map[string]any{"input_item_type": "image"}}, wantError: true},
		{name: "bad rules", rules: map[string]any{}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "strict-budget-unit-" + strings.ReplaceAll(tc.name, " ", "-")
			row := map[string]any{"input_cost_per_token": 1e-6, "output_cost_per_token": 2e-6, "max_input_tokens": 100, "max_output_tokens": 8192, "supported_parameters": []string{"max_completion_tokens"}}
			if tc.rules != nil {
				row["pricing_rules_v2"] = tc.rules
			}
			catalog.SetModel(id, row)
			t.Cleanup(func() { catalog.RemoveModel(id) })
			dep := config.ModelEntry{ModelName: "not-in-catalog", LiteLLMParams: map[string]any{"model": "provider/not-in-catalog"}, ModelInfo: map[string]any{"base_model": id}}
			p, _, err := StrictBudgetPlan(dep, strictBudgetEncode(t, strictBudgetBody("openai-chat")), "openai-chat", time.Now())
			if tc.wantError {
				strictBudgetExpectError(t, err, "unsupported_budget")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// 预占完成后目录变化不能改变同一请求的账单，也不能借改公开字段越过原始cap。
			catalog.SetModel(id, map[string]any{"input_cost_per_token": 100, "output_cost_per_token": 100})
			p.Amount = 100
			p.OutputCap = 100000
			p.PriceSnapshot = "modified"
			c, err := p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 2})
			if err != nil || math.Abs(c.Total-14e-6) > 1e-12 {
				t.Fatalf("目录变化穿透冻结快照: %+v %v", c, err)
			}
			_, err = p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 4097})
			strictBudgetExpectError(t, err, "invalid_usage")
		})
	}
	t.Run("manual missing side never falls back", func(t *testing.T) {
		id := "strict-budget-manual-precedence"
		catalog.SetModel(id, map[string]any{"input_cost_per_token": 1e-6, "output_cost_per_token": 2e-6})
		t.Cleanup(func() { catalog.RemoveModel(id) })
		d := strictBudgetDeployment()
		d.ModelInfo["base_model"] = id
		delete(d.LiteLLMParams, "output_cost_per_token")
		_, _, err := StrictBudgetPlan(d, strictBudgetEncode(t, strictBudgetBody("openai-chat")), "openai-chat", time.Now())
		strictBudgetExpectError(t, err, "unsupported_budget")
	})
}

// TestStrictBudgetFrozenWindowsAndBaseUnits 验证USD已按基础token归一，不能再除UnitSize，并冻结请求开始时段及缓存最大费率；纯内存无残留。
func TestStrictBudgetFrozenWindowsAndBaseUnits(t *testing.T) {
	d := strictBudgetDeployment()
	rates := []catalog.Rate{
		{Measure: "token", Side: "input", Window: "peak", UnitSize: 1000, USD: 2e-6}, {Measure: "token", Side: "output", Window: "peak", UnitSize: 1000, USD: 4e-6},
		{Measure: "token", Side: "input", Window: "offpeak", UnitSize: 1000, USD: 1e-6}, {Measure: "token", Side: "output", Window: "offpeak", UnitSize: 1000, USD: 2e-6},
		{Measure: "token", Side: "input", Variant: "cached", Window: "all", UnitSize: 1000, USD: 5e-6},
	}
	d.LiteLLMParams["rates"] = rates
	b := strictBudgetBody("openai-chat")
	b["max_tokens"] = 10
	for _, hour := range []int{2, 15} {
		t.Run(time.Date(2026, 10, 20, hour, 0, 0, 0, time.UTC).String(), func(t *testing.T) {
			start := time.Date(2026, 10, 20, hour, 0, 0, 0, time.UTC)
			p, _, err := StrictBudgetPlan(d, strictBudgetEncode(t, b), "openai-chat", start)
			if err != nil {
				t.Fatal(err)
			}
			out := 2e-6
			if catalog.WindowAt(start) == "peak" {
				out = 4e-6
			}
			want := 100*5e-6 + 10*out
			if p.Amount < want || p.Amount-want > 1e-12 {
				t.Fatalf("基础单位/缓存最坏价错误: %g want %g", p.Amount, want)
			}
			c, err := p.Charge(catalog.Usage{PromptTokens: 100, CompletionTokens: 10, CachedTokens: 100})
			if err != nil || math.Abs(c.Total-want) > 1e-12 || c.Window != catalog.WindowAt(start) {
				t.Fatalf("计价窗口未冻结: %+v %v", c, err)
			}
		})
	}
}

// TestStrictBudgetUsageBounds 验证实测正常、边界、缺失或越界不能退款，且只使用冻结部署值；所有账单均为内存计算。
func TestStrictBudgetUsageBounds(t *testing.T) {
	d := strictBudgetDeployment()
	b := strictBudgetBody("openai-chat")
	b["max_tokens"] = 10
	p, _ := strictBudgetMustPlan(t, d, b, "openai-chat")
	for _, tc := range []struct {
		name  string
		usage catalog.Usage
		valid bool
	}{
		{name: "explicit zero", valid: true}, {name: "full reserved bound", valid: true, usage: catalog.Usage{PromptTokens: 100, CompletionTokens: 10}},
		{name: "unpriced cache read", usage: catalog.Usage{PromptTokens: 100, CompletionTokens: 10, CachedTokens: 100}},
		{name: "matching pricing model", valid: true, usage: catalog.Usage{PromptTokens: 2, CompletionTokens: 2, PricingModel: "private-strict-budget-test"}},
		{name: "missing usage", usage: catalog.Usage{PricingBlocked: "upstream_usage_missing"}},
		{name: "negative input", usage: catalog.Usage{PromptTokens: -1}}, {name: "input overflow", usage: catalog.Usage{PromptTokens: 101}},
		{name: "output overflow", usage: catalog.Usage{CompletionTokens: 11}}, {name: "negative output", usage: catalog.Usage{CompletionTokens: -1}},
		{name: "cached exceeds total", usage: catalog.Usage{PromptTokens: 10, CachedTokens: 11}}, {name: "negative cache", usage: catalog.Usage{CachedTokens: -1}},
		{name: "unpriced cache write", usage: catalog.Usage{PromptTokens: 10, CacheWriteTokens: 1}}, {name: "cache write overflow", usage: catalog.Usage{CacheWriteTokens: 101}},
		{name: "text details contradict total", usage: catalog.Usage{PromptTokens: 10, InputTextTokens: 11}},
		{name: "unexpected image tokens", usage: catalog.Usage{InputImageTokens: 1}}, {name: "unexpected image count", usage: catalog.Usage{InputImages: 1}},
		{name: "unexpected image output", usage: catalog.Usage{Images: 1}}, {name: "unexpected input duration", usage: catalog.Usage{InputSeconds: 1}},
		{name: "unexpected output duration", usage: catalog.Usage{Seconds: 1}}, {name: "unexpected search", usage: catalog.Usage{Searches: 1}},
		{name: "unexpected variant", usage: catalog.Usage{OutputVariant: "thinking"}}, {name: "unexpected image variant", usage: catalog.Usage{ImageVariant: "standard"}},
		{name: "unexpected pricing model", usage: catalog.Usage{PricingModel: "different"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := p.Charge(tc.usage)
			if !tc.valid {
				strictBudgetExpectError(t, err, "invalid_usage")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := float64(tc.usage.PromptTokens)*1e-6 + float64(tc.usage.CompletionTokens)*2e-6
			if math.Abs(c.Total-want) > 1e-12 {
				t.Fatalf("实测账单错误: %+v", c)
			}
		})
	}
	t.Run("uninitialized plan", func(t *testing.T) {
		_, err := (BudgetPlan{}).Charge(catalog.Usage{})
		strictBudgetExpectError(t, err, "invalid_usage")
	})
	d.LiteLLMParams["input_cost_per_token"] = 100
	c, err := p.Charge(catalog.Usage{PromptTokens: 10, CompletionTokens: 10})
	if err != nil || math.Abs(c.Total-30e-6) > 1e-12 {
		t.Fatalf("部署费率变化穿透冻结快照: %+v %v", c, err)
	}
}

// TestStrictBudgetRawUsageValidation 验证原始实测不能经归一化截断为低量、推理不超过输出且可选维度也严格；无数据库或全局状态。
func TestStrictBudgetRawUsageValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   map[string]any
		valid bool
	}{
		{name: "normal", valid: true, raw: map[string]any{"prompt_tokens": 10, "completion_tokens": 2}},
		{name: "explicit zero", valid: true, raw: map[string]any{"input_tokens": 0, "output_tokens": 0}},
		{name: "JSON numbers", valid: true, raw: map[string]any{"input_tokens": json.Number("10"), "output_tokens": json.Number("2")}},
		{name: "nil", raw: nil}, {name: "missing input", raw: map[string]any{"completion_tokens": 1}}, {name: "missing output", raw: map[string]any{"prompt_tokens": 1}},
		{name: "numeric string", raw: map[string]any{"prompt_tokens": "10", "completion_tokens": 1}},
		{name: "fractional float", raw: map[string]any{"prompt_tokens": 10.5, "completion_tokens": 1}},
		{name: "fractional JSON number", raw: map[string]any{"prompt_tokens": json.Number("10.5"), "completion_tokens": 1}},
		{name: "negative", raw: map[string]any{"prompt_tokens": -1, "completion_tokens": 1}},
		{name: "NaN", raw: map[string]any{"prompt_tokens": math.NaN(), "completion_tokens": 1}},
		{name: "overflow", raw: map[string]any{"prompt_tokens": float64(math.MaxInt32) + 1, "completion_tokens": 1}},
		{name: "conflicting aliases", raw: map[string]any{"input_tokens": 10, "prompt_tokens": 11, "completion_tokens": 1}},
		{name: "blocked", raw: map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "pricing_blocked": "incomplete"}},
		{name: "fractional cache", raw: map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "prompt_tokens_details": map[string]any{"cached_tokens": 1.5}}},
		{name: "malformed details", raw: map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "prompt_tokens_details": "none"}},
		{name: "reasoning exceeds completion", raw: map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "completion_tokens_details": map[string]any{"reasoning_tokens": 2}}},
		{name: "unreserved audio", raw: map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "prompt_tokens_details": map[string]any{"audio_tokens": 1}}},
		{name: "unreserved server search", raw: map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "server_tool_use": map[string]any{"web_search_requests": 1}}},
		{name: "cache string", raw: map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "cache_creation_input_tokens": "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStrictBudgetUsage(tc.raw)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				strictBudgetExpectError(t, err, "invalid_usage")
			}
		})
	}
}

// TestStrictBudgetRawCacheTTL 验证实测缓存期限与冻结价格一致且分桶总数可对账；构造5m/1h计划，无真实缓存或清理副作用。
func TestStrictBudgetRawCacheTTL(t *testing.T) {
	plans := map[string]BudgetPlan{}
	for _, ttl := range []string{"5m", "1h"} {
		d := strictBudgetDeployment()
		d.LiteLLMParams["cache_creation_input_token_cost"] = 1.25e-6
		d.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 2e-6
		b := strictBudgetBody("anthropic-messages")
		b["cache_control"] = map[string]any{"type": "ephemeral", "ttl": ttl}
		p, _ := strictBudgetMustPlan(t, d, b, "anthropic-messages")
		plans[ttl] = p
	}
	for _, tc := range []struct {
		name, plan string
		details    any
		total      int
		valid      bool
	}{
		{name: "matching five minute", plan: "5m", details: map[string]any{"ephemeral_5m_input_tokens": 3}, total: 3, valid: true},
		{name: "matching hour", plan: "1h", details: map[string]any{"ephemeral_1h_input_tokens": 3}, total: 3, valid: true},
		{name: "missing plan", details: map[string]any{"ephemeral_1h_input_tokens": 3}, total: 3},
		{name: "unexpected hour", plan: "5m", details: map[string]any{"ephemeral_1h_input_tokens": 3}, total: 3},
		{name: "unexpected five minute", plan: "1h", details: map[string]any{"ephemeral_5m_input_tokens": 3}, total: 3},
		{name: "mixed TTL", plan: "1h", details: map[string]any{"ephemeral_5m_input_tokens": 1, "ephemeral_1h_input_tokens": 2}, total: 3},
		{name: "contradicting totals", plan: "1h", details: map[string]any{"ephemeral_1h_input_tokens": 3}, total: 2},
		{name: "unknown TTL", plan: "1h", details: map[string]any{"ephemeral_24h_input_tokens": 3}, total: 3},
		{name: "bad shape", plan: "5m", details: "none", total: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"input_tokens": 10, "output_tokens": 2, "cache_creation_input_tokens": tc.total, "cache_creation": tc.details}
			p := plans[tc.plan]
			err := ValidateStrictBudgetUsage(raw, p)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				strictBudgetExpectError(t, err, "invalid_usage")
			}
		})
	}
}

// TestStrictBudgetRoundUp 验证预占金额向上而非向下舍入，拒绝溢出/非有限金额；纯数学测试，无外部状态。
func TestStrictBudgetRoundUp(t *testing.T) {
	for _, value := range []float64{0, 1e-15, 1.2345678901234, 0.1, 1e5} {
		t.Run("valid", func(t *testing.T) {
			out, err := budgetRoundUp(value)
			if err != nil || out < value || out-value > 1e-10 {
				t.Fatalf("预占未安全向上舍入: %g -> %g %v", value, out, err)
			}
		})
	}
	for _, value := range []float64{-1, math.NaN(), math.Inf(1), 1e20} {
		t.Run("invalid", func(t *testing.T) {
			_, err := budgetRoundUp(value)
			strictBudgetExpectError(t, err, "unsupported_budget")
		})
	}
}
