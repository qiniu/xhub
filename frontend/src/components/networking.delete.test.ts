import { afterEach, describe, expect, it, vi } from "vitest";
import { userDeleteCall, teamDeleteCall } from "./networking";

/** 删除运输契约：仅替换fetch，运行真实请求构造；每例恢复全局状态，不创建持久数据。 */
describe("single resource deletion contract", () => {
  const originalFetch = global.fetch;
  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });
  for (const [kind, call] of [
    ["user", userDeleteCall],
    ["team", teamDeleteCall],
  ] as const) {
    /** 普通和空ID均按单值字段交给后台校验，不能转成数组或扩大删除范围；清理由afterEach完成。 */
    for (const id of ["synthetic-id", ""])
      it(`${kind} sends singular ID ${id}`, async () => {
        const fetcher = vi.fn().mockResolvedValue(new Response('{"deleted":true}', { status: 200 }));
        global.fetch = fetcher;
        expect(await call("session", id)).toEqual({ deleted: true });
        expect(fetcher).toHaveBeenCalledOnce();
        const [url, init] = fetcher.mock.calls[0];
        expect(new URL(url, "http://test.local").pathname).toBe(`/gw/${kind}/delete`);
        expect(JSON.parse(init.body)).toEqual({ [`${kind}_id`]: id });
      });
    /** 后台拒绝删除必须传给页面显示失败，不能返回假成功；无数据库数据，恢复fetch及日志替身。 */
    it(`${kind} preserves rejection`, async () => {
      vi.spyOn(console, "error").mockImplementation(() => undefined);
      global.fetch = vi.fn().mockResolvedValue(new Response('{"error":{"message":"Forbidden"}}', { status: 403 }));
      await expect(call("session", "synthetic-id")).rejects.toMatchObject({ status: 403 });
    });
  }
});
