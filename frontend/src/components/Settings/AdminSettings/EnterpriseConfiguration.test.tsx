import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/i18n";
import * as api from "@/lib/http/enterprise";
import EnterpriseConfiguration from "./EnterpriseConfiguration";
// 独立刷新子表单由 SAMLRefresh.test.tsx 验证，避免父表单测试混入调度读取。
vi.mock("./SAMLRefresh", () => ({default: () => null}));
vi.mock("@/lib/http/enterprise");
describe("EnterpriseConfiguration", () => {
 beforeEach(() => { vi.resetAllMocks(); localStorage.clear(); document.cookie = "xhub_locale=; max-age=0; path=/"; });
 afterEach(cleanup);
 // 前置中文 OIDC 表单；验证平台模板只改变稳定身份声明，不覆盖用户 issuer 或发送平台字段，清理组件与 mock。
 it("平台下拉辅助声明选择且保留实际租户地址", () => {
  render(<I18nProvider initialLocale="zh-CN"><EnterpriseConfiguration id="connection" /></I18nProvider>);
  fireEvent.change(screen.getByLabelText("单点登录名称"), {target:{value:"Original Login"}});
  fireEvent.change(screen.getByLabelText("OIDC 签发者"), {target:{value:"https://identity.example.com/tenant"}});
  fireEvent.change(screen.getByLabelText("身份平台配置模板"), {target:{value:"entra"}});
  expect(screen.getByLabelText("稳定身份声明")).toHaveValue("oid");
  fireEvent.change(screen.getByLabelText("身份平台配置模板"), {target:{value:"okta"}});
  expect(screen.getByLabelText("稳定身份声明")).toHaveValue("sub");
  expect(screen.getByLabelText("OIDC 签发者")).toHaveValue("https://identity.example.com/tenant");
 });
 // 前置中文管理员表单与模拟接口；验证 OIDC 原始输入和密码清空，清理组件及 mock，无真实写入。
 it("保存 SSO 后清空秘密且保留原始输入", async () => {
  vi.mocked(api.saveEnterprise).mockResolvedValue();
  render(<I18nProvider initialLocale="zh-CN"><EnterpriseConfiguration id="connection" /></I18nProvider>);
  fireEvent.change(screen.getByLabelText("单点登录名称"), {target:{value:"Original Login"}});
  fireEvent.change(screen.getByLabelText("OIDC 签发者"), { target: { value: "https://identity.example.com" } });
  fireEvent.change(screen.getByLabelText("客户端密钥"), { target: { value: "secret" } });
  fireEvent.click(screen.getByRole("switch", {name: "启用集成"}));
  fireEvent.click(screen.getByRole("button", { name: "保存集成配置" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("操作已完成"));
  expect(screen.getByLabelText("客户端密钥")).toHaveValue("");
  expect(screen.getByLabelText("OIDC 签发者")).toHaveValue("https://identity.example.com");
  expect(api.saveEnterprise).toHaveBeenCalledWith("/admin/enterprise/sso/connection", expect.objectContaining({ client_secret: "secret", issuer: "https://identity.example.com", identity_claim: "sub", enabled: true }));
 });
 // 前置英文配置及队列接口；验证动态状态、英文失败与探针操作，清理 mock 和组件。
 it("英文队列状态与失败提示完整翻译", async () => {
  vi.mocked(api.readEnterprise).mockResolvedValue({ configuration: { bucket: "billing-bucket", region: "z0", prefix: "billing", interval_seconds: 300, batch_size: 50, enabled: true }, counts: { failed: 2, done: 3 }, events: [{ id: 1, state: "failed", object_key: "billing/1", error: "archive_upload_failed" }] });
  vi.mocked(api.archiveAction).mockRejectedValue(new Error("sensitive provider message"));
  render(<I18nProvider initialLocale="en"><EnterpriseConfiguration /></I18nProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Load configuration and status" }));
  await waitFor(() => expect(screen.getByText("Pending 0 · Processing 0 · Failed 2 · Archived 3")).toBeVisible());
  expect(screen.getByLabelText("Kodo bucket")).toHaveValue("billing-bucket");
  expect(screen.getByLabelText("Archive processing interval")).toHaveValue("300");
  expect(screen.getByLabelText("Maximum events per cycle")).toHaveValue(50);
  fireEvent.change(screen.getByLabelText("Archive processing interval"), {target:{value:"60"}});
  fireEvent.change(screen.getByLabelText("Maximum events per cycle"), {target:{value:"200"}});
  expect(screen.getByText(/Billing metadata only/)).not.toBeVisible();
  fireEvent.click(screen.getByText("Setup guide"));
  expect(screen.getByText(/Billing metadata only/)).toBeVisible();
  expect(screen.getByLabelText("Kodo region")).toHaveValue("z0");
  fireEvent.change(screen.getByLabelText("Kodo region"), {target:{value:"z1"}});
  vi.mocked(api.saveEnterprise).mockResolvedValue();
  fireEvent.click(screen.getByRole("button", {name:"Save integration"}));
  await waitFor(() => expect(api.saveEnterprise).toHaveBeenCalledWith("/admin/archive/kodo", expect.objectContaining({region:"z1", interval_seconds:60, batch_size:200})));
  await waitFor(() => expect(screen.getByRole("button", { name: "Test Kodo connection" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Test Kodo connection" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Operation failed"));
  expect(screen.queryByText("sensitive provider message")).not.toBeInTheDocument();
 });
 // 前置为公开源及已有表单；验证明确生成地址、不修改 issuer，以及未配置源时按钮禁用，无网络写入。
 it("按公开源显式生成回调地址", () => {
  const {unmount} = render(<I18nProvider initialLocale="zh-CN"><EnterpriseConfiguration id="connection" publicOrigin="https://console.example.com" /></I18nProvider>);
  fireEvent.change(screen.getByLabelText("OIDC 签发者"), {target:{value:"https://identity.example.com/tenant"}});
  fireEvent.click(screen.getByRole("button", {name:"生成回调和返回地址"}));
  expect(screen.getByLabelText("回调地址")).toHaveValue("https://console.example.com/auth/enterprise/callback");
  expect(screen.getByLabelText("控制台返回地址")).toHaveValue("https://console.example.com/ui/login");
  expect(screen.getByLabelText("OIDC 签发者")).toHaveValue("https://identity.example.com/tenant");
  unmount();
  render(<I18nProvider initialLocale="en"><EnterpriseConfiguration id="connection" /></I18nProvider>);
  expect(screen.getByRole("button", {name:"Generate callback and return URLs"})).toBeDisabled();
 });
 // 前置为未配置的中文归档表单；验证默认调度、数值空值与上限校验、动态路径保留原始输入；清理 DOM，无网络写入。
 it("归档调度默认值与数值边界可观察", () => {
  render(<I18nProvider initialLocale="zh-CN"><EnterpriseConfiguration /></I18nProvider>);
  expect(screen.getByLabelText("归档处理间隔")).toHaveValue("2");
  const batch = screen.getByLabelText("每轮最多处理事件数") as HTMLInputElement;
  expect(batch).toHaveValue(20);
  fireEvent.change(batch, {target:{value:""}});
  expect(batch.checkValidity()).toBe(false);
  fireEvent.change(batch, {target:{value:"201"}});
  expect(batch.checkValidity()).toBe(false);
  fireEvent.change(batch, {target:{value:"1"}});
  expect(batch.checkValidity()).toBe(true);
  fireEvent.change(screen.getByLabelText("对象前缀"), {target:{value:"custom/path"}});
  expect(screen.getByText(/custom\/path\/v/)).toBeVisible();
 });
});
