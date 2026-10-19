import React from "react";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { fireEvent, render, screen, waitFor, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/i18n";
import { ModelMonitorView } from "./view";
vi.mock("@/components/networking", () => ({ getProxyBaseUrl: () => "http://gateway" }));
vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: () => ({ accessToken: "session" }) }));
vi.mock("recharts", () => ({
  ResponsiveContainer: ({ children }: any) => <div>{children}</div>,
  LineChart: ({ children }: any) => <div>{children}</div>,
  Line: () => null,
  CartesianGrid: () => null,
  XAxis: () => null,
  YAxis: () => null,
  Tooltip: () => null,
  Legend: () => null,
}));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});
const row = {
  supplier: "supplier-a",
  model: "original-model",
  requests: 2,
  failed_requests: 0,
  prompt_tokens: 20,
  completion_tokens: 10,
  total_tokens: 30,
  cost: 0.25,
  avg_tps: null,
  avg_ttft_ms: null,
  avg_latency_ms: 200,
  tps_samples: 0,
  ttft_samples: 0,
};
/** 前置隔离请求替身，验证中英文、动态数量、筛选和日期错误；无网络持久化，afterEach 清理 DOM。 */
describe("model monitor view", () => {
  it.each(["zh-CN", "en"] as const)("renders scoped rows and validation in %s", async (locale) => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({
          options: [],
          summary: row,
          models: [row],
          trend: [],
          start_date: "2026-01-01",
          end_date: "2026-01-02",
        }),
      }),
    );
    render(
      <NuqsTestingAdapter hasMemory>
        <I18nProvider initialLocale={locale}>
          <ModelMonitorView accessToken="session" />
        </I18nProvider>
      </NuqsTestingAdapter>,
    );
    expect(await screen.findByRole("rowheader", { name: "original-model" })).toBeVisible();
    expect(screen.getByText(locale === "en" ? "Model details (1 combinations)" : "模型明细（1 个组合）")).toBeVisible();
    expect(screen.getByText(locale === "en" ? /TPS = output tokens/ : /TPS 为输出 Token/)).toHaveTextContent(locale === "en" ? "Synchronous requests have no TPS sample" : "同步请求无 TPS 样本");
    const start = screen.getByLabelText(locale === "en" ? "Start date" : "开始日期");
    const end = screen.getByLabelText(locale === "en" ? "End date" : "结束日期");
    fireEvent.change(start, { target: { value: "2026-02-01" } });
    fireEvent.change(end, { target: { value: "2026-01-01" } });
    fireEvent.click(screen.getByRole("button", { name: locale === "en" ? "Refresh" : "刷新" }));
    expect(screen.getByRole("alert")).toHaveTextContent(locale === "en" ? "1 to 90" : "1 至 90");
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  /** 前置两家供应商同模型，验证筛选后卡片与表格同步、重新加载解除过期筛选；afterEach 清理 DOM 和请求替身。 */
  it("keeps summary aligned with supplier filter and resets it on refresh", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({
          options: [],
          summary: row,
          models: [row, { ...row, supplier: "supplier-b" }],
          trend: [],
          start_date: "2026-01-01",
          end_date: "2026-01-02",
        }),
      }),
    );
    render(
      <NuqsTestingAdapter hasMemory>
        <I18nProvider initialLocale="en">
          <ModelMonitorView accessToken="session" />
        </I18nProvider>
      </NuqsTestingAdapter>,
    );
    await screen.findAllByRole("rowheader", { name: "original-model" });
    expect(screen.getByRole("region", { name: "Requests" })).toHaveTextContent("4");
    fireEvent.change(screen.getByLabelText("Supplier connection"), { target: { value: "supplier-a" } });
    expect(screen.getByRole("region", { name: "Requests" })).toHaveTextContent("2");
    expect(screen.getAllByRole("rowheader")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(screen.getByLabelText("Supplier connection")).toHaveValue(""));
  });
  /** 前置空统计，验证空态与零计数，缺失性能不显示虚假数值；afterEach 清理替身与 DOM。 */
  it("renders an empty monitor without invented samples", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({
          options: [
            { supplier: "configured-a", model: "unused-model" },
            { supplier: "configured-b", model: "another-model" },
          ],
          summary: row,
          models: [],
          trend: [],
          start_date: "2026-01-01",
          end_date: "2026-01-02",
        }),
      }),
    );
    render(
      <NuqsTestingAdapter hasMemory>
        <I18nProvider initialLocale="en">
          <ModelMonitorView accessToken="session" />
        </I18nProvider>
      </NuqsTestingAdapter>,
    );
    expect(await screen.findByText("No completed requests in this period.")).toBeVisible();
    expect(screen.getByRole("region", { name: "Requests" })).toHaveTextContent("0");
    expect(screen.getByRole("option", { name: "configured-a" })).toBeVisible();
    expect(screen.getByRole("option", { name: "unused-model" })).toBeVisible();
    fireEvent.change(screen.getByLabelText("Supplier connection"), { target: { value: "configured-a" } });
    await waitFor(() => expect(screen.queryByRole("option", { name: "another-model" })).not.toBeInTheDocument());
    fireEvent.change(screen.getByLabelText("Actual model"), { target: { value: "unused-model" } });
    expect(screen.getByText("No completed requests in this period.")).toBeVisible();
    expect(screen.getByRole("region", { name: "Requests" })).toHaveTextContent("0");
  });
  it("renders request error and retries", async () => {
    const request = vi.fn().mockResolvedValue({ ok: false });
    vi.stubGlobal("fetch", request);
    render(
      <NuqsTestingAdapter hasMemory>
        <I18nProvider initialLocale="en">
          <ModelMonitorView accessToken="session" />
        </I18nProvider>
      </NuqsTestingAdapter>,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load");
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(request).toHaveBeenCalledTimes(2));
  });
});
