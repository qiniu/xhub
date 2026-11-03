import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it } from "vitest";
import { I18nProvider, useI18n, translate } from "@/i18n";
import SkillHubDashboard from "./SkillHubDashboard";
import type { Plugin } from "../claude_code_plugins/types";

const fixture: Plugin = { id: "one", name: "保留-name", description: "Original 原文", enabled: false, source: { source: "github", repo: "example/fixture" } };
afterEach(() => { cleanup(); localStorage.clear(); document.cookie = "xhub_locale=; Max-Age=0; path=/"; });

/** 仅供语言切换测试；按钮切换上下文，不发请求，DOM和语言存储由afterEach清理。 */
function SwitchLocale() {
  const { setLocale } = useI18n();
  return <button onClick={() => setLocale("zh-CN")}>中文</button>;
}
for (const locale of ["en", "zh-CN"] as const) {
  /** 失败与合法空列表必须区分；验证双语支持状态，无真实后端依赖，清理DOM。 */
  it("区分错误与空列表 " + locale, () => {
    const view = render(<I18nProvider initialLocale={locale}><SkillHubDashboard skills={[]} isLoading={false} loadError /></I18nProvider>);
    expect(screen.getByRole("alert")).toHaveTextContent(translate(locale,"skillHub.loadError"));
    expect(screen.queryByText(translate(locale,"skillHub.empty"))).not.toBeInTheDocument();
    view.rerender(<I18nProvider initialLocale={locale}><SkillHubDashboard skills={[]} isLoading={false} /></I18nProvider>);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText(translate(locale,"skillHub.empty"))).toBeVisible();
  });
}
/** 当前页面切换语言后，动态数量和配置状态同步翻译，名称与描述保持原文；无请求，清理DOM与语言存储。 */
it("语言切换保留元数据与动态数量", async () => {
  render(<I18nProvider initialLocale="en"><SwitchLocale /><SkillHubDashboard skills={[fixture]} isLoading={false} /></I18nProvider>);
  expect(screen.getByText("Showing 1 of 1 configurations")).toBeVisible();
  await userEvent.click(screen.getByRole("button", { name: "中文" }));
  expect(screen.getByText("显示 1 / 1 条配置")).toBeVisible();
  expect(screen.getByText("配置已停用")).toBeVisible();
  expect(screen.getByText(fixture.name)).toBeVisible();
  expect(screen.getByText(fixture.description!)).toBeVisible();
});
