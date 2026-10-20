package dialogue

import (
	"math"
	"strings"
)

// dialogueUsageAliases 列出同一个实测计数的平级或嵌套别名；只用于检测同帧矛盾和替换旧帧快照。
// 缓存读取是否包含于输入总量仍由原始字段表达，此表不转换该语义，也不把 TTL 分桶当作总量别名。
var dialogueUsageAliases = [][]string{
	{"prompt_tokens", "input_tokens"},
	{"completion_tokens", "output_tokens"},
	{"prompt_tokens_details.text_tokens", "input_tokens_details.text_tokens"},
	{"prompt_tokens_details.image_tokens", "input_tokens_details.image_tokens"},
	{"cache_creation_input_tokens", "cache_write_tokens"},
	{"cache_read_input_tokens", "cache_read_tokens", "cached_tokens", "prompt_tokens_details.cached_tokens", "input_tokens_details.cached_tokens"},
}

// MergeDialogueUsage 合并一帧原始实测用量，并保留任何已观察到的矛盾或非法计数。
// 参数 previous 为本次请求累积对象（可为空），incoming 为当前帧；返回累积对象，更新计数但不转换别名。
// 流适配器在归一化前调用；后续完整帧或空 pricing_blocked 不得把已不可信的费用重新标为可结算。
// 不拒绝缺少字段的合法部分帧，不修改 incoming，不执行计费或外部操作。
func MergeDialogueUsage(previous, incoming map[string]any) map[string]any {
	if previous == nil {
		previous = map[string]any{}
	}
	blocked, _ := previous["pricing_blocked"].(string)
	if blocked == "" {
		blocked = DialogueUsageProblem(incoming)
	}
	// 别名冲突只比较同一帧；合法累计更新可以只携带其中一个名字，
	// 必须删除另一名字的旧快照，避免最终归一化把跨帧增长误判为矛盾。
	for _, group := range dialogueUsageAliases {
		updated := false
		for _, path := range group {
			if dialogueUsageAlias(incoming, path) != nil {
				updated = true
				break
			}
		}
		if updated {
			for _, path := range group {
				if dialogueUsageAlias(incoming, path) == nil {
					deleteDialogueUsageAlias(previous, path)
				}
			}
		}
	}
	for key, value := range incoming {
		if strings.HasSuffix(key, "_tokens_details") {
			if value == nil {
				continue
			}
			if fields, ok := value.(map[string]any); ok {
				// 流帧常只更新缓存或推理明细；保留其它已观察模态，避免下一帧把图片证据抹掉。
				merged := map[string]any{}
				for name, n := range dialogueMap(previous[key]) {
					merged[name] = n
				}
				for name, n := range fields {
					if n != nil {
						merged[name] = n
					}
				}
				previous[key] = merged
				continue
			}
		}
		previous[key] = value
	}
	if blocked != "" {
		previous["pricing_blocked"] = blocked
	}
	return previous
}

// DialogueUsageProblem 检查归一化会覆盖的别名以及原始 token 数值；参数可为部分帧，返回机器可读原因或空串。
// 同一帧的等价别名必须一致，已报告的 token 必须为非负有限整数；null 仍视为缺失，最终完整性检查负责拒绝。
// 递归检查明细，避免非法值被后续帧覆盖后失去证据。
// 原始流观测、同步归一化和硬额度结算共同调用；只检查已观察事实，不要求完整 usage，不修改输入。
func DialogueUsageProblem(usage map[string]any) string {
	for _, group := range dialogueUsageAliases {
		var first float64
		seen := false
		for _, path := range group {
			value := dialogueUsageAlias(usage, path)
			if value == nil {
				continue
			}
			n, ok := dialogueNumber(value)
			if !ok {
				return "upstream_usage_invalid"
			}
			if seen && first != n {
				return "upstream_usage_conflict"
			}
			first, seen = n, true
		}
	}
	for key, value := range usage {
		if value == nil {
			continue
		}
		if strings.HasSuffix(key, "_tokens_details") {
			if _, ok := value.(map[string]any); !ok {
				return "upstream_usage_invalid"
			}
		}
		if strings.HasSuffix(key, "_tokens") {
			n, ok := dialogueNumber(value)
			if !ok || n < 0 || n != math.Trunc(n) {
				return "upstream_usage_invalid"
			}
		}
		if nested, ok := value.(map[string]any); ok {
			if problem := DialogueUsageProblem(nested); problem != "" {
				return problem
			}
		}
	}
	return ""
}

// dialogueUsageAlias 读取固定路径中的计数；参数为用量对象与平级或一层嵌套路径，返回原值，缺失/null 返回 nil。
// 同帧比较和累计快照替换调用；不创建对象或修改供应商原文。
func dialogueUsageAlias(usage map[string]any, path string) any {
	parent, child, nested := strings.Cut(path, ".")
	if !nested {
		return usage[parent]
	}
	fields, _ := usage[parent].(map[string]any)
	return fields[child]
}

// deleteDialogueUsageAlias 删除旧帧的单个别名；参数为本次请求累积对象及固定路径，无返回。
// 嵌套对象可能仍由供应商帧引用，先复制再删除，保留同一明细中的其它计量且不修改历史输入。
func deleteDialogueUsageAlias(usage map[string]any, path string) {
	parent, child, nested := strings.Cut(path, ".")
	if !nested {
		delete(usage, parent)
		return
	}
	fields, _ := usage[parent].(map[string]any)
	if _, exists := fields[child]; !exists {
		return
	}
	copy := make(map[string]any, len(fields)-1)
	for key, value := range fields {
		if key != child {
			copy[key] = value
		}
	}
	usage[parent] = copy
}
