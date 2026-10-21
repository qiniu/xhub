import { beforeEach, afterEach } from "vitest";
import { I18nProvider, useI18n, translate, setActiveLocale } from "@/i18n";
/** 各场景使用中文断言，结束恢复默认语言；无后台数据。 */
beforeEach(() => setActiveLocale("zh-CN"));
afterEach(() => setActiveLocale("en"));
import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import TemplateEditor from "./TemplateEditor";
import { emptyForm } from "./templateForm";

/** Harness 持有编辑器草稿；无参数，返回完整模板编辑器，内存状态由测试卸载清理。 */
function Harness() {
  const [form, setForm] = useState(emptyForm());
  return (
    <TemplateEditor
      name="fixture"
      form={form}
      deployments={[
        { deployment_id: "g1", model_name: "g", model: "upstream/g", api_base: "" },
        { deployment_id: "b1", model_name: "b", model: "upstream/b", api_base: "" },
      ]}
      accessToken={null}
      onName={() => {}}
      onChange={setForm}
      onJsonValid={() => {}}
    />
  );
}

describe("模板JSON与业务分区同步", () => {
  /** 导入合法JSON后修改故障转移，验证回到JSON时显示新草稿；失败输入须禁用表单切换，无外部数据。 */
  it("updates imported JSON after a fallback form change", () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole("tab", { name: "JSON" }));
    const text = screen.getByRole("textbox", { name: "JSON" });
    const body = JSON.parse((text as HTMLTextAreaElement).value);
    body.fallbacks = [{ g: ["b"] }];
    fireEvent.change(text, { target: { value: JSON.stringify(body) } });
    fireEvent.click(screen.getByRole("tab", { name: "故障转移" }));
    fireEvent.click(screen.getByRole("button", { name: "编辑故障转移 g 通用错误" }));
    const dialog = screen.getByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "清空回退" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "保存到模板" }));
    fireEvent.click(screen.getByRole("tab", { name: "JSON" }));
    const updated = screen.getByRole("textbox", { name: "JSON" });
    expect(JSON.parse((updated as HTMLTextAreaElement).value).fallbacks).toEqual([{ g: [] }]);
    fireEvent.change(updated, { target: { value: "{" } });
    expect(screen.getByRole("tab", { name: "故障转移" })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("alert")).toBeVisible();
    expect(screen.getByRole("region", { name: "真实路由预览" })).toBeVisible();
    expect(screen.getByRole("button", { name: "预览草稿路由" })).toBeDisabled();
  });
});

/** LanguageHarness 提供真实语言上下文；返回切换按钮和草稿宿主，存储由测试清理。 */
function LanguageHarness() {
  const { setLocale } = useI18n();
  return <><button onClick={() => setLocale("en")}>English</button><button onClick={() => setLocale("zh-CN")}>中文</button><Harness /></>;
}

/** 前置中文编辑器；验证非法 JSON 错误随语言切换、原始输入保持、恢复后可编辑；无后台写入，清理语言存储。 */
it("switches editor labels and active errors without changing user input", () => {
  localStorage.clear();
  document.cookie = "xhub_locale=; max-age=0; path=/";
  const view = render(<I18nProvider initialLocale="zh-CN"><LanguageHarness /></I18nProvider>);
  fireEvent.click(screen.getByRole("tab", { name: "JSON" }));
  fireEvent.change(screen.getByRole("textbox", { name: "JSON" }), { target: { value: "{用户原文" } });
  expect(screen.getByRole("alert")).toHaveTextContent(translate("zh-CN", "pages.routeTemplates.jsonInvalid"));
  fireEvent.click(screen.getByRole("button", { name: "English" }));
  expect(screen.getByRole("alert")).toHaveTextContent(translate("en", "pages.routeTemplates.jsonInvalid"));
  expect(screen.getByRole("textbox", { name: "JSON" })).toHaveValue("{用户原文");
  expect(screen.getByRole("tab", { name: "Load balancing" })).toHaveAttribute("aria-disabled", "true");
  fireEvent.change(screen.getByRole("textbox", { name: "JSON" }), { target: { value: JSON.stringify({ model_routes: [] }) } });
  fireEvent.click(screen.getByRole("tab", { name: "Load balancing" }));
  expect(screen.getByLabelText("Name")).toHaveValue("fixture");
  fireEvent.click(screen.getByRole("button", { name: "中文" }));
  expect(screen.getByLabelText("名称")).toHaveValue("fixture");
  view.unmount();
  localStorage.clear();
  document.cookie = "xhub_locale=; max-age=0; path=/";
});
