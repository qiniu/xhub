import { describe, expect, it } from "vitest";
import { formatLogCost } from "./formatLogCost";

/** 前置纯数字及未知输入；验证日志金额完整精度、零值、极小值和失败输入，无页面或后台状态，无需清理。 */
describe("日志金额精度", () => {
  it.each([
    [0.00308641, "$0.00308641"], [0.00022396, "$0.00022396"], [0.000000000001, "$0.000000000001"],
    [1e-25, "$0.0000000000000000000000001"], [0, "$0.00"], [-0, "$0.00"], [2, "$2.00"],
    [1000.25, "$1,000.25"], [-0.00000001, "$-0.00000001"], [undefined, "-"], [null, "-"],
    [NaN, "-"], [Infinity, "-"], [-Infinity, "-"],
  ] as const)("%s 显示为 %s", (value, expected) => expect(formatLogCost(value)).toBe(expected));
});

/** 前置 API 零值和定价/响应缓存元数据；验证未知、真实免费、兼容旧数据及无效数值，纯函数无需清理。 */
describe("日志费用状态", () => {
  it.each([
    [0, "unpriced", false, "-"], [0, "unpriced", "false", "-"],
    [0, "unpriced", true, "$0.00"], [0, "unpriced", "TRUE", "$0.00"],
    [0, "priced", false, "$0.00"], [0, undefined, false, "$0.00"],
    [0.01, "unpriced", false, "$0.01"], [NaN, "priced", true, "-"],
    [undefined, "priced", true, "-"],
  ] as const)("金额 %s、定价 %s、缓存 %s 显示 %s", (amount, pricingStatus, cacheHit, expected) => {
    expect(formatLogCost(amount, { metadata: { cost_breakdown: { pricing_status: pricingStatus } }, cache_hit: cacheHit })).toBe(expected);
  });
});
