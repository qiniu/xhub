import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider, translate } from "@/i18n";
import { getPersonalLogCapture, setPersonalLogCapture } from "@/components/networking";
import { PersonalLogCapture } from "./PersonalLogCapture";

vi.mock("@/components/networking", () => ({ getPersonalLogCapture: vi.fn(), setPersonalLogCapture: vi.fn() }));
const initial = { enabled: false, global_enabled: false, effective_enabled: false, retention_days: 30, global_retention_days: 30, used_bytes: 0, limit_bytes: 1048576 };

afterEach(() => { vi.clearAllMocks(); });

describe("PersonalLogCapture", () => {
  // 验证中英文开关会提交自己的选择，动态显示期限；独立查询客户端与 DOM 在用例结束清理。
  it.each(["zh-CN", "en"] as const)("updates the personal choice in %s", async (locale) => {
    vi.mocked(getPersonalLogCapture).mockResolvedValue(initial);
    vi.mocked(setPersonalLogCapture).mockResolvedValue({ ...initial, enabled: true, effective_enabled: true });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<I18nProvider initialLocale={locale}><QueryClientProvider client={client}><PersonalLogCapture accessToken="session" /></QueryClientProvider></I18nProvider>);
    try {
      const toggle = await screen.findByRole("switch", { name: translate(locale, "logsUX.captureTitle") });
      expect(toggle).not.toBeChecked();
      expect(screen.getByText(translate(locale, "logsUX.captureDescription", { days: 30 }))).toBeInTheDocument();
      fireEvent.click(toggle);
      await waitFor(() => expect(setPersonalLogCapture).toHaveBeenCalledWith("session", true));
      await waitFor(() => expect(toggle).toBeChecked());
    } finally { view.unmount(); client.clear(); }
  });

  // 全局优先时即使个人关闭也必须显示说明；不请求真实外部服务，卸载后清理查询缓存。
  it("explains the global override", async () => {
    vi.mocked(getPersonalLogCapture).mockResolvedValue({ ...initial, global_enabled: true, effective_enabled: true });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<I18nProvider initialLocale="en"><QueryClientProvider client={client}><PersonalLogCapture accessToken="session" /></QueryClientProvider></I18nProvider>);
    try { expect(await screen.findByText(translate("en", "logsUX.captureGlobal", { days: 30 }))).toBeVisible(); }
    finally { view.unmount(); client.clear(); }
  });

  // 后台读取失败要提示重试，不把错误显示成开关关闭；模拟请求失败，卸载后清理缓存。
  it("shows an actionable error when settings cannot be loaded", async () => {
    vi.mocked(getPersonalLogCapture).mockRejectedValue(new Error("unavailable"));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<I18nProvider initialLocale="en"><QueryClientProvider client={client}><PersonalLogCapture accessToken="session" /></QueryClientProvider></I18nProvider>);
    try { expect(await screen.findByRole("alert")).toHaveTextContent(translate("en", "logsUX.captureLoadFailed")); }
    finally { view.unmount(); client.clear(); }
  });

  // 更新失败时保留上一次有效状态并提示重试；仅模拟 HTTP 失败，查询缓存和组件在结束时清理。
  it("keeps the previous choice when saving fails", async () => {
    vi.mocked(getPersonalLogCapture).mockResolvedValue(initial);
    vi.mocked(setPersonalLogCapture).mockRejectedValue(new Error("write failed"));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const view = render(<I18nProvider initialLocale="en"><QueryClientProvider client={client}><PersonalLogCapture accessToken="session" /></QueryClientProvider></I18nProvider>);
    try {
      const toggle = await screen.findByRole("switch", { name: translate("en", "logsUX.captureTitle") });
      fireEvent.click(toggle);
      expect(await screen.findByRole("alert")).toHaveTextContent(translate("en", "logsUX.captureSaveFailed"));
      expect(toggle).not.toBeChecked();
    } finally { view.unmount(); client.clear(); }
  });
});
