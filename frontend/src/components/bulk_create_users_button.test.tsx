import { fireEvent, render, screen, waitFor, cleanup } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { I18nProvider, translate, useI18n } from "@/i18n";
import { downloadUserCsv } from "./user_csv";
import BulkCreateUsersButton from "./bulk_create_users_button";
import { userCreateCall } from "./networking";
vi.mock("./networking", () => ({ userCreateCall: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  localStorage.clear();
  document.cookie = "xhub_locale=; max-age=0; path=/";
});
/** 上传内存CSV；参数为内容和本地化标签，无返回值，仅触发组件读取，不保存文件。 */
function upload(text: string, label: string) {
  const file = new File([text], "users.csv", { type: "text/csv" });
  Object.defineProperty(file, "text", { value: async () => text });
  fireEvent.change(screen.getByLabelText(label), { target: { files: [file] } });
}
/** 测试用语言切换器；无参数，返回按钮供弹窗打开时切换语言，不修改用户输入。 */
function ToggleLocale() {
  const { setLocale } = useI18n();
  return <button onClick={() => setLocale("zh-CN")}>Switch test locale</button>;
}

// 双语组件测试：真实解析、提交字段和失败反馈，网络隔离；afterEach清理DOM及语言存储。
describe("bulk user import", () => {
  it.each(["en", "zh-CN"] as const)("validates and submits in %s without exposing passwords", async (locale) => {
    const tr = (key: string, vars?: Record<string, number>) => translate(locale, `userCsv.${key}`, vars);
    render(
      <I18nProvider initialLocale={locale}>
        <BulkCreateUsersButton accessToken="test" teams={[]} possibleUIRoles={null} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: tr("title") }));
    upload("user_email,user_role,password,max_budget\na@example.com,user,password123,10abc", tr("upload"));
    expect(await screen.findByText(tr("budgetError"))).toBeVisible();
    expect(screen.getByRole("button", { name: tr("create", { count: 1 }) })).toBeDisabled();
    upload("user_email,user_role,password,max_budget\na@example.com,user,password123,0", tr("upload"));
    await waitFor(() => expect(screen.queryByText(tr("budgetError"))).not.toBeInTheDocument());
    vi.mocked(userCreateCall).mockResolvedValueOnce({ user_id: "created" });
    fireEvent.click(screen.getByRole("button", { name: tr("create", { count: 1 }) }));
    await screen.findByText(tr("summary", { success: 1, failed: 0 }));
    expect(userCreateCall).toHaveBeenCalledWith("test", null, {
      user_email: "a@example.com",
      user_role: "user",
      password: "password123",
      max_budget: 0,
    });
    expect(screen.queryByText("password123")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: tr("create", { count: 1 }) })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: tr("reset") }));
    expect(screen.queryByText("a@example.com")).not.toBeInTheDocument();
    upload("user_email,user_role,password\na@example.com,user,password123", tr("upload"));
    await screen.findByText("a@example.com");
    vi.mocked(userCreateCall).mockRejectedValueOnce(new Error("private detail"));
    fireEvent.click(screen.getByRole("button", { name: tr("create", { count: 1 }) }));
    await screen.findByText(tr("summary", { success: 0, failed: 1 }));
    expect(screen.getByText(tr("requestError"))).toBeVisible();
    expect(screen.queryByText("private detail")).not.toBeInTheDocument();
  });
  // 已解析错误随语言变化，原始邮箱保持不变；只操作内存DOM，afterEach负责清理。
  it("updates validation language without modifying user data", async () => {
    render(
      <I18nProvider initialLocale="en">
        <ToggleLocale />
        <BulkCreateUsersButton accessToken="test" teams={[]} possibleUIRoles={null} />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Bulk create users" }));
    upload("user_email,user_role,password,max_budget\nOriginal@example.com,user,password123,10abc", "Upload CSV");
    await screen.findByText("Budget must be a finite non-negative number.");
    fireEvent.click(screen.getByText("Switch test locale"));
    expect(screen.getByText("预算必须是有限的非负数字。")).toBeVisible();
    expect(screen.getByText("Original@example.com")).toBeVisible();
    expect(screen.queryByText("Budget must be a finite non-negative number.")).not.toBeInTheDocument();
  });

  // 下载工具必须创建可用链接并延后释放URL；全部DOM与计时器在finally还原。
  it("downloads CSV and releases its object URL after navigation", () => {
    vi.useFakeTimers();
    const create = vi.fn(() => "blob:test");
    const revoke = vi.fn();
    const originalCreate = URL.createObjectURL;
    const originalRevoke = URL.revokeObjectURL;
    URL.createObjectURL = create;
    URL.revokeObjectURL = revoke;
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      expect(this.download).toBe("users.csv");
      expect(this.href).toBe("blob:test");
      expect(document.body.contains(this)).toBe(true);
    });
    try {
      downloadUserCsv("user_email\na@example.com", "users.csv");
      expect(click).toHaveBeenCalledOnce();
      expect(revoke).not.toHaveBeenCalled();
      vi.advanceTimersByTime(1000);
      expect(revoke).toHaveBeenCalledWith("blob:test");
    } finally {
      click.mockRestore();
      URL.createObjectURL = originalCreate;
      URL.revokeObjectURL = originalRevoke;
      vi.useRealTimers();
    }
  });
});
