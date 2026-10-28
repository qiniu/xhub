import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { CacheManagement } from "./CacheManagement";
import { translate, type Locale } from "@/i18n/translate";

const state = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), locale: "en" as Locale }));
vi.mock("@/components/networking", () => ({ apiClient: { get: state.get, post: state.post } }));
vi.mock("@/i18n", () => ({ useT: () => (key: string) => translate(state.locale, key) }));
const ready = { enabled: true, cache_type: "memory", scope: "process", configurable: false };

/** 缓存视图单元测试：纯mock隔离请求，覆盖双语正常/坏响应/失败恢复；每例清理DOM和mock，不代替真实E2E。 */
describe("CacheManagement", () => {
  beforeEach(() => { cleanup(); vi.resetAllMocks(); state.locale = "en"; state.get.mockResolvedValue(ready); state.post.mockResolvedValue({ status: "ok", message: "cache flushed" }); });

  /** 已就绪状态下确认清理契约并切换语言；无外部资源，beforeEach回收DOM。 */
  it.each(["en", "zh-CN"] as const)("shows actual scope and confirmed flush in %s", async (locale) => {
    state.locale = locale;
    const tr = (key: string) => translate(locale, "cacheAdmin." + key);
    const view = render(<CacheManagement accessToken="session" />);
    expect(await screen.findByText(tr("ready"))).toBeVisible();
    expect(screen.getByText(tr("unsupported"))).toBeVisible();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: tr("flush") }));
    expect(await screen.findByText(tr("flushed"))).toBeVisible();
    expect(state.post).toHaveBeenCalledWith("/flushall", { accessToken: "session", body: {} });
    state.locale = locale === "en" ? "zh-CN" : "en";
    view.rerender(<CacheManagement accessToken="session" />);
    expect(screen.getByText(translate(state.locale, "cacheAdmin.flushed"))).toBeVisible();
    expect(screen.queryByText(tr("flushed"))).not.toBeInTheDocument();
  });

  /** 查询失败或旧配置不能报告可用；刷新用正常响应恢复，清理DOM，无外部副作用。 */
  it.each(["network", "legacy", "null"])("rejects %s status then recovers", async (mode) => {
    if (mode === "network") state.get.mockRejectedValueOnce(new Error("private detail"));
    else state.get.mockResolvedValueOnce(mode === "null" ? null : { type: "redis", enabled: true });
    render(<CacheManagement accessToken="session" />);
    expect(await screen.findByRole("alert")).toHaveTextContent(translate("en", "cacheAdmin.loadFailed"));
    expect(screen.getByRole("button", { name: translate("en", "cacheAdmin.flush") })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: translate("en", "cacheAdmin.refresh") }));
    expect(await screen.findByText(translate("en", "cacheAdmin.ready"))).toBeVisible();
  });

  /** 清理网络失败和伪成功响应必须提示失败，再次正常操作可恢复；不持有外部资源。 */
  it.each(["network", "unexpected", "null"])("rejects %s flush result then retries", async (mode) => {
    if (mode === "network") state.post.mockRejectedValueOnce(new Error("private detail"));
    else state.post.mockResolvedValueOnce(mode === "null" ? null : { status: "success" });
    render(<CacheManagement accessToken="session" />);
    await screen.findByText(translate("en", "cacheAdmin.ready"));
    fireEvent.click(screen.getByRole("button", { name: translate("en", "cacheAdmin.flush") }));
    expect(await screen.findByRole("alert")).toHaveTextContent(translate("en", "cacheAdmin.flushFailed"));
    expect(screen.queryByText("private detail")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: translate("en", "cacheAdmin.flush") }));
    expect(await screen.findByText(translate("en", "cacheAdmin.flushed"))).toBeVisible();
  });

  /** 缺少会话时不查询或开放清理；纯渲染，无网络清理。 */
  it("does not call without a session", () => {
    render(<CacheManagement accessToken={null} />);
    expect(state.get).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: translate("en", "cacheAdmin.flush") })).toBeDisabled();
  });
});
