import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider, translate, useI18n } from "@/i18n";
import * as api from "@/lib/http/enterprise";
import SAMLRefresh from "./SAMLRefresh";
vi.mock("@/lib/http/enterprise");
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
const initial: api.SAMLRefreshStatus = {
  connection_id: "fixture",
  enabled: false,
  source_url: "",
  interval_hours: 6,
  last_attempt: 0,
  last_success: 0,
  next_attempt: 0,
  expires_at: 1793200000,
  error_code: "",
};
// 两种语言真实组件交互；覆盖默认关闭、用户输入原样提交、保存回读和失败提示，DOM/mock 在每例后清理。
it.each(["en", "zh-CN"] as const)("%s refresh settings and failure", async (locale) => {
  const tr = (key: string) => translate(locale, "samlRefresh." + key);
  let state = { ...initial };
  vi.mocked(api.readEnterprise).mockImplementation(async () => state);
  vi.mocked(api.saveEnterprise).mockImplementation(async (_, body) => {
    state = { ...state, ...body } as api.SAMLRefreshStatus;
  });
  render(
    <I18nProvider initialLocale={locale}>
      <SAMLRefresh id="fixture" />
    </I18nProvider>,
  );
  expect(await screen.findByRole("switch", { name: tr("enabled") })).not.toBeChecked();
  fireEvent.change(screen.getByLabelText(tr("interval")), { target: { value: "0" } });
  expect(screen.getByRole("button", { name: tr("save") })).toBeDisabled();
  fireEvent.change(screen.getByLabelText(tr("interval")), { target: { value: "6" } });
  fireEvent.change(screen.getByLabelText(tr("source")), { target: { value: "https://idp.example/原始" } });
  fireEvent.click(screen.getByRole("switch", { name: tr("enabled") }));
  fireEvent.click(screen.getByRole("button", { name: tr("save") }));
  await screen.findByText(tr("saved"));
  expect(api.saveEnterprise).toHaveBeenCalledWith(expect.any(String), {
    enabled: true,
    interval_hours: 6,
    source_url: "https://idp.example/原始",
  });
  vi.mocked(api.refreshSSOMetadata).mockImplementation(async () => {
    state = { ...state, error_code: "trust_changed" };
    throw new Error("fixture private details");
  });
  fireEvent.click(screen.getByRole("button", { name: tr("now") }));
  await screen.findByText(tr("errors.trust_changed"));
  expect(screen.queryByText("fixture private details")).not.toBeInTheDocument();
  vi.mocked(api.refreshSSOMetadata).mockImplementation(async () => {
    state = { ...state, error_code: "", last_success: 1793190000 };
    return state;
  });
  await waitFor(() => expect(screen.getByRole("button", { name: tr("now") })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: tr("now") }));
  await screen.findByText(tr("refreshed"));
});
// 初次读取失败不能展示假默认配置；重载成功才允许编辑，清理 DOM 与 mock。
it("load failure requires successful retry", async () => {
  vi.mocked(api.readEnterprise).mockRejectedValueOnce(new Error("unavailable")).mockResolvedValue(initial);
  render(
    <I18nProvider initialLocale="en">
      <SAMLRefresh id="fixture" />
    </I18nProvider>,
  );
  await screen.findByRole("alert");
  expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: translate("en", "samlRefresh.load") }));
  await screen.findByRole("switch");
});

/** 真实语言切换控制，仅修改 i18n 上下文，测试结束随组件卸载。 */
function LanguageSwitch() {
  const { setLocale } = useI18n();
  return <button onClick={() => setLocale("en")}>Switch test locale</button>;
}
// 切换语言后刷新表单全部采用英文，已输入来源原文不变；异步初始加载完成后操作，清理 DOM 和 mock。
it("switches locale without changing the entered metadata URL", async () => {
  vi.mocked(api.readEnterprise).mockResolvedValue(initial);
  render(
    <I18nProvider initialLocale="zh-CN">
      <LanguageSwitch />
      <SAMLRefresh id="fixture" />
    </I18nProvider>,
  );
  const input = await screen.findByLabelText(translate("zh-CN", "samlRefresh.source"));
  fireEvent.change(input, { target: { value: "https://idp.example/原文" } });
  fireEvent.click(screen.getByRole("button", { name: "Switch test locale" }));
  expect(screen.getByLabelText(translate("en", "samlRefresh.source"))).toHaveValue("https://idp.example/原文");
  expect(screen.queryByText(translate("zh-CN", "samlRefresh.hint"))).not.toBeInTheDocument();
  expect(screen.getByRole("switch", { name: translate("en", "samlRefresh.enabled") })).not.toBeChecked();
});
