export type MonitorRow = {
  supplier: string;
  model: string;
  day: string;
  requests: number;
  failed_requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cost: number;
  avg_tps: number | null;
  avg_ttft_ms: number | null;
  avg_latency_ms: number | null;
  tps_samples: number;
  ttft_samples: number;
};
export type MonitorData = {
  options: MonitorOption[];
  summary: MonitorRow;
  models: MonitorRow[];
  trend: MonitorRow[];
  start_date: string;
  end_date: string;
  visibility_scope?: "current_available" | "historical";
};
/** MonitorOption 是配置目录中的公开身份，不包含账单或连接密钥。 */
export type MonitorOption = Pick<MonitorRow, "supplier" | "model">;
/** parseMonitorData 校验接口目录和统计结构，供页面请求边界调用；返回可展示数据。
 * 参数为不可信 JSON；缺字段或无效身份抛出错误，页面展示本地化重试提示，不隐瞒成空目录。 */
export function parseMonitorData(value: unknown): MonitorData {
  const data = value as MonitorData | null;
  if (
    !data ||
    !Array.isArray(data.options) ||
    !Array.isArray(data.models) ||
    !Array.isArray(data.trend) ||
    !data.summary ||
    typeof data.start_date !== "string" ||
    typeof data.end_date !== "string" ||
    [...data.options, ...data.models, ...data.trend].some(
      (row) => !row || typeof row.supplier !== "string" || typeof row.model !== "string",
    )
  ) {
    throw new TypeError("monitor_response_invalid");
  }
  return data;
}
/** monitorOptions 仅返回后台当前授权且启用的候选，供下拉筛选调用；不以历史结果扩大目录。
 * 参数为接口数据，返回去重排序身份；不修改输入，空目录返回空数组。 */
export function monitorOptions(data: MonitorData): MonitorOption[] {
  const options = new Map<string, MonitorOption>();
  for (const row of data.options) {
    options.set(seriesKey(row), { supplier: row.supplier, model: row.model });
  }
  return [...options.values()].sort((a, b) => a.supplier.localeCompare(b.supplier) || a.model.localeCompare(b.model));
}
/** reconcileMonitorFilters 根据已成功加载的授权候选修正地址筛选；候选未知时保留原值。
 * 供监控页面及单元测试调用；供应商失效时同时清除模型，模型失效时只清除模型，不修改参数。 */
export function reconcileMonitorFilters(options: MonitorOption[] | null, supplier: string, model: string) {
  if (!options) return { supplier, model };
  if (supplier && !options.some((option) => option.supplier === supplier)) return { supplier: "", model: "" };
  if (model && !options.some((option) => (!supplier || option.supplier === supplier) && option.model === model)) {
    return { supplier, model: "" };
  }
  return { supplier, model };
}
export const metrics = ["avg_tps", "avg_ttft_ms", "total_tokens", "cost", "requests", "avg_latency_ms"] as const;
export type Metric = (typeof metrics)[number];
/** localDate 将日期转为浏览器本地日历字符串，供日期控件使用；不改变参数，无副作用。 */
export function localDate(date: Date): string {
  return [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, "0"),
    String(date.getDate()).padStart(2, "0"),
  ].join("-");
}
/** chartRows 把授权后的模型日趋势按 supplier/model 组合成多线图数据；返回完整日期及空样本间断。
 * 参数为接口数据、指标及选中的组合标识；页面调用，无写入，缺失性能样本不填成零。 */
export function chartRows(data: MonitorData, metric: Metric, selected: string[]) {
  const rows: Record<string, Record<string, string | number | null>> = {};
  const start = new Date(data.start_date + "T00:00:00Z"),
    end = new Date(data.end_date + "T00:00:00Z");
  for (let date = start; date <= end; date = new Date(date.getTime() + 86400000)) {
    const day = date.toISOString().slice(0, 10);
    rows[day] = { day };
    selected.forEach((key) => {
      rows[day][key] = metric.startsWith("avg_") ? null : 0;
    });
  }
  for (const row of data.trend) {
    const key = seriesKey(row);
    if (selected.includes(key) && rows[row.day]) rows[row.day][key] = row[metric];
  }
  return Object.values(rows);
}
/** seriesKey 为供应商实际模型生成无歧义身份，供图表及筛选使用；参数为模型行，无副作用。 */
export function seriesKey(row: Pick<MonitorRow, "supplier" | "model">) {
  return JSON.stringify([row.supplier, row.model]);
}
/** validWindow 验证本地日期窗口为一至九十天，供提交前检查；非法或反向日期返回 false，无副作用。 */
export function validWindow(start: string, end: string) {
  const a = Date.parse(start + "T00:00:00Z"),
    b = Date.parse(end + "T00:00:00Z");
  return Number.isFinite(a) && Number.isFinite(b) && b >= a && b - a < 90 * 86400000;
}
