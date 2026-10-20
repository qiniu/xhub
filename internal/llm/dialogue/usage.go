package dialogue

import (
	"encoding/json"
	"math"
)

// dialogueNumber 读取有限数值；参数为动态数值，返回数和有效标记；请求校验调用，无副作用。
func dialogueNumber(v any) (float64, bool) {
	var n float64
	switch value := v.(type) {
	case float64:
		n = value
	case int:
		n = float64(value)
	case json.Number:
		var err error
		n, err = value.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}

// NormalizeDialogueUsage 统一实测用量名称并保留缓存事实；参数为协议用量，返回新对象。
// 调用：同步和流适配器；先记录原始别名冲突/非法计数，再转换名称，避免覆盖导致错误结算；缺失实测量不估算。
func NormalizeDialogueUsage(u map[string]any) map[string]any {
	out := MergeDialogueUsage(nil, u)
	for source, target := range map[string]string{"input_tokens": "prompt_tokens", "output_tokens": "completion_tokens", "input_tokens_details": "prompt_tokens_details", "output_tokens_details": "completion_tokens_details"} {
		if value, exists := u[source]; exists {
			if value == nil && (source == "input_tokens_details" || source == "output_tokens_details") {
				// null是未报告，不得覆盖另一别名已经观测到的图片/缓存计量。
				delete(out, source)
				continue
			}
			if (source == "input_tokens_details" || source == "output_tokens_details") && dialogueMap(value) != nil {
				// 两种别名可能各带一部分明细；先前Merge已标记冲突，合并不能丢失图片或缓存事实。
				fields := map[string]any{}
				for key, v := range dialogueMap(out[target]) {
					fields[key] = v
				}
				for key, v := range dialogueMap(value) {
					if v != nil {
						fields[key] = v
					}
				}
				out[target] = fields
			} else {
				out[target] = value
			}
		}
		delete(out, source)
	}
	if value, exists := u["cache_read_input_tokens"]; exists {
		fields := map[string]any{}
		for key, v := range dialogueMap(out["prompt_tokens_details"]) {
			fields[key] = v
		}
		fields["cached_tokens"] = value
		out["prompt_tokens_details"] = fields
	}
	input, hasInput := dialogueNumber(out["prompt_tokens"])
	output, hasOutput := dialogueNumber(out["completion_tokens"])
	if !hasInput || !hasOutput {
		if reason, _ := out["pricing_blocked"].(string); reason == "" {
			out["pricing_blocked"] = "upstream_usage_missing"
		}
	} else {
		out["total_tokens"] = input + output
	}
	return out
}

// DialogueOutputUsage 将共享实测事实映射到用户用量协议；参数为事实与协议，返回新对象。
// 调用：同步输出；未知用量通过 pricing_blocked 明示，不生成估算数值。
func DialogueOutputUsage(u map[string]any, protocol string) map[string]any {
	out := map[string]any{}
	for key, value := range u {
		out[key] = value
	}
	for source, target := range map[string]string{"prompt_tokens": "input_tokens", "completion_tokens": "output_tokens", "prompt_tokens_details": "input_tokens_details", "completion_tokens_details": "output_tokens_details"} {
		if value, exists := u[source]; exists {
			out[target] = value
		}
		delete(out, source)
	}
	if protocol == "openai-responses" {
		// LiteLLM 的 Responses usage 转换同样补齐 SDK 必需明细；这是格式默认值，不改变已报告的输入/输出总量或计费。
		for detail, field := range map[string]string{"input_tokens_details": "cached_tokens", "output_tokens_details": "reasoning_tokens"} {
			fields := map[string]any{}
			for key, value := range dialogueMap(out[detail]) {
				fields[key] = value
			}
			if fields[field] == nil {
				fields[field] = 0
			}
			out[detail] = fields
		}
	}
	if protocol == "anthropic-messages" {
		if details := dialogueMap(u["prompt_tokens_details"]); details["cached_tokens"] != nil {
			out["cache_read_input_tokens"] = details["cached_tokens"]
			// OpenAI 的缓存命中是输入总量的子集，Messages 的 cache_read_input_tokens 却独立于 input_tokens。
			// 只有原用量没有独立缓存字段时才扣除子集；否则同步编码后的再次计费会把同一缓存输入算两次。
			if u["cache_read_input_tokens"] == nil && u["cache_read_tokens"] == nil {
				input, inputOK := dialogueNumber(u["prompt_tokens"])
				cached, cacheOK := dialogueNumber(details["cached_tokens"])
				if inputOK && cacheOK && cached >= 0 && cached <= input {
					out["input_tokens"] = input - cached
				}
			}
		}
		delete(out, "input_tokens_details")
		delete(out, "output_tokens_details")
		delete(out, "total_tokens")
	}
	return out
}
