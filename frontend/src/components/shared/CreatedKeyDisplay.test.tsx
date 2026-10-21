import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@/i18n/I18nProvider";
import LanguageSwitcher from "@/components/LanguageSwitcher";
import { translate } from "@/i18n/translate";
import CreatedKeyDisplay from "./CreatedKeyDisplay";

import { toast } from "@/lib/toast";

vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: vi.fn() }) }));

describe("CreatedKeyDisplay", () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("should render", () => {
    render(<CreatedKeyDisplay apiKey="sk-test-123" />);
    expect(screen.getByText("sk-test-123")).toBeInTheDocument();
  });

  it("should theme the key box with tokens instead of a hardcoded light background", () => {
    render(<CreatedKeyDisplay apiKey="sk-test-123" />);

    const keyBox = screen.getByText("sk-test-123").parentElement as HTMLElement;

    expect(keyBox).not.toHaveAttribute("style");
    expect(keyBox).toHaveClass("bg-muted");
  });

  it("should display the security warning", () => {
    render(<CreatedKeyDisplay apiKey="sk-test-123" />);
    expect(screen.getByText(/you will not be able to view it again/i)).toBeInTheDocument();
  });

  it("should show the copy button with initial label", () => {
    render(<CreatedKeyDisplay apiKey="sk-test-123" />);
    expect(screen.getByRole("button", { name: /copy virtual key/i })).toBeInTheDocument();
  });

  it("should change button text to Copied after clicking copy", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<CreatedKeyDisplay apiKey="sk-test-123" />);

    await user.click(screen.getByRole("button", { name: /copy virtual key/i }));

    expect(screen.getByRole("button", { name: /copied/i })).toBeInTheDocument();
  });

  it("should show a success message when the key is copied", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<CreatedKeyDisplay apiKey="sk-test-123" />);

    await user.click(screen.getByRole("button", { name: /copy virtual key/i }));

    expect(toast.success).toHaveBeenCalledWith("Key copied to clipboard");
  });

  it("should revert button text back after 2 seconds", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<CreatedKeyDisplay apiKey="sk-test-123" />);

    await user.click(screen.getByRole("button", { name: /copy virtual key/i }));
    expect(screen.getByRole("button", { name: /copied/i })).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(2000);
    });

    expect(screen.getByRole("button", { name: /copy virtual key/i })).toBeInTheDocument();
  });
});

// 验证实际中英文切换后的完整保存提示与复制按钮，密钥包含品牌词也不改写；组件卸载及存储清理由测试负责。
it("switches key guidance without rewriting the secret", async () => {
  localStorage.clear();
  document.cookie = "xhub_locale=zh-CN; path=/";
  const user = userEvent.setup();
  const secret = "sk-LiteLLM-原始-value";
  const view = render(<I18nProvider initialLocale="zh-CN"><LanguageSwitcher /><CreatedKeyDisplay apiKey={secret} /></I18nProvider>);
  expect(screen.getByText(translate("zh-CN", "createdKey.warning"))).toBeInTheDocument();
  expect(screen.getByRole("button", {name: translate("zh-CN", "Copy Virtual Key")})).toBeInTheDocument();
  await user.click(screen.getByRole("button", {name: "English", exact: true}));
  expect(screen.getByText(translate("en", "createdKey.warning"))).toBeInTheDocument();
  expect(screen.getByText(secret)).toBeInTheDocument();
  expect(screen.getByRole("button", {name: "Copy Virtual Key"})).toBeInTheDocument();
  view.unmount(); localStorage.clear(); document.cookie = "xhub_locale=; path=/; max-age=0";
});
