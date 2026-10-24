import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { modelDeleteCall, modelPatchUpdateCall, testConnectionRequest } from "./networking";
vi.mock("@/utils/cookieUtils", () => ({ getCookie: vi.fn(), clearTokenCookies: vi.fn(), storeLoginToken: vi.fn() }));
const fetcher = vi.fn();
beforeEach(() => { vi.stubGlobal("fetch", fetcher); fetcher.mockReset(); });
afterEach(() => { vi.unstubAllGlobals(); });
/** 返回客户端HTTP形状替身，验证组装与错误传播；不模拟业务可用性，afterEach恢复fetch。 */
function reply(body: unknown, status = 200) {
  fetcher.mockResolvedValue(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
}
describe("model contract clients", () => {
  /** 补丁省略/null/零、原始名字及扩展必须原样发送，回复保留部署而非错误信封；无数据库清理。 */
  it("preserves patch and response", async () => {
    const fields = { model_name: "原始名字", litellm_params: { input_cost_per_token: 0, custom: null }, model_info: { disabled: false } };
    const result = { model_name: "原始名字", litellm_params: fields.litellm_params, model_info: { id: "m" } };
    reply(result);
    expect(await modelPatchUpdateCall("session", fields, "m")).toEqual(result);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual(fields);
    expect(fetcher.mock.calls[0][1].method).toBe("PATCH");
  });
  /** HTTP200业务失败必须原样传给页面，不能转换成成功；纯客户端测试无外部清理。 */
  it("retains unhealthy probe despite HTTP 200", async () => {
    const result = { status: "error", healthy: false, error: "upstream_http_error", message: "Failed" };
    reply(result);
    expect(await testConnectionRequest("session", {}, { id: "m" }, "chat")).toEqual(result);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ litellm_params: {}, model_info: { id: "m" }, mode: "chat" });
  });
  /** 删除及补丁的真实HTTP失败必须向调用者传播；各状态使用新Response，结束恢复fetch。 */
  it.each([403, 409, 500])("propagates write failure %s", async (status) => {
    const body = { error: { code: String(status), message: "Fixture failure", type: "fixture", param: null } };
    reply(body, status);
    await expect(modelDeleteCall("session", "m")).rejects.toMatchObject({ status });
    reply(body, status);
    await expect(modelPatchUpdateCall("session", {}, "m")).rejects.toThrow();
  });
});
