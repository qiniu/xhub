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
