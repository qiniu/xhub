import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/i18n";
import { SectionHeader } from "./SectionHeader";

describe("SectionHeader", () => {
  /** 前置输入摘要；验证完整数量、费用和轮次，组件自动卸载，无后台数据。 */
  it("renders the input label with token, cost and turn metrics", () => {
    render(<SectionHeader type="input" tokens={1234} cost={0.000123} turnCount={3} onCopy={vi.fn()} />);

    expect(screen.getByText("Input")).toBeInTheDocument();
    expect(screen.getByText("Tokens: 1,234")).toBeInTheDocument();
    expect(screen.getByText("Cost: $0.000123")).toBeInTheDocument();
    expect(screen.getByText("Turns: 3")).toBeInTheDocument();
  });

  it("renders the output label", () => {
    render(<SectionHeader type="output" onCopy={vi.fn()} />);

    expect(screen.getByText("Output")).toBeInTheDocument();
  });

  it("omits metrics that were not provided", () => {
    render(<SectionHeader type="input" onCopy={vi.fn()} />);

    expect(screen.queryByText(/^Tokens:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Cost:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Turns:/)).not.toBeInTheDocument();
  });

  it("omits the turn count when there are no turns", () => {
    render(<SectionHeader type="input" turnCount={0} onCopy={vi.fn()} />);

    expect(screen.queryByText(/^Turns:/)).not.toBeInTheDocument();
  });

  it("copies without toggling the section", async () => {
    const onCopy = vi.fn();
    const onToggleCollapse = vi.fn();
    render(<SectionHeader type="input" onCopy={onCopy} onToggleCollapse={onToggleCollapse} />);

    await userEvent.click(screen.getByRole("button", { name: /copy/i }));

    expect(onCopy).toHaveBeenCalledTimes(1);
    expect(onToggleCollapse).not.toHaveBeenCalled();
  });

  it("toggles the section when the header is clicked", async () => {
    const onToggleCollapse = vi.fn();
    render(<SectionHeader type="input" onCopy={vi.fn()} onToggleCollapse={onToggleCollapse} />);

    await userEvent.click(screen.getByText("Input"));

    expect(onToggleCollapse).toHaveBeenCalledTimes(1);
  });

  it("reports its collapsed state to assistive technology", () => {
    const { rerender } = render(
      <SectionHeader type="input" onCopy={vi.fn()} onToggleCollapse={vi.fn()} isCollapsed={false} />,
    );

    expect(screen.getByRole("button", { name: /^Input/ })).toHaveAttribute("aria-expanded", "true");

    rerender(<SectionHeader type="input" onCopy={vi.fn()} onToggleCollapse={vi.fn()} isCollapsed={true} />);

    expect(screen.getByRole("button", { name: /^Input/ })).toHaveAttribute("aria-expanded", "false");
  });

  it("names each copy button for the section it belongs to", () => {
    render(<SectionHeader type="output" onCopy={vi.fn()} onToggleCollapse={vi.fn()} />);

    expect(screen.getByRole("button", { name: "Copy output" })).toBeInTheDocument();
  });

  it("stays inert when no toggle handler is given", async () => {
    const onCopy = vi.fn();
    render(<SectionHeader type="input" onCopy={onCopy} />);

    await userEvent.click(screen.getByText("Input"));

    expect(onCopy).not.toHaveBeenCalled();
  });
});

/** 前置输入摘要的小额费用与完整 Token；双语验证所有动态标签和未知费用，自动卸载清理，无后台写入。 */
it.each(["en","zh-CN"] as const)("%s 输入输出摘要不截断小额费用",(locale)=>{
  const {rerender}=render(<I18nProvider initialLocale={locale}><SectionHeader type="input" tokens={10000} cost={0.00308641} turnCount={2} onCopy={vi.fn()} /></I18nProvider>);
  expect(screen.getByText(locale==="en"?"Tokens: 10,000":"Token 数：10,000")).toBeVisible();
  expect(screen.getByText(locale==="en"?"Cost: $0.00308641":"费用：$0.00308641")).toBeVisible();
  expect(screen.getByText(locale==="en"?"Turns: 2":"轮次：2")).toBeVisible();
  rerender(<I18nProvider initialLocale={locale}><SectionHeader type="output" onCopy={vi.fn()} /></I18nProvider>);
  expect(screen.queryByText(/\$0\.00308641/)).not.toBeInTheDocument();
});
