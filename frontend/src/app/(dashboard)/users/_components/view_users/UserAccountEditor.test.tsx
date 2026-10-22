import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setActiveLocale, translate } from "@/i18n";
import UserAccountEditor from "./UserAccountEditor";
import type { UserInfoV2Response } from "@/components/networking";

const { update, fromError } = vi.hoisted(() => ({ update: vi.fn(), fromError: vi.fn() }));
vi.mock("@/components/networking", () => ({ userUpdateUserCall: update }));
vi.mock("@/lib/toast", () => ({ toast: { success: vi.fn(), fromError } }));
const account = { user_id: "u1", user_email: "user@example.com", user_alias: "Before", user_role: "internal_user", max_budget: 10 } as UserInfoV2Response;

describe("UserAccountEditor", () => {
  afterEach(() => setActiveLocale("en"));
  beforeEach(() => { vi.clearAllMocks(); update.mockResolvedValue({}); });

  /** 双语说明：前置无团队空预算，验证可访问帮助和空值保存；卸载及恢复语言清理，无真实写入。 */
  it.each(["en", "zh-CN"] as const)("explains unlimited personal budgets in %s", async (locale) => {
    setActiveLocale(locale);
    render(<UserAccountEditor user={{ ...account, teams: [], max_budget: null }} accessToken="session" onSaved={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByLabelText(translate(locale, "pages.users.maxBudget"))).toHaveAccessibleDescription(translate(locale, "quotaGuide.personalBillingHelp"));
    expect(screen.getByLabelText(translate(locale, "pages.users.maxBudget"))).toHaveAttribute("placeholder", translate(locale, "pages.users.unlimited"));
    await userEvent.setup().click(screen.getByRole("button", { name: translate(locale, "pages.users.saveChanges") }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("session", expect.objectContaining({ max_budget: null }), null));
  });

  /** 双语周期设置与取消：前置已有日期，验证完整选项和真实字段；失败保留日期，mock及组件由测试清理。 */
  it.each(["en", "zh-CN"] as const)("edits calendar dates in %s", async (locale) => {
    setActiveLocale(locale);
    const actor = userEvent.setup();
    render(<UserAccountEditor user={{ ...account, budget_reset_day: 15 }} accessToken="session" onSaved={vi.fn()} onCancel={vi.fn()} />);
    const control = screen.getByLabelText(translate(locale, "userBudgetReset.label"));
    expect(control).toHaveValue("15");
    expect(control.querySelectorAll("option")).toHaveLength(32);
    await actor.selectOptions(control, "31");
    update.mockRejectedValueOnce(new Error("Unavailable"));
    await actor.click(screen.getByRole("button", { name: translate(locale, "pages.users.saveChanges") }));
    await waitFor(() => expect(fromError).toHaveBeenCalled());
    expect(control).toHaveValue("31");
    await actor.selectOptions(control, "0");
    await actor.click(screen.getByRole("button", { name: translate(locale, "pages.users.saveChanges") }));
    await waitFor(() => expect(update).toHaveBeenLastCalledWith("session", expect.objectContaining({ budget_reset_day: 0 }), null));
  });

  /** 团队预算说明：前置已有团队且个人额度为空，双语占位符保持共享上级；卸载及恢复语言清理。 */
  it.each(["en", "zh-CN"] as const)("keeps the shared budget placeholder with a team in %s", (locale) => {
    setActiveLocale(locale);
    render(<UserAccountEditor user={{ ...account, teams: ["team-1"], max_budget: null }} accessToken="session" onSaved={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByLabelText(translate(locale, "pages.users.maxBudget"))).toHaveAttribute("placeholder", translate(locale, "quotaGuide.blank"));
  });

  /** 正常保存：前置已有账户，验证邮箱、名称和零预算发送正确且刷新回调执行；组件卸载清理，无外部写入。 */
  it("saves supported fields including a zero budget", async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn().mockResolvedValue(undefined);
    render(<UserAccountEditor user={account} accessToken="session" onSaved={onSaved} onCancel={vi.fn()} />);
    expect(screen.getByRole("combobox", { name: "Account role" })).toHaveTextContent("Regular user");
    await user.clear(screen.getByLabelText("User Alias"));
    await user.type(screen.getByLabelText("User Alias"), "After");
    await user.clear(screen.getByLabelText("Max Budget (USD)"));
    await user.type(screen.getByLabelText("Max Budget (USD)"), "0");
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(update).toHaveBeenCalledWith("session", { user_id: "u1", user_email: "user@example.com", user_alias: "After", budget_reset_day: 0, user_role: "user", max_budget: 0, rpm_limit: null, tpm_limit: null, blocked: false }, null);
  });

  /** 边界保存：前置名称/预算为空且账户禁用，验证空名称和无限预算不会被默认值覆盖；卸载清理。 */
  it("preserves null budgets and allows clearing the name", async () => {
    const user = userEvent.setup();
    render(<UserAccountEditor user={{ ...account, user_alias: null, max_budget: null, blocked: true }} accessToken="session" onSaved={vi.fn().mockResolvedValue(undefined)} onCancel={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("session", expect.objectContaining({ user_alias: "", max_budget: null, blocked: true }), null));
  });

  /** 失败保存：前置后台拒绝更新，验证错误可见、输入保留、刷新不执行且可重试；卸载清理。 */
  it("keeps the input and reports failed saves", async () => {
    const error = new Error("email already exists");
    update.mockRejectedValue(error);
    const user = userEvent.setup();
    const onSaved = vi.fn();
    render(<UserAccountEditor user={account} accessToken="session" onSaved={onSaved} onCancel={vi.fn()} />);
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => expect(fromError).toHaveBeenCalledWith(error));
    expect(screen.getByLabelText("User Alias")).toHaveValue("Before");
    expect(onSaved).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Save Changes" })).toBeEnabled();
  });

  /** 分钟边界：已有固定分配，清空 RPM 表示共享、TPM 零禁止调用；验证后台请求及刷新，卸载清理。 */
  it("clears RPM and preserves zero TPM", async () => {
    const user = userEvent.setup();
    render(<UserAccountEditor user={{ ...account, rpm_limit: 10, tpm_limit: 64 }} accessToken="session" onSaved={vi.fn().mockResolvedValue(undefined)} onCancel={vi.fn()} />);
    await user.clear(screen.getByLabelText("Requests per minute Limit (RPM)"));
    await user.clear(screen.getByLabelText("Tokens per minute Limit (TPM)"));
    await user.type(screen.getByLabelText("Tokens per minute Limit (TPM)"), "0");
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("session", expect.objectContaining({ rpm_limit: null, tpm_limit: 0 }), null));
  });

  /** 取消操作：前置可编辑账户，点击取消仅触发回调，后台不写入；卸载清理。 */
  it("cancels without writing", async () => {
    const onCancel = vi.fn();
    render(<UserAccountEditor user={account} accessToken="session" onSaved={vi.fn()} onCancel={onCancel} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();
    expect(update).not.toHaveBeenCalled();
  });
});
