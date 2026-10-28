import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useI18n } from "@/i18n";
import { I18nProvider } from "@/i18n/I18nProvider";
import SkillDetail from "./skill_detail";
import MakeSkillPublicForm from "./MakeSkillPublicForm";
import { skillHubPublicCall } from "../networking";
import type { Plugin } from "./types";

/** 组件测试切换真实语言上下文，验证详情随locale重绘，无网络或持久化写入。 */
function DetailLanguageSwitch() {
  const { setLocale } = useI18n();
  return <button onClick={() => setLocale("zh-CN")}>中文</button>;
}

const fixture: Plugin = { id: "fixture", name: "原始-name", description: "Keep 用户说明", source: { source: "github", repo: "example/fixture" }, enabled: true };
afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); document.cookie = "xhub_locale=; max-age=0; path=/"; });

describe("Skill support boundaries", () => {
  /** 真实客户端函数接收失败HTTP替身；非2xx不能伪造空列表，afterEach恢复fetch，无持久化数据。 */
  it.each([404, 500])("公开目录失败必须抛错 %s", async status => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("{}", { status })));
    await expect(skillHubPublicCall()).rejects.toThrow();
  });
  /** 200也必须符合目录响应；缺失列表、非法JSON和null均报错，fetch替身逐例恢复。 */
  it.each(["{}", "null", "{", '{"plugins":{}}', '{"plugins":[null]}', '{"plugins":[{}]}', '{"plugins":[42]}'])("拒绝非法目录响应 %s", async body => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(body, { status: 200 })));
    await expect(skillHubPublicCall()).rejects.toThrow();
  });
  /** 合法空列表和原始内容正常返回，不把用户名称说明翻译；无外部调用且每例清理fetch。 */
  it.each([{plugins: []}, {plugins: [fixture]}])("保留合法目录 %#", async ({plugins}) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(Response.json({ plugins })));
    expect(await skillHubPublicCall()).toEqual({ plugins });
  });
  /** 网络拒绝必须传递给目录错误状态；仅替换fetch，不访问外部网络，afterEach恢复。 */
  it("网络失败不返回空目录", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("network unavailable")));
    await expect(skillHubPublicCall()).rejects.toThrow("network unavailable");
  });
  /** 缺少来源的停用记录仍可查看，返回目录触发导航回调；cleanup回收DOM，无持久化写入。 */
  it("无来源的停用记录不生成链接且可返回", async () => {
    const onBack = vi.fn();
    render(<I18nProvider initialLocale="en"><SkillDetail skill={{ ...fixture, enabled: false, source: undefined } as unknown as Plugin} onBack={onBack} /></I18nProvider>);
    expect(screen.getByText("Configuration disabled")).toBeVisible();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Skills", exact: true }));
    expect(onBack).toHaveBeenCalledOnce();
  });
  /** 子目录链接和添加日期必须保留；语言切换只改变日期格式、不改变来源和用户文本，cleanup清理DOM。 */
  it("详情保留子目录并随语言格式化日期", async () => {
    const skill = { ...fixture, source: { source: "git-subdir" as const, url: "https://github.com/example/repo", path: "skills/test" }, created_at: "2026-10-28T12:00:00" };
    render(<I18nProvider initialLocale="en"><DetailLanguageSwitch /><SkillDetail skill={skill} onBack={vi.fn()} /></I18nProvider>);
    expect(screen.getByRole("link")).toHaveAttribute("href", "https://github.com/example/repo/tree/main/skills/test");
    expect(screen.getByText("10/28/2026")).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "中文", exact: true }));
    expect(screen.getByText("2026/10/28")).toBeVisible();
    expect(screen.getByText(fixture.description!)).toBeVisible();
    expect(screen.getByRole("link")).toHaveAttribute("href", "https://github.com/example/repo/tree/main/skills/test");
  });
  for (const locale of ["en", "zh-CN"] as const) {
    /** 双语详情保留原始元数据，但不提供未经实现的安装指令；纯渲染，cleanup回收DOM。 */
    it("详情只承诺元数据 " + locale, async () => {
      render(<I18nProvider initialLocale={locale}><SkillDetail skill={fixture} onBack={vi.fn()} /></I18nProvider>);
      expect(screen.getByText(fixture.name, { selector: "h1" })).toBeVisible();
      expect(screen.getByText(fixture.description!)).toBeVisible();
      expect(screen.getByText(locale === "en" ? "Configuration enabled" : "配置已启用")).toBeVisible();
      await userEvent.click(screen.getByRole("button", { name: locale === "en" ? "How to Use" : "如何使用" }));
      expect(screen.getByText(locale === "en" ? "Publishing and installation are unavailable" : "发布与安装暂不可用")).toBeVisible();
      expect(document.body).not.toHaveTextContent("/plugin");
    });
    /** 已启用记录不能被零请求“发布成功”；支持状态弹窗只允许关闭，不提交或调用成功回调，清理DOM。 */
    it("发布不能假成功 " + locale, async () => {
      const onSuccess = vi.fn();
      const onClose = vi.fn();
      const fetcher = vi.fn();
      vi.stubGlobal("fetch", fetcher);
      render(<I18nProvider initialLocale={locale}><MakeSkillPublicForm visible onClose={onClose} accessToken="fixture" skillsList={[fixture]} onSuccess={onSuccess} /></I18nProvider>);
      expect(screen.getByText(locale === "en" ? "Publishing and installation are unavailable" : "发布与安装暂不可用")).toBeVisible();
      expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: locale === "en" ? "Close" : "关闭", exact: true }));
      expect(onClose).toHaveBeenCalledOnce();
      expect(onSuccess).not.toHaveBeenCalled();
      expect(fetcher).not.toHaveBeenCalled();
    });
  }
});
