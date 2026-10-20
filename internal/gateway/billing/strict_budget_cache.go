package billing

import "github.com/sunqirui1987/xhub/internal/catalog"

// budgetMaximumCacheTTL 冻结所有可达档的各期限最高价，仅供预占计划验证已支持的缓存维度。
// 参数为已校验且独立复制的档位；返回值映射不引用输入映射，实际费用必须另选实测所在档。
func budgetMaximumCacheTTL(tiers []budgetPriceTier) map[string]catalog.Rate {
	var out map[string]catalog.Rate
	for _, tier := range tiers {
		for ttl, rate := range tier.CacheWriteByTTL {
			if out == nil {
				out = map[string]catalog.Rate{}
			}
			if previous, exists := out[ttl]; !exists || rate.USD > previous.USD {
				out[ttl] = rate
			}
		}
	}
	return out
}

// budgetCacheCreationCounts 读取原始缓存写入期限分桶，校验整数、已知期限及汇总一致性。
// 参数为只读usage；返回5m/1h数量、是否提供分桶及错误。没有分桶不会猜测期限，未知非零桶拒绝。
func budgetCacheCreationCounts(raw map[string]any) (five, hour int, present bool, err error) {
	value := raw["cache_creation"]
	if value == nil {
		return 0, 0, false, nil
	}
	fail := func(message string) (int, int, bool, error) {
		return 0, 0, true, budgetFailure("invalid_usage", message)
	}
	details, ok := value.(map[string]any)
	if !ok {
		return fail("measured cache creation details must be an object")
	}
	for key, value := range details {
		n, ok := budgetUsageInteger(value)
		if !ok {
			return fail("measured cache creation counts must be nonnegative integers")
		}
		switch key {
		case "ephemeral_5m_input_tokens":
			five = n
		case "ephemeral_1h_input_tokens":
			hour = n
		default:
			if n != 0 {
				return fail("measured cache creation TTL is unsupported")
			}
		}
	}
	// 以64位先相加，避免两个分别合法的最大整数在32位环境中溢出。
	total64 := int64(five) + int64(hour)
	if total64 > 2147483647 {
		return fail("measured cache creation counts exceed the supported bound")
	}
	if total64 > 0 {
		total, ok := budgetUsageInteger(raw["cache_creation_input_tokens"])
		if !ok {
			total, ok = budgetUsageInteger(raw["cache_write_tokens"])
		}
		if !ok || int64(total) != total64 {
			return fail("measured cache creation details contradict the total")
		}
	} else {
		// 明确报告两个零桶不能与非零缓存总量矛盾；旧响应完全省略分桶仍由调用方按兼容规则处理。
		for _, key := range []string{"cache_creation_input_tokens", "cache_write_tokens"} {
			if v, exists := raw[key]; exists {
				n, ok := budgetUsageInteger(v)
				if !ok || n != 0 {
					return fail("measured cache creation details contradict the total")
				}
			}
		}
	}
	return five, hour, true, nil
}

// budgetValidateCacheTTL 对比原始分桶与冻结费率，不修改计划或回退到最新目录。
// 参数为可为空的冻结计划及已验证数量；新计划允许已冻结的混合桶，历史单期限快照仍要求完全匹配。
func budgetValidateCacheTTL(plan *budgetPriceSnapshot, five, hour int) error {
	fail := func() error {
		return budgetFailure("invalid_usage", "measured cache creation TTL differs from the reserved price")
	}
	if plan == nil || plan.CacheWrite == nil {
		return fail()
	}
	if len(plan.CacheWriteByTTL) > 0 {
		if _, ok := plan.CacheWriteByTTL[""]; five > 0 && !ok {
			return fail()
		}
		if _, ok := plan.CacheWriteByTTL["1h"]; hour > 0 && !ok {
			return fail()
		}
		return nil
	}
	if five > 0 && hour > 0 {
		return budgetFailure("invalid_usage", "mixed cache creation TTLs require separate prices")
	}
	if hour > 0 && plan.CacheWrite.Variant != "1h" || five > 0 && plan.CacheWrite.Variant == "1h" {
		return fail()
	}
	return nil
}

// budgetAddCacheCharges 按实测分桶和当前实际档的冻结费率累加缓存费用。
// 参数为私有账单、冻结计划、已归一化缓存总数和可选原始usage；错误时调用方丢弃账单，不修改共享计划。
// 多期限计划必须有原始分桶；只有旧单期限计划或仅默认5m费率可沿用聚合计量，不把最高预占价当实收费率。
func budgetAddCacheCharges(c *catalog.Charge, s *budgetPriceSnapshot, total int, raw []map[string]any) error {
	var five, hour int
	var present bool
	var err error
	if len(raw) > 0 {
		five, hour, present, err = budgetCacheCreationCounts(raw[0])
		if err != nil {
			return err
		}
	}
	if present {
		if int64(five)+int64(hour) != int64(total) {
			return budgetFailure("invalid_usage", "measured cache creation details contradict the normalized total")
		}
		if err := budgetValidateCacheTTL(s, five, hour); err != nil {
			return err
		}
	}
	if len(s.CacheWriteByTTL) == 0 {
		budgetAddCharge(c, *s.CacheWrite, total)
		return nil
	}
	if !present {
		rate, ok := s.CacheWriteByTTL[""]
		if len(s.CacheWriteByTTL) != 1 || !ok {
			return budgetFailure("invalid_usage", "measured cache creation TTL details are required for multiple frozen prices")
		}
		budgetAddCharge(c, rate, total)
		return nil
	}
	if five > 0 {
		budgetAddCharge(c, s.CacheWriteByTTL[""], five)
	}
	if hour > 0 {
		budgetAddCharge(c, s.CacheWriteByTTL["1h"], hour)
	}
	return nil
}
