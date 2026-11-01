import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { type ReactNode } from "react";
import { logDetailsQueryOptions, useLogDetails } from "./useLogDetails";
import { uiSpendLogDetailsCall } from "@/components/networking";
vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: () => ({ accessToken: "test-session" }) }));
vi.mock("@/components/networking", () => ({ uiSpendLogDetailsCall: vi.fn() }));

/** 前置隔离查询缓存；按参数创建懒查询，返回 hook 与缓存清理句柄，不访问真实后台。 */
function setup(id: string | undefined, start: string | undefined, enabled: boolean) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = renderHook(() => useLogDetails(id, start, enabled), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  });
  return {
    ...view,
    cleanup: () => {
      view.unmount();
      client.clear();
    },
  };
}
/** 前置历史记录不在当前列表窗口；详情仍按 ID 加载且不发送日期，结束清理查询缓存。 */
it.each([undefined, "2026-11-01 00:00:00"])("loads history independently of start %s", async (start) => {
  vi.mocked(uiSpendLogDetailsCall).mockReset().mockResolvedValue({ request_id: "old" });
  const view = setup("old", start, true);
  try {
    await waitFor(() => expect(view.result.current.isSuccess).toBe(true));
    expect(uiSpendLogDetailsCall).toHaveBeenCalledWith("test-session", "old", "");
  } finally {
    view.cleanup();
  }
});
/** 前置抽屉关闭或 ID 缺失；不得请求详情，结束清理缓存。 */
it.each([
  [undefined, true],
  ["old", false],
] as const)("does not query missing or closed details", async (id, enabled) => {
  vi.mocked(uiSpendLogDetailsCall).mockReset();
  const view = setup(id, undefined, enabled);
  try {
    expect(view.result.current.fetchStatus).toBe("idle");
    expect(uiSpendLogDetailsCall).not.toHaveBeenCalled();
  } finally {
    view.cleanup();
  }
});
/** 前置后台返回失败；保留错误状态供 UI 重试，不将失败吞成空数据，结束清理缓存。 */
it("exposes detail failures", async () => {
  vi.mocked(uiSpendLogDetailsCall).mockReset().mockRejectedValue(new Error("unavailable"));
  const view = setup("old", undefined, true);
  try {
    await waitFor(() => expect(view.result.current.isError).toBe(true));
    expect(view.result.current.data).toBeUndefined();
  } finally {
    view.cleanup();
  }
});

/** 前置两个登录凭据读取同一 ID；同身份的深链接与抽屉复用结果，不同身份重新查询，最后清理缓存。 */
it("isolates shared detail cache by the current credential", async () => {
  const client = new QueryClient();
  vi.mocked(uiSpendLogDetailsCall).mockReset().mockImplementation(async (token) => ({ request_id: "same-id", user: token }));
  try {
    const first = await client.fetchQuery(logDetailsQueryOptions("first-viewer", "same-id"));
    const cached = await client.fetchQuery(logDetailsQueryOptions("first-viewer", "same-id"));
    const second = await client.fetchQuery(logDetailsQueryOptions("second-viewer", "same-id"));
    expect(first?.user).toBe("first-viewer");
    expect(cached).toEqual(first);
    expect(second?.user).toBe("second-viewer");
    expect(uiSpendLogDetailsCall).toHaveBeenCalledTimes(2);
    expect(await logDetailsQueryOptions(null, "same-id").queryFn?.({} as never)).toBeNull();
    expect(await logDetailsQueryOptions("session", undefined).queryFn?.({} as never)).toBeNull();
  } finally { client.clear(); }
});
