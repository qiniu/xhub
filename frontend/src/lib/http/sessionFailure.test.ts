import { describe, it, expect, vi } from "vitest";
import { createSessionFailureCheck } from "./sessionFailure";

/** 建立纯内存核验上下文；参数是模拟 fetch，返回检查器和可修改会话/网关状态；每例独立无需清理。 */
function fixture(fetchImpl: typeof fetch) {
  const state = { token: "session-a", base: "/gw" };
  const check = createSessionFailureCheck({
    getSessionToken: () => state.token,
    getSessionKey: () => "login-bearer",
    getBaseUrl: () => state.base,
    getHeaderName: () => "X-Session",
    fetchImpl,
  });
  return { state, check };
}
describe("登录会话与自定义Key错误隔离", () => {
  /** 有效会话、权限拒绝及依赖失败不能注销，只有明确401才失效；纯fetch替身，无外部副作用。 */
  it.each([200, 401, 403, 500, 502])("仅明确401标记失效：%s", async (status) => {
    const fetcher = vi.fn().mockResolvedValue({ status });
    const { check } = fixture(fetcher);
    expect(await check()).toBe(status === 401);
    expect(fetcher).toHaveBeenCalledWith("/gw/auth/me", {
      headers: { "X-Session": "Bearer login-bearer" },
      credentials: "omit",
      cache: "no-store",
    });
  });
  /** 网络中断不能证明会话过期；前置拒绝的fetch，结果false，无清理。 */
  it("网络错误保留会话", async () => {
    expect(await fixture(vi.fn().mockRejectedValue(new Error("offline"))).check()).toBe(false);
  });
  /** 无Cookie或缺少会话Key时不发送核验；空输入边界不触发外部调用，无清理。 */
  it("缺少会话不发送请求", async () => {
    const fetcher = vi.fn();
    for (const token of [null, "invalid"]) {
      const check = createSessionFailureCheck({
        getSessionToken: () => token,
        getSessionKey: () => null,
        getBaseUrl: () => "/gw",
        getHeaderName: () => "Authorization",
        fetchImpl: fetcher,
      });
      expect(await check()).toBe(false);
    }
    expect(fetcher).not.toHaveBeenCalled();
  });
  /** 同一会话并发核验合并，完成后不缓存结果；前置可控Promise，验证请求次数和重试，无外部资源。 */
  it("合并并发核验并释放结果", async () => {
    let resolve!: (v: { status: number }) => void;
    const fetcher = vi.fn().mockImplementation(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    );
    const { check } = fixture(fetcher);
    const a = check(),
      b = check();
    expect(fetcher).toHaveBeenCalledTimes(1);
    resolve({ status: 401 });
    expect(await a).toBe(true);
    expect(await b).toBe(true);
    fetcher.mockResolvedValue({ status: 200 });
    expect(await check()).toBe(false);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  /** 核验期间更换登录身份或网关，旧401不能影响新身份；前置可控Promise，无外部清理。 */
  it.each(["token", "base"] as const)("忽略已过时的%s核验", async (field) => {
    let resolve!: (v: { status: number }) => void;
    const { state, check } = fixture(
      vi.fn().mockImplementation(
        () =>
          new Promise((r) => {
            resolve = r;
          }),
      ),
    );
    const result = check();
    state[field] = "changed";
    resolve({ status: 401 });
    expect(await result).toBe(false);
  });
});
