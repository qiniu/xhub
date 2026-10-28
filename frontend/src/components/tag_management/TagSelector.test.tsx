import userEvent from "@testing-library/user-event";
import { render, screen, cleanup, act } from "@testing-library/react";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import TagSelector from "./TagSelector";
import { tagListCall } from "../networking";
import { I18nProvider, translate } from "@/i18n";

const identity = vi.hoisted(() => ({ role: "Admin" }));
vi.mock("../networking", () => ({ tagListCall: vi.fn() }));
vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: () => ({ userRole: identity.role }) }));

/** 标签组件测试夹具每例清空语言存储和模拟读取；结束卸载组件，避免异步状态跨例污染。 */
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  document.cookie = "xhub_locale=; Max-Age=0; path=/";
  identity.role = "Admin";
  vi.mocked(tagListCall).mockResolvedValue({});
});
afterEach(cleanup);

describe("TagSelector", () => {
  for (const locale of ["en", "zh-CN"] as const) {
    /** 无真实数据库；分别验证双语错误、空结果及原文标签显示，读取替身只用于组件状态分层验证。 */
    it("shows actual read errors and keeps user text " + locale, async () => {
      vi.mocked(tagListCall).mockRejectedValueOnce(new Error("read failed"));
      const view = render(
        <I18nProvider initialLocale={locale}>
          <TagSelector onChange={() => {}} accessToken="first" />
        </I18nProvider>,
      );
      expect(await screen.findByRole("alert")).toHaveTextContent(translate(locale, "tagSelector.loadFailed"));
      vi.mocked(tagListCall).mockResolvedValueOnce({
        原名: { name: "原名", description: "用户说明", models: [], created_at: "", updated_at: "" },
      });
      view.rerender(
        <I18nProvider initialLocale={locale}>
          <TagSelector onChange={() => {}} accessToken="second" />
        </I18nProvider>,
      );
      await vi.waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
      const input = screen.getByRole("combobox", { name: translate(locale, "Select or create tags") });
      await vi.waitFor(() => expect(input).toBeEnabled());
      await userEvent.setup().click(input);
      expect(await screen.findByText("原名")).toBeInTheDocument();
      expect(screen.getByText("用户说明")).toBeInTheDocument();
    });
  }
  /** 普通用户保持既有禁止管理读取行为；允许输入自定义文本，不扩大角色权限，组件卸载即清理。 */
  it("does not read configuration for ordinary users", () => {
    identity.role = "Internal User";
    render(<TagSelector onChange={() => {}} accessToken="token" />);
    expect(tagListCall).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
  /** 空库是正常结果而非故障；测试等待实际异步读取完成，不把未调用当空列表成功。 */
  it("accepts an empty catalog", async () => {
    render(<TagSelector onChange={() => {}} accessToken="token" />);
    await vi.waitFor(() => expect(tagListCall).toHaveBeenCalledWith("token"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

/** 管理员读取未完成时切换普通身份，输入必须恢复且旧响应不能泄漏标签；组件替身仅控制时序，卸载清理。 */
it("releases pending loading when management identity is lost", async () => {
  let complete!: (value: Awaited<ReturnType<typeof tagListCall>>) => void;
  vi.mocked(tagListCall).mockReturnValueOnce(new Promise(resolve => { complete = resolve; }));
  const changed = vi.fn();
  const view = render(<TagSelector onChange={changed} accessToken="admin" />);
  const input = screen.getByRole("combobox");
  expect(input).toBeDisabled();
  identity.role = "Internal User";
  view.rerender(<TagSelector onChange={changed} accessToken="member" />);
  expect(input).toBeEnabled();
  await act(async () => { complete({ private: { name: "private", description: "", models: [], created_at: "", updated_at: "" } }); });
  expect(screen.queryByText("private")).not.toBeInTheDocument();
  await userEvent.setup().type(input, "custom");
  await userEvent.setup().click(await screen.findByRole("option", { name: /custom/ }));
  expect(changed).toHaveBeenCalledWith(["custom"]);
  expect(tagListCall).toHaveBeenCalledTimes(1);
});
