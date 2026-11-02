"use client";
import React, { useEffect, useMemo, useState } from "react";
import { parseAsString, useQueryStates } from "nuqs";
import { ResponsiveContainer, LineChart, Line, CartesianGrid, XAxis, YAxis, Tooltip, Legend } from "recharts";
import { getProxyBaseUrl } from "@/components/networking";
import { useI18n } from "@/i18n";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { RefreshCw } from "lucide-react";
import {
  chartRows,
  monitorOptions,
  parseMonitorData,
  reconcileMonitorFilters,
  localDate,
  seriesKey,
  validWindow,
  metrics,
  type Metric,
  type MonitorData,
  type MonitorRow,
} from "./data";
const colors = ["#6366f1", "#06b6d4", "#f59e0b", "#10b981", "#ec4899", "#8b5cf6", "#ef4444", "#64748b"];
/** ModelMonitorView 查询当前登录者的预聚合监控；参数 accessToken 为会话令牌，返回筛选、趋势和明细。
 * 页面及组件测试调用；切换请求会取消旧请求，错误和空值显式展示，默认七日、最多九十日。 */
export function ModelMonitorView({ accessToken }: { accessToken: string | null }) {
  const { t, locale } = useI18n();
  const [end, setEnd] = useState(() => localDate(new Date()));
  const [start, setStart] = useState(() => {
    const d = new Date();
    d.setDate(d.getDate() - 6);
    return localDate(d);
  });
  const [window, setWindow] = useState({ start, end, revision: 0 });
  const [data, setData] = useState<MonitorData | null>(null);
  const [loading, setLoading] = useState(true),
    [error, setError] = useState(false),
    [invalid, setInvalid] = useState(false);
  // 将筛选保存在地址中，便于分享当前查看范围并支持浏览器前进后退。
  const [{ supplier, model, metric: selectedMetric }, setFilters] = useQueryStates({
    supplier: parseAsString.withDefault(""),
    model: parseAsString.withDefault(""),
    metric: parseAsString.withDefault("avg_tps"),
  });
  const metric: Metric = metrics.includes(selectedMetric as Metric) ? (selectedMetric as Metric) : "avg_tps";
  useEffect(() => {
    if (!accessToken) return;
    const controller = new AbortController();
    setLoading(true);
    setError(false);
    setData(null);
    const params = new URLSearchParams({
      start_date: window.start,
      end_date: window.end,
      timezone: String(new Date().getTimezoneOffset()),
    });
    fetch(getProxyBaseUrl() + "/models/monitor?" + params, {
      headers: { Authorization: "Bearer " + accessToken },
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) throw new Error(String(response.status));
        return parseMonitorData(await response.json());
      })
      .then((result) => {
        setData(result);
        setLoading(false);
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setError(true);
          setLoading(false);
        }
      });
    return () => controller.abort();
  }, [accessToken, window]);
  const options = useMemo(() => (data ? monitorOptions(data) : []), [data]);
  // 仅在候选成功返回后清理撤权或失效的地址参数；加载和报错时保留用户原有筛选。
  const { supplier: activeSupplier, model: activeModel } = reconcileMonitorFilters(
    data && !loading && !error ? options : null,
    supplier,
    model,
  );
  useEffect(() => {
    if (activeSupplier !== supplier || activeModel !== model) {
      void setFilters({ supplier: activeSupplier, model: activeModel });
    }
  }, [activeSupplier, activeModel, supplier, model, setFilters]);
  const rows = useMemo(
    () =>
      (data?.models ?? []).filter((row) => (!activeSupplier || row.supplier === activeSupplier) && (!activeModel || row.model === activeModel)),
    [data, activeSupplier, activeModel],
  );
  // 卡片与图表、明细使用同一筛选范围；只累加计数与费用，性能平均值仍由服务端样本加权。
  const totals = rows.reduce(
    (sum, row) => ({
      requests: sum.requests + row.requests,
      total_tokens: sum.total_tokens + row.total_tokens,
      cost: sum.cost + row.cost,
      failed_requests: sum.failed_requests + row.failed_requests,
    }),
    { requests: 0, total_tokens: 0, cost: 0, failed_requests: 0 },
  );
  const shown = rows.slice(0, 8),
    keys = shown.map(seriesKey);
  const plot = data ? chartRows(data, metric, keys) : [];
  /** format 展示有限指标，缺失性能样本显示本地化占位；金额美元六位精度，页面各视图调用，无副作用。 */
  const format = (value: number | null | undefined, money = false) =>
    value == null || !Number.isFinite(value)
      ? t("monitor.noSample")
      : (money ? "$" : "") + value.toLocaleString(locale, { maximumFractionDigits: money ? 6 : 2 });
  /** label 为连接名称及实际模型生成图例；空身份使用翻译占位，不修改原始模型字符串。 */
  const label = (row: MonitorRow) =>
    (row.supplier || t("monitor.unassigned")) + " / " + (row.model || t("monitor.unknownModel"));
  return (
    <main className="mx-auto w-full min-w-0 max-w-[1600px] space-y-6 text-foreground">
      <div className="flex flex-wrap items-end justify-between gap-6">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{t("monitor.title")}</h1>
          <p className="mt-2 max-w-2xl text-sm text-muted-foreground">{t("monitor.description")}</p>
        </div>
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (!validWindow(start, end)) {
              setInvalid(true);
              return;
            }
            setInvalid(false);
            void setFilters({ supplier: "", model: "" });
            setWindow({ start, end, revision: window.revision + 1 });
          }}
        >
          <label className="grid gap-1.5 text-sm font-medium">
            {t("monitor.start")}
            <Input
              className="w-40 bg-background"
              aria-label={t("monitor.start")}
              aria-invalid={invalid}
              type="date"
              required
              value={start}
              onChange={(e) => setStart(e.target.value)}
            />
          </label>
          <label className="grid gap-1.5 text-sm font-medium">
            {t("monitor.end")}
            <Input
              className="w-40 bg-background"
              aria-label={t("monitor.end")}
              aria-invalid={invalid}
              type="date"
              required
              value={end}
              onChange={(e) => setEnd(e.target.value)}
            />
          </label>
          <Button variant="outline" type="submit" disabled={loading && !error}>
            <RefreshCw className={loading ? "size-4 animate-spin" : "size-4"} aria-hidden="true" />
            {t("monitor.refresh")}
          </Button>
        </form>
      </div>
      {invalid && (
        <p
          className="rounded-lg border border-destructive/20 bg-destructive/5 px-4 py-3 text-sm text-destructive"
          role="alert"
        >
          {t("monitor.invalidRange")}
        </p>
      )}
      {loading && (
        <p className="text-sm text-muted-foreground" role="status">
          {t("monitor.loading")}
        </p>
      )}
      {error && (
        <p
          className="rounded-lg border border-destructive/20 bg-destructive/5 px-4 py-3 text-sm text-destructive"
          role="alert"
        >
          {t("monitor.error")}
        </p>
      )}
      {data && (
        <>
          {data.visibility_scope === "current_available" && <p className="text-sm text-muted-foreground">{t("monitor.currentScopeNote")}</p>}
          <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
            {(["requests", "total_tokens", "cost", "failed_requests"] as const).map((key) => (
              <section key={key} aria-label={t("monitor." + key)} className="rounded-lg border bg-card px-4 py-4">
                <h2 className="text-sm text-muted-foreground">{t("monitor." + key)}</h2>
                <p className="mt-2 text-2xl font-semibold tracking-tight tabular-nums">
                  {format(totals[key], key === "cost")}
                </p>
              </section>
            ))}
          </div>
          <div className="flex flex-wrap items-end gap-3 rounded-lg border bg-card p-4">
            <label className="grid min-w-40 flex-1 gap-1.5 text-sm font-medium">
              {t("monitor.supplier")}
              <select
                className="h-9 w-full min-w-0 rounded-md border border-input bg-background px-3 text-sm font-normal shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                aria-label={t("monitor.supplier")}
                value={activeSupplier}
                onChange={(e) => {
                  void setFilters({ supplier: e.target.value, model: "" });
                }}
              >
                <option value="">{t("monitor.allSuppliers")}</option>
                {Array.from(new Set(options.map((r) => r.supplier)))
                  .filter(Boolean)
                  .map((value) => (
                    <option key={value}>{value}</option>
                  ))}
              </select>
            </label>
            <label className="grid min-w-40 flex-1 gap-1.5 text-sm font-medium">
              {t("monitor.model")}
              <select
                className="h-9 w-full min-w-0 rounded-md border border-input bg-background px-3 text-sm font-normal shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                aria-label={t("monitor.model")}
                value={activeModel}
                onChange={(e) => void setFilters({ model: e.target.value })}
              >
                <option value="">{t("monitor.allModels")}</option>
                {Array.from(new Set(options.filter((r) => !activeSupplier || r.supplier === activeSupplier).map((r) => r.model)))
                  .filter(Boolean)
                  .map((value) => (
                    <option key={value}>{value}</option>
                  ))}
              </select>
            </label>
            <label className="grid min-w-40 flex-1 gap-1.5 text-sm font-medium">
              {t("monitor.metric")}
              <select
                className="h-9 w-full min-w-0 rounded-md border border-input bg-background px-3 text-sm font-normal shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                aria-label={t("monitor.metric")}
                value={metric}
                onChange={(e) => void setFilters({ metric: e.target.value })}
              >
                {metrics.map((value) => (
                  <option key={value} value={value}>
                    {t("monitor." + value)}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <section className="rounded-lg border bg-card p-5" aria-label={t("monitor.trend")}>
            <h2 className="text-base font-semibold">{t("monitor.trend")}</h2>
            {rows.length > 8 && (
              <p className="text-sm text-muted-foreground">{t("monitor.chartLimit", { count: 8 })}</p>
            )}
            {rows.length > 0 ? (
              <div className="h-80 mt-4">
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart data={plot} accessibilityLayer>
                    <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" vertical={false} />
                    <XAxis
                      dataKey="day"
                      axisLine={false}
                      tickLine={false}
                      tick={{ fill: "var(--muted-foreground)", fontSize: 12 }}
                      tickMargin={12}
                    />
                    <YAxis
                      axisLine={false}
                      tickLine={false}
                      tick={{ fill: "var(--muted-foreground)", fontSize: 12 }}
                      width={64}
                    />
                    <Tooltip
                      contentStyle={{
                        background: "var(--card)",
                        border: "1px solid var(--border)",
                        borderRadius: 8,
                        color: "var(--foreground)",
                        fontSize: 12,
                      }}
                      formatter={(value) => format(typeof value === "number" ? value : null, metric === "cost")}
                    />
                    <Legend wrapperStyle={{ paddingTop: 16, fontSize: 12 }} />
                    {shown.map((row, i) => (
                      <Line
                        key={keys[i]}
                        dataKey={(point) => point[keys[i]]}
                        name={label(row)}
                        stroke={colors[i]}
                        type="monotone"
                        strokeWidth={2}
                        connectNulls={false}
                        isAnimationActive={false}
                      />
                    ))}
                  </LineChart>
                </ResponsiveContainer>
              </div>
            ) : (
              <p className="py-16 text-center text-sm text-muted-foreground">{t("monitor.empty")}</p>
            )}
          </section>
          <section className="overflow-auto rounded-lg border bg-card">
            <table className="w-full text-sm">
              <caption className="px-5 py-4 text-left text-base font-semibold">
                {t("monitor.detail", { count: rows.length })}
              </caption>
              <thead className="bg-muted/40 text-muted-foreground">
                <tr>
                  {[
                    "supplier",
                    "model",
                    "requests",
                    "failed_requests",
                    "avg_tps",
                    "avg_ttft_ms",
                    "avg_latency_ms",
                    "prompt_tokens",
                    "completion_tokens",
                    "total_tokens",
                    "cost",
                    "tps_samples",
                    "ttft_samples",
                  ].map((key) => (
                    <th
                      className="border-b px-4 py-3 text-left text-xs font-medium whitespace-nowrap"
                      key={key}
                      scope="col"
                    >
                      {t("monitor." + key)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={seriesKey(row)} className="transition-colors hover:bg-muted/30">
                    <td className="border-b px-4 py-3 text-sm text-muted-foreground">
                      {row.supplier || t("monitor.unassigned")}
                    </td>
                    <th className="border-b px-4 py-3 text-left text-sm font-medium" scope="row">
                      {row.model || t("monitor.unknownModel")}
                    </th>
                    {(
                      [
                        "requests",
                        "failed_requests",
                        "avg_tps",
                        "avg_ttft_ms",
                        "avg_latency_ms",
                        "prompt_tokens",
                        "completion_tokens",
                        "total_tokens",
                        "cost",
                        "tps_samples",
                        "ttft_samples",
                      ] as const
                    ).map((key) => (
                      <td key={key} className="border-b px-4 py-3 text-right text-sm tabular-nums">
                        {format(row[key], key === "cost")}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
          <p className="text-sm text-muted-foreground">{t("monitor.measurement")}</p>
        </>
      )}
    </main>
  );
}
