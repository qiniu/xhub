import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import React, { ReactNode } from "react";
import { useSetKeyBlockedState, setKeyBlockedState } from "./useSetKeyBlockedState";
import { apiClient } from "@/components/networking";
import { getActiveLocale, setActiveLocale } from "@/i18n/runtime";
import { translate } from "@/i18n/translate";

vi.mock("@/components/networking", () => ({ apiClient: { post: vi.fn() } }));
const mockUseAuthorized = vi.fn();
vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: () => mockUseAuthorized() }));
const mockPost = vi.mocked(apiClient.post);
const initialLocale = getActiveLocale();
const clients: QueryClient[] = [];

/** 为mutation测试建立无自动重试的隔离缓存，返回provider；测试结束统一clear，不遗留异步缓存或DOM。 */
const createWrapper = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(queryClient);
  /** 给当前测试hook提供独立缓存；只渲染children，无持久化或外部请求。 */
  const wrapper = ({ children }: { children: ReactNode }) => React.createElement(QueryClientProvider, { client: queryClient }, children);
  return { queryClient, wrapper };
};

beforeEach(() => {
  mockPost.mockReset();
  mockUseAuthorized.mockReturnValue({ accessToken: "test-session" });
  setActiveLocale("en");
});
afterEach(() => {
  clients.splice(0).forEach(client => client.clear());
  setActiveLocale(initialLocale);
});

describe("setKeyBlockedState", () => {
  /** 对两种方向验证实际路由、原始记录ID和明确的布尔回包；客户端替身不证明存储，真实生效由regression/E2E覆盖，无数据清理。 */
  it.each([true, false])("accepts only a confirmed state: %s", async (blocked) => {
    mockPost.mockResolvedValueOnce({ blocked });
    expect(await setKeyBlockedState("test-session", { keyToken: "record-id", blocked })).toEqual({ blocked });
    expect(mockPost).toHaveBeenCalledWith(blocked ? "/key/block" : "/key/unblock", {
      accessToken: "test-session", body: { key: "record-id" },
    });
  });

  /** 错误成功响应不能按请求补成结果；覆盖空、缺字段、类型错误及反向状态，中英文均检查实际翻译，afterEach恢复语言。 */
  for (const locale of ["en", "zh-CN"] as const) {
    for (const blocked of [true, false]) {
      it.each([null, undefined, {}, { blocked: null }, { blocked: "true" }, { blocked: 1 }, { blocked: !blocked }, [], true, "ok"])(
        `rejects invalid confirmation ${locale} requested=${blocked}: %j`, async (response) => {
          setActiveLocale(locale);
          expect(translate(locale, "keyActions.invalidStateResponse")).not.toBe("keyActions.invalidStateResponse");
          mockPost.mockResolvedValueOnce(response);
          await expect(setKeyBlockedState("test-session", { keyToken: "record-id", blocked }))
            .rejects.toThrow(translate(locale, "keyActions.invalidStateResponse"));
          expect(mockPost).toHaveBeenCalledTimes(1);
        },
      );
    }
  }

  /** 网络/HTTP错误保持原异常，不变造为成功或重复请求；替身与状态在afterEach回收。 */
  it.each(["Network unavailable", "HTTP 403", "HTTP 500"])("preserves request errors: %s", async (message) => {
    const error = new Error(message);
    mockPost.mockRejectedValueOnce(error);
    await expect(setKeyBlockedState("test-session", { keyToken: "record-id", blocked: true })).rejects.toBe(error);
    expect(mockPost).toHaveBeenCalledTimes(1);
  });
});

describe("useSetKeyBlockedState", () => {
  /** 明确确认后刷新Key缓存；隔离hook缓存不涉及后台状态，afterEach清理。 */
  it("invalidates key queries after confirmed success", async () => {
    mockPost.mockResolvedValueOnce({ blocked: true });
    const { queryClient, wrapper } = createWrapper();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const { result } = renderHook(() => useSetKeyBlockedState(), { wrapper });
    result.current.mutate({ keyToken: "record-id", blocked: true });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["keys"] });
  });

  /** 畸形响应必须进入mutation错误态，不调用成功回调或刷新成功缓存；框架卸载DOM，afterEach清理缓存。 */
  it.each([null, {}, { blocked: false }])("does not report success for an unconfirmed write: %j", async (response) => {
    mockPost.mockResolvedValueOnce(response);
    const { queryClient, wrapper } = createWrapper();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    const onSuccess = vi.fn();
    const { result } = renderHook(() => useSetKeyBlockedState(), { wrapper });
    result.current.mutate({ keyToken: "record-id", blocked: true }, { onSuccess });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(false);
    expect(onSuccess).not.toHaveBeenCalled();
    expect(invalidate).not.toHaveBeenCalled();
  });

  /** 无会话不发请求；验证既有权限前置行为保持，隔离缓存由afterEach清理。 */
  it("errors without an access token", async () => {
    mockUseAuthorized.mockReturnValue({ accessToken: null });
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useSetKeyBlockedState(), { wrapper });
    result.current.mutate({ keyToken: "record-id", blocked: true });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(mockPost).not.toHaveBeenCalled();
  });
});
