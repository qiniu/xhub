/* @vitest-environment jsdom */
import React from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { it, expect } from "vitest";
import { I18nProvider, useI18n, useT } from "@/i18n";
import { DataTablePagination } from "./DataTablePagination";

/** 测试用语言切换容器；无参数，展示分页与团队删除警告，切换由真实 i18n 提供，不触发网络。 */
function DirectoryLabels() {
  const { setLocale } = useI18n();
  const t = useT();
  return (
    <>
      <button onClick={() => setLocale("en")}>English</button>
      <DataTablePagination page={1} pageSize={25} rowCount={51} onPageChange={() => {}} onPageSizeChange={() => {}} />
      <p>{t("consoleDirectory.teamDelete.confirm")}</p>
      <p>{t("consoleDirectory.teamDelete.warning")}</p>
      <p>{t("consoleDirectory.noTeams")}</p>
      <span>测试-Team Alpha</span>
    </>
  );
}

/** 从中文切换英文验证分页、空态、删除提示与数量插值；用户名称保持原文，DOM与存储由测试框架清理。 */
it("switches directory labels without translating names", async () => {
  localStorage.clear();
  document.cookie = "xhub_locale=; max-age=0; path=/";
  render(
    <I18nProvider initialLocale="zh-CN">
      <DirectoryLabels />
    </I18nProvider>,
  );
  expect(screen.getByRole("button", { name: "前往下一页" })).toBeEnabled();
  expect(screen.getByTestId("pagination-range")).toHaveTextContent("显示 26-50，共 51 条");
  expect(screen.getByText(/成员账号及其个人 API 密钥会保留/)).toBeInTheDocument();
  expect(screen.getByText("未找到团队")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "English" }));
  expect(screen.getByRole("button", { name: "Go to next page" })).toBeEnabled();
  expect(screen.getByTestId("pagination-range")).toHaveTextContent("Showing 26-50 of 51");
  expect(screen.getByText(/Members and their personal API keys will be retained/)).toBeInTheDocument();
  expect(screen.getByText("No teams found")).toBeInTheDocument();
  expect(screen.getByText("测试-Team Alpha")).toBeInTheDocument();
  expect(screen.queryByText(/确定要删除此团队/)).not.toBeInTheDocument();
});
