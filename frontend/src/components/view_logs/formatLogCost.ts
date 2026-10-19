import type { LogEntry } from "./columns";

/**
 * 将日志原始美元金额完整显示；value 为 API 数值，log 可选传入定价和响应缓存状态，返回金额或占位符。
 * 日志表、会话、指标和费用明细共用；保留接口数值的有效数字与至少两位小数，不把小额费用显示为零。
 * 不重新计费或改写原始数据；极小金额使用完整小数，真实零值显示 $0.00，负零归零。
 * 未定价请求的零值只是后台占位，显示未知；响应缓存命中为真实零费用，正数会话合计仍保留已记录金额。
 */
export function formatLogCost(value: number | null | undefined, log?: Pick<LogEntry, "metadata" | "cache_hit">): string {
  if (value == null || !Number.isFinite(value)) return "-";
  if (
    value === 0 &&
    log?.metadata?.cost_breakdown?.pricing_status === "unpriced" &&
    String(log.cache_hit).toLowerCase() !== "true"
  ) return "-";
  const rendered = new Intl.NumberFormat("en-US", { maximumSignificantDigits: 21 }).format(value === 0 ? 0 : value);
  const [whole, fraction = ""] = rendered.split(".");
  return `$${whole}.${fraction.padEnd(2, "0")}`;
}
