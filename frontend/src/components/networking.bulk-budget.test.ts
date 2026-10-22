import { afterEach, describe, expect, it, vi } from "vitest";
import { userBulkUpdateUserCall } from "./networking";

/** 批量网络契约单元：只替代HTTP运输，不替代请求构造；每个用例结束恢复fetch，无持久数据。 */
describe("user bulk budget HTTP contract", () => {
  const originalFetch = global.fetch;
  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });
  for (const amount of [0, 12.5, null]) {
    /** 验证选定用户的零、普通额度和显式null逐项保留；前置成功HTTP响应，返回真实原子结果，不改变输入。 */
    it("sends explicit selected budgets " + amount, async () => {
      const result = {
        atomic: true,
        batch_id: "batch",
        results: [],
        total_requested: 2,
        successful_updates: 2,
        failed_updates: 0,
      };
      const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(result), { status: 200 }));
      global.fetch = fetcher;
      expect(await userBulkUpdateUserCall("session", { max_budget: amount }, ["first", "second"])).toEqual(result);
      expect(fetcher).toHaveBeenCalledOnce();
      const [url, init] = fetcher.mock.calls[0];
      expect(new URL(url, "http://test.local").pathname).toBe("/gw/user/bulk_update");
      expect(JSON.parse(init.body)).toEqual({
        users: [
          { user_id: "first", max_budget: amount },
          { user_id: "second", max_budget: amount },
        ],
      });
    });
  }
  /** 验证全部范围必须显式声明且不发送选定数组；前置成功HTTP替身，结束恢复fetch。 */
  it("sends explicit all-users command", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ atomic: true }), { status: 200 }));
    global.fetch = fetcher;
    await userBulkUpdateUserCall("session", { max_budget: null }, undefined, true);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ all_users: true, user_updates: { max_budget: null } });
  });
  /** 只设置刷新日期时不得序列化max_budget；真实请求构造配合HTTP替身，结束恢复fetch。 */
  it("sends a reset date without clearing the cap", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ atomic: true }), { status: 200 }));
    global.fetch = fetcher;
    await userBulkUpdateUserCall("session", { budget_reset_day: 0 }, ["first"]);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ users: [{ user_id: "first", budget_reset_day: 0 }] });
  });
  /** 验证空目标不会隐式扩大到全部；前置空数组，HTTP不应调用，console输出由测试恢复。 */
  it("rejects missing target before HTTP", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const fetcher = vi.fn();
    global.fetch = fetcher;
    await expect(userBulkUpdateUserCall("session", { max_budget: 1 }, [])).rejects.toThrow();
    expect(fetcher).not.toHaveBeenCalled();
  });
  /** 验证后台错误码保留给组件国际化；前置原子额度拒绝响应，结果应抛错而非伪造成功，结束恢复运输。 */
  it("preserves atomic allocation rejection", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const body = { error: { type: "bulk_budget_allocation", message: "Rejected batch" } };
    global.fetch = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify(body), { status: 400, statusText: "Bad Request" }));
    await expect(userBulkUpdateUserCall("session", { max_budget: 0 }, ["first"])).rejects.toMatchObject({
      status: 400,
      body,
    });
  });
});
