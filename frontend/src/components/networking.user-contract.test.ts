import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { userBulkUpdateUserCall, userCreateCall, userDeleteCall, userGetInfoV2, userListCall, userUpdateUserCall } from "./networking";

// 只验证客户端请求组装及错误传播；真实权限/事务/计费另由PG回归和浏览器E2E验证，不把fetch替身当业务成功证据。
vi.mock("@/utils/cookieUtils", () => ({ getCookie: vi.fn(), clearTokenCookies: vi.fn(), storeLoginToken: vi.fn() }));
const fetcher = vi.fn();
beforeEach(() => { vi.stubGlobal("fetch", fetcher); fetcher.mockReset(); });
afterEach(() => { vi.unstubAllGlobals(); });

/** 以HTTP形状替身回复，仅用于客户端单测；body/status为测试输入，无网络或持久化，afterEach恢复fetch。 */
function reply(body: unknown, status = 200) {
  fetcher.mockResolvedValue(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
}

/** 读取最近请求JSON供契约断言；无参数，缺调用时失败，不修改表单或服务器。 */
function sent() { return JSON.parse(fetcher.mock.calls.at(-1)![1].body); }

describe("user contract client boundaries", () => {
  /** 前置单人更新回复，验证字段省略、null、0和兼容字符串未经转换；每例恢复fetch，无待清理记录。 */
  it.each([undefined, null, 0, "", "500"])("preserves single-user budget %s and real envelope", async (budget) => {
    const fields = budget === undefined ? { user_id: "u", user_alias: "原始名字" } : { user_id: "u", max_budget: budget };
    const snapshot = { user_info: { user_id: "u", max_budget: budget ?? null } };
    reply(snapshot);
    expect(await userUpdateUserCall("session", fields, null)).toEqual(snapshot);
    expect(sent()).toEqual(fields);
    expect(fetcher.mock.calls[0][1].headers).toMatchObject({ Authorization: "Bearer session" });
  });

  /** 前置批量成功形状，验证选定和全部模式只发送对应字段，不能把未填预算变0；恢复fetch，无服务端数据。 */
  it("preserves selected/all budget commands and reset-only omission", async () => {
    const result = { atomic: true, batch_id: "batch", results: [], total_requested: 0, successful_updates: 0, failed_updates: 0 };
    reply(result);
    expect(await userBulkUpdateUserCall("session", { budget_reset_day: 0 }, ["u"])).toEqual(result);
    expect(sent()).toEqual({ users: [{ user_id: "u", budget_reset_day: 0 }] });
    reply(result);
    await userBulkUpdateUserCall("session", { max_budget: null }, undefined, true);
    expect(sent()).toEqual({ all_users: true, user_updates: { max_budget: null } });
  });

  /** 前置无目标批量请求，应在HTTP前失败，不默认为全员；恢复fetch，无数据清理。 */
  it("rejects missing bulk targets without HTTP", async () => {
    await expect(userBulkUpdateUserCall("session", { max_budget: 0 })).rejects.toThrow();
    expect(fetcher).not.toHaveBeenCalled();
  });

  /** 前置真实失败信封的替身，校验403/409/500仍抛出而不是转为成功结果；每次重新创建Response，无持久化。 */
  it.each([403, 409, 500])("propagates HTTP %s for update and bulk", async (status) => {
    reply({ error: { code: String(status), message: "Fixture failure", type: "fixture", param: null } }, status);
    await expect(userUpdateUserCall("session", { user_id: "u", max_budget: 1 }, null)).rejects.toMatchObject({ status });
    reply({ error: { code: String(status), message: "Fixture failure", type: "fixture", param: null } }, status);
    await expect(userBulkUpdateUserCall("session", { max_budget: 1 }, ["u"])).rejects.toMatchObject({ status });
  });

  /** 前置详情缺少信封或真实形状失败回复，客户端必须抛错；覆盖读/创建/删除失败，不改变权限，恢复fetch。 */
  it("rejects missing detail and read/create/delete failures", async () => {
    reply({});
    await expect(userGetInfoV2("session", "u")).rejects.toThrow();
    const failure = { error: { code: "500", message: "Fixture storage failure", type: "storage_error", param: null } };
    reply(failure, 500);
    await expect(userListCall("session")).rejects.toMatchObject({ status: 500 });
    reply(failure, 500);
    await expect(userGetInfoV2("session", "u")).rejects.toMatchObject({ status: 500 });
    reply(failure, 500);
    await expect(userDeleteCall("session", "u")).rejects.toMatchObject({ status: 500 });
    reply(failure, 500);
    await expect(userCreateCall("session", null, { user_email: "example@example.invalid", password: "Example-password" })).rejects.toThrow("Fixture storage failure");
  });

  /** 前置列表/详情/创建/删除回复，验证URL筛选和返回裸用户/详情解包/删除信封保持；恢复fetch，无业务数据。 */
  it("keeps read, create and delete wire shapes", async () => {
    const user = { user_id: "u", teams: [{ team_id: "t", team_alias: "Team", user_role: "user", organization_id: "o" }] };
    reply({ user_info: user });
    expect(await userGetInfoV2("session", "u")).toEqual(user);
    expect(fetcher.mock.calls.at(-1)![0]).toContain("/v2/user/info?user_id=u");
    reply({ users: [user], total: 1, page: 1, page_size: 10, total_pages: 1 });
    expect((await userListCall("session", ["u"], 1, 10)).users).toEqual([user]);
    expect(fetcher.mock.calls.at(-1)![0]).toContain("user_ids=u");
    reply(user);
    expect(await userCreateCall("session", null, { user_email: "example@example.invalid", password: "Example-password", max_budget: 0 })).toEqual(user);
    expect(sent()).toMatchObject({ auto_create_key: false, max_budget: 0 });
    reply({ deleted: true });
    expect(await userDeleteCall("session", "u")).toEqual({ deleted: true });
    expect(sent()).toEqual({ user_id: "u" });
  });
});
