import { afterEach, describe, expect, it, vi } from "vitest";
const get = vi.hoisted(() => vi.fn());
const post = vi.hoisted(() => vi.fn());
vi.mock("./client", () => ({createApiClient: () => ({get,post})}));
vi.mock("./runtime", () => ({getAuthHeaderName: () => "Authorization", getAuthToken: () => "test-admin-token", getRequestBaseUrl: () => "/gw"}));
import { refreshSSOMetadata, readSSOMetadata } from "./enterprise";

afterEach(() => vi.resetAllMocks());

describe("SAML metadata admin contract", () => {
  // 前置管理员接口返回XML；验证配置ID编码、鉴权和原文保持，纯mock无需外部清理。
  it("reads saved XML through the authenticated metadata endpoint", async () => {
    const xml = '<EntityDescriptor entityID="urn:original"/>';
    get.mockResolvedValue({metadata: xml});
    expect(await readSSOMetadata("id/with space")).toBe(xml);
    expect(get).toHaveBeenCalledWith("/admin/enterprise/sso/id%2Fwith%20space/metadata", {accessToken:"test-admin-token"});
  });
  // 前置空或非法接口形状；验证拒绝下载伪XML，afterEach清理mock，不发送网络。
  it.each([{}, {metadata:""}, {metadata:"   "}, {metadata:42}])("rejects missing metadata: %j", async (result) => {
    get.mockResolvedValue(result);
    await expect(readSSOMetadata("saved")).rejects.toThrow("Invalid SAML metadata response");
  });
  // 前置鉴权/网络失败；验证原错误交给表单展示固定翻译，不伪造下载成功。
  it("propagates API failure", async () => {
    get.mockRejectedValue(new Error("denied"));
    await expect(readSSOMetadata("saved")).rejects.toThrow("denied");
  });
});

// 管理员立即刷新使用编码连接 ID 和真实认证入口；接口失败原样抛出，不重复请求，mock 在用例后清理。
it("refreshes metadata through authenticated POST without retries", async () => {
 post.mockResolvedValue({last_success:1});
 expect(await refreshSSOMetadata("id/space here")).toEqual({last_success:1});
 expect(post).toHaveBeenCalledWith("/admin/enterprise/sso/id%2Fspace%20here/refresh",{accessToken:"test-admin-token",body:{}});
 post.mockRejectedValue(new Error("refresh failed"));
 await expect(refreshSSOMetadata("saved")).rejects.toThrow("refresh failed");
 expect(post).toHaveBeenCalledTimes(2);
});
