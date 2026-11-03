import { teamDailyActivityAggregatedCall } from "./networking";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  userDailyActivityCall,
  testConnectionRequest,
  modelInfoCall,
  modelPatchUpdateCall,
  regenerateKeyCall,
  uiSpendLogsCall,
  uiSpendLogDetailsCall,
  sessionSpendLogsCall,
  userSetPasswordCall,
  userDailyActivityAggregatedCall,
} from "./networking";
vi.mock("@/utils/cookieUtils", () => ({ getCookie: vi.fn(), clearTokenCookies: vi.fn(), storeLoginToken: vi.fn() }));
const fetcher = vi.fn();
beforeEach(() => {
  vi.stubGlobal("fetch", fetcher);
  fetcher.mockReset();
});
afterEach(() => {
  vi.unstubAllGlobals();
});
/** 提供确定性HTTP回复，仅测试序列化和错误保留，不计入真实业务验证；每例恢复全局fetch。 */
function reply(body: unknown, status = 200) {
  fetcher.mockResolvedValue(
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
  );
}
const calls = [
  () => teamDailyActivityAggregatedCall("session", new Date(2026, 0, 1), new Date(2026, 0, 2), ["team-a", "team-b"]),
  () => userDailyActivityCall("session", new Date(2026, 0, 1), new Date(2026, 0, 2)),
  () => testConnectionRequest("session", {}, {}, "chat"),
  () => modelInfoCall("session", "user", "user"),
  () => modelPatchUpdateCall("session", {}, "model/id"),
  () => regenerateKeyCall("session", "key/id", {}),
  () => uiSpendLogsCall({ accessToken: "session", start_date: "2026-01-01", end_date: "2026-01-02" }),
  () => uiSpendLogDetailsCall("session", "log/id", "2026-01-01"),
  () => sessionSpendLogsCall("session", "session/id"),
  () => userSetPasswordCall("session", "user", "Contract-password"),
  () => userDailyActivityAggregatedCall("session", new Date(2026, 0, 1), new Date(2026, 0, 2)),
];
describe("core contract client errors", () => {
  /** 团队汇总传递团队筛选和日期，保留真实统计及未知字段；纯HTTP序列化测试，afterEach恢复fetch。 */
  it("preserves team aggregation contract and filters", async () => {
    const body = { results: [], metadata: { total_api_requests: 2, total_spend: 0.25 }, future: { original: true } };
    reply(body);
    expect(await teamDailyActivityAggregatedCall("s", new Date(2026, 0, 1), new Date(2026, 0, 2), ["team-a", "team-b"])).toEqual(body);
    const url = new URL(String(fetcher.mock.calls.at(-1)![0]), "http://test");
    expect(url.searchParams.get("team_ids")).toBe("team-a,team-b");
    expect(url.searchParams.get("exclude_team_ids")).toBe("litellm-dashboard");
    expect(url.searchParams.get("start_date")).toBe("2026-01-01");
    reply(body);
    await teamDailyActivityAggregatedCall("s", new Date(2026, 0, 1), new Date(2026, 0, 2), []);
    expect(new URL(String(fetcher.mock.calls.at(-1)![0]), "http://test").searchParams.has("team_ids")).toBe(false);
  });

  /** 所有迁移客户端保留400/401/403/404/500状态与原文，尤其权限拒绝不得伪装成功空列表；无外部资源。 */
  it.each([400, 401, 403, 404, 500])("preserves status and body %s", async (status) => {
    const body = {
      error: {
        code: String(status),
        message: "Not allowed to access management endpoints",
        type: "access_denied",
        param: null,
      },
    };
    for (const call of calls) {
      reply(body, status);
      await expect(call()).rejects.toMatchObject({ status, body });
    }
  });
  /** 网络失败不得转换为默认数据或成功；遍历公共客户端入口，无后台副作用。 */
  it("propagates transport failure", async () => {
    fetcher.mockRejectedValue(new TypeError("Failed to fetch"));
    for (const call of calls) await expect(call()).rejects.toThrow("Failed to fetch");
  });
  /** ID编码、查询范围、未知正文及可空指标保持原值；不猜测缺失缓存指标，结束恢复fetch。 */
  it("retains raw bodies and query scope", async () => {
    const body = {
      request_id: "log/id",
      messages: [{ role: "user", content: "用户原文" }],
      response: "upstream non-json",
      ttft_ms: null,
    };
    reply(body);
    expect(await uiSpendLogDetailsCall("s", "log/id", "2026-01-01")).toEqual(body);
    expect(String(fetcher.mock.calls[0][0])).toContain("/spend/logs/ui/log%2Fid?start_date=2026-01-01");
    reply({ data: [], total: 0, page: 2, page_size: 10, total_pages: 0 });
    await sessionSpendLogsCall("s", "会话", 2, 10, { apiKey: "key", userId: "ignored" });
    const url = new URL(String(fetcher.mock.calls.at(-1)![0]), "http://test");
    expect(url.searchParams.get("session_id")).toBe("会话");
    expect(url.searchParams.get("api_key")).toBe("key");
    expect(url.searchParams.has("user_id")).toBe(false);
    reply({ password_updated: true, user_id: "u" });
    await userSetPasswordCall("s", "u", "Contract-password");
    expect(JSON.parse(fetcher.mock.calls.at(-1)![1].body)).toEqual({ user_id: "u", password: "Contract-password" });
    reply({ results: [], metadata: {} });
    await userDailyActivityAggregatedCall("s", new Date(2026, 0, 1), new Date(2026, 0, 2), "", false, "");
    const filtered = new URL(String(fetcher.mock.calls.at(-1)![0]), "http://test");
    expect(filtered.searchParams.get("user_id")).toBe("");
    expect(filtered.searchParams.get("api_key")).toBe("");
  });
});
