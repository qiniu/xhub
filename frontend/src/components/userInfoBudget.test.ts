import { afterEach, expect, it, vi } from "vitest";
import { apiClient, userGetInfoV2 } from "./networking";
afterEach(() => vi.restoreAllMocks());
// 验证真实 HTTP 客户端边界解包，预算 500/0/null 不丢失；spy 每例恢复，无外部请求。
it.each([500, 0, null])("unwraps user budget %s", async (max_budget) => {
  const user = { user_id: "u", max_budget, spend: 0 };
  vi.spyOn(apiClient, "get").mockResolvedValue({ user_info: user });
  expect(await userGetInfoV2("test-token")).toEqual(user);
});
// 协议缺失及接口失败不能伪装成无限额；无持久化，恢复 spy 清理。
it("rejects missing user or network failures", async () => {
  const get = vi.spyOn(apiClient, "get").mockResolvedValue({});
  await expect(userGetInfoV2("test-token")).rejects.toThrow();
  get.mockRejectedValue(new Error("network failure"));
  await expect(userGetInfoV2("test-token")).rejects.toThrow("network failure");
});
