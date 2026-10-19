/**
 * 将日志原始美元金额完整显示；value 为 API 数值，返回带美元符号的字符串，缺失或非有限值返回占位符。
 * 日志表、会话、指标和费用明细共用；保留接口数值的有效数字与至少两位小数，不把小额费用显示为零。
 * 不重新计费或改写原始数据；极小金额使用完整小数，真实零值显示 $0.00，负零归零。
 */
export function formatLogCost(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "-";
  const rendered = new Intl.NumberFormat("en-US", { maximumSignificantDigits: 21 }).format(value === 0 ? 0 : value);
  const [whole, fraction = ""] = rendered.split(".");
  return `$${whole}.${fraction.padEnd(2, "0")}`;
}
