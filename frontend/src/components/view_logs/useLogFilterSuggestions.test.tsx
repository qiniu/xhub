import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLogFilterSuggestions } from "./useLogFilterSuggestions";

const state = vi.hoisted(() => ({ token: "credential-a", user: "user-a" }));
vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: () => ({ accessToken: state.token, userId: state.user }) }));
vi.mock("@/components/networking", () => ({ getProxyBaseUrl: () => "/gw" }));
const window = { start_date: "2026-10-01 00:00:00", end_date: "2026-11-01 00:00:00" };

/** 为候选查询测试提供独立查询缓存，结束后由测试卸载；无后台数据。 */
function createWrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useLogFilterSuggestions", () => {
  beforeEach(() => { state.token = "credential-a"; state.user = "user-a"; });
  afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  /** 打开抽屉时将原始筛选、身份与时间准确交给后端，关闭时不请求；测试移除 fetch mock。 */
  it("fetches a bounded scoped page only when open", async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: true, text: async () => JSON.stringify({ data: ["模型-a"], page: 1, page_size: 50, has_more: false }) });
    vi.stubGlobal("fetch", fetcher);
    const { result, rerender } = renderHook(({ open }) => useLogFilterSuggestions("model", window, "模型", open), { wrapper: createWrapper(), initialProps: { open: false } });
    expect(fetcher).not.toHaveBeenCalled();
    rerender({ open: true });
    await waitFor(() => expect(result.current.data?.data).toEqual(["模型-a"]));
    const [target, init] = fetcher.mock.calls[0];
    const url = new URL(target, "http://example.test");
    expect(url.pathname).toBe("/gw/spend/logs/ui/filter-options");
    expect(Object.fromEntries(url.searchParams)).toMatchObject({ field: "model", q: "模型", start_date: window.start_date, end_date: window.end_date, page: "1", page_size: "50" });
    expect(init.headers.Authorization).toBe("Bearer credential-a");
    expect(init.headers["Cache-Control"]).toBe("no-store");
    expect(init.signal).toBeInstanceOf(AbortSignal);
  });

  /** 切换身份不复用旧账号候选，后端失败不变成空结果；测试用独立内存缓存并清除 mock。 */
  it("isolates identity and reports failed lookup without stale options", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce({ ok: true, text: async () => JSON.stringify({ data: ["private-a"], page: 1, page_size: 50, has_more: false }) }).mockResolvedValueOnce({ ok: false, status: 503, text: async () => "" });
    vi.stubGlobal("fetch", fetcher);
    const { result, rerender } = renderHook(() => useLogFilterSuggestions("supplier", window, "", true), { wrapper: createWrapper() });
    await waitFor(() => expect(result.current.data?.data).toEqual(["private-a"]));
    state.token = "credential-b"; state.user = "user-b";
    rerender();
    expect(result.current.data).toBeUndefined();
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
  });

  /** 后台响应形状错误必须显示失败而不能伪装空目录；测试无需持久化清理。 */
  it("rejects malformed result pages", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, text: async () => JSON.stringify({ data: ["x"], has_more: false }) }));
    const { result } = renderHook(() => useLogFilterSuggestions("key_alias", window, "", true), { wrapper: createWrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
  });
});
