import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen, waitFor } from "../../../../../tests/test-utils";
import BulkEditUserModal, { bulkBudgetErrorKey } from "./BulkEditUsers";
import { userBulkUpdateUserCall } from "@/components/networking";
import { toast } from "@/lib/toast";
import { setActiveLocale, translate } from "@/i18n";

vi.mock("@/components/networking", () => ({ userBulkUpdateUserCall: vi.fn() }));
const request = vi.mocked(userBulkUpdateUserCall);

/** 创建独立组件输入；返回仅本测试使用的账户与回调，不修改服务器，组件卸载由测试运行器负责。 */
function props() {
  return {
    open: true,
    onCancel: vi.fn(),
    onSuccess: vi.fn(),
    selectedUsers: [
      { user_id: "a", user_email: "原始邮箱@example.com", max_budget: 50 },
      { user_id: "b", user_email: "b@example.com", max_budget: null },
    ],
    possibleUIRoles: null,
    accessToken: "test-token",
    teams: [],
    userRole: "Admin",
    userModels: [],
    allowAllUsers: true,
  };
}

/** 返回后台原子成功形状；参数为实际数量，默认两位，供组件层验证成功处理，完整接口能力另由真实regression/E2E验证。 */
function result(count = 2) {
  return {
    atomic: true,
    batch_id: "batch-test",
    results: [],
    total_requested: count,
    successful_updates: count,
    failed_updates: 0,
  };
}

for (const locale of ["en", "zh-CN"] as const)
  describe("bulk user budget " + locale, () => {
    beforeEach(() => {
      vi.clearAllMocks();
      setActiveLocale(locale);
      request.mockResolvedValue(result());
    });
    const tr = (key: string) => translate(locale, key);

    /** 验证初始不修改、范围说明及仅支持预算字段；无网络，清理由组件卸载完成。 */
    it("shows explicit budget-only actions and preserves original account text", () => {
      renderWithProviders(<BulkEditUserModal {...props()} />);
      expect(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") })).toHaveValue("");
      expect(screen.getByRole("button", { name: tr("pages.users.saveChanges") })).toBeDisabled();
      expect(screen.getByText(tr("pages.users.bulkBudgetHelp"))).toBeVisible();
      expect(screen.getByText("原始邮箱@example.com")).toBeVisible();
      expect(screen.queryByText(tr("pages.users.teamManagement"))).not.toBeInTheDocument();
      expect(screen.getAllByRole("combobox")).toHaveLength(2);
    });

    /** 验证双语独立日期编辑不发送预算，允许取消刷新；组件替身由beforeEach重置，无持久数据。 */
    it("updates only the chosen reset date", async () => {
      const user = userEvent.setup();
      renderWithProviders(<BulkEditUserModal {...props()} />);
      await user.selectOptions(screen.getByLabelText(tr("userBudgetReset.label")), "31");
      expect(screen.getByText(translate(locale, "userBudgetReset.day", { day: 31 }))).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: tr("pages.users.saveChanges") }));
      expect(request).toHaveBeenCalledWith("test-token", { budget_reset_day: 31 }, ["a", "b"], false);
    });

    /** 验证数字零确为上限而非取消；提交后只在确认全部成功时刷新关闭，fixture不写真实资源。 */
    it("submits zero as a real cap and closes only after atomic success", async () => {
      const user = userEvent.setup(),
        p = props();
      renderWithProviders(<BulkEditUserModal {...p} />);
      await user.selectOptions(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") }), "set");
      await user.type(screen.getByLabelText(tr("pages.users.maxBudget")), "0");
      await user.click(screen.getByRole("button", { name: tr("pages.users.saveChanges") }));
      await waitFor(() => expect(request).toHaveBeenCalledWith("test-token", { max_budget: 0 }, ["a", "b"], false));
      expect(toast.success).toHaveBeenCalledWith(translate(locale, "pages.users.bulkBudgetSuccess", { count: 2 }));
      expect(p.onSuccess).toHaveBeenCalledOnce();
      expect(p.onCancel).toHaveBeenCalledOnce();
    });

    /** 验证明示取消个人上限发送null，留空从不发送null；纯组件替身无持久资源。 */
    it("clears only through the explicit remove action", async () => {
      const user = userEvent.setup();
      renderWithProviders(<BulkEditUserModal {...props()} />);
      await user.selectOptions(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") }), "clear");
      expect(screen.getByText(tr("pages.users.bulkBudgetClearHelp"))).toBeVisible();
      await user.click(screen.getByRole("button", { name: tr("pages.users.saveChanges") }));
      await waitFor(() => expect(request).toHaveBeenCalledWith("test-token", { max_budget: null }, ["a", "b"], false));
    });

    /** 验证设置模式空输入显示当前语言错误且不会请求后台，避免误清空额度；无需外部清理。 */
    it("rejects blank values without calling the API", async () => {
      const user = userEvent.setup();
      renderWithProviders(<BulkEditUserModal {...props()} />);
      await user.selectOptions(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") }), "set");
      await user.click(screen.getByRole("button", { name: tr("pages.users.saveChanges") }));
      expect(screen.getByRole("alert")).toHaveTextContent(tr("pages.users.bulkBudgetInvalid"));
      expect(request).not.toHaveBeenCalled();
    });

    /** 验证负数由应用显示本地化错误，不依赖浏览器原生语言；HTTP不应执行，无持久资源。 */
    it("rejects negative budgets with a localized error", async () => {
      const user = userEvent.setup();
      renderWithProviders(<BulkEditUserModal {...props()} />);
      await user.selectOptions(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") }), "set");
      await user.type(screen.getByLabelText(tr("pages.users.maxBudget")), "-1");
      await user.click(screen.getByRole("button", { name: tr("pages.users.saveChanges") }));
      expect(screen.getByRole("alert")).toHaveTextContent(tr("pages.users.bulkBudgetInvalid"));
      expect(request).not.toHaveBeenCalled();
    });

    /** 验证全部模式有明确系统范围/数量限制并提交显式标记；mock只验证UI契约，真实全部更新由后台回归覆盖。 */
    it("submits all users only after explicit selection", async () => {
      const user = userEvent.setup();
      request.mockResolvedValue(result(3));
      renderWithProviders(<BulkEditUserModal {...props()} />);
      await user.click(screen.getByRole("checkbox", { name: tr("pages.users.updateAllUsers") }));
      expect(screen.getByText(translate(locale, "pages.users.bulkBudgetAllWarning", { count: 1000 }))).toBeVisible();
      await user.selectOptions(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") }), "set");
      await user.type(screen.getByLabelText(tr("pages.users.maxBudget")), "12.5");
      await user.click(screen.getByRole("button", { name: tr("pages.users.saveChanges") }));
      await waitFor(() => expect(request).toHaveBeenCalledWith("test-token", { max_budget: 12.5 }, undefined, true));
      expect(toast.success).toHaveBeenCalledWith(translate(locale, "pages.users.bulkBudgetSuccess", { count: 3 }));
    });

    /** 验证真实错误码映射成当前语言且保留输入，失败不刷新不关闭；无外部资源。 */
    it("shows allocation errors in the selected language and keeps input", async () => {
      const user = userEvent.setup(),
        p = props();
      request.mockRejectedValue({ body: { error: { type: "bulk_budget_allocation", message: "server diagnostic" } } });
      renderWithProviders(<BulkEditUserModal {...p} />);
      await user.selectOptions(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") }), "set");
      await user.type(screen.getByLabelText(tr("pages.users.maxBudget")), "7");
      await user.click(screen.getByRole("button", { name: tr("pages.users.saveChanges") }));
      await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(tr("pages.users.bulkBudgetAllocation")));
      expect(screen.getByLabelText(tr("pages.users.maxBudget"))).toHaveValue(7);
      expect(p.onCancel).not.toHaveBeenCalled();
      expect(p.onSuccess).not.toHaveBeenCalled();
    });

    /** 验证部分成功/缺少atomic标记不能显示成功，防止兼容旧错误接口时假成功；纯组件fixture。 */
    it("rejects partial success responses", async () => {
      const user = userEvent.setup(),
        p = props();
      request.mockResolvedValue({ ...result(), successful_updates: 1, failed_updates: 1 });
      renderWithProviders(<BulkEditUserModal {...p} />);
      await user.selectOptions(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") }), "clear");
      await user.click(screen.getByRole("button", { name: tr("pages.users.saveChanges") }));
      await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(tr("pages.users.bulkBudgetFailed")));
      expect(toast.success).not.toHaveBeenCalled();
      expect(p.onCancel).not.toHaveBeenCalled();
    });

    /** 验证取消无写入、重开恢复不修改模式；所有本地状态随组件卸载清理。 */
    it("resets scope and operation on reopening", async () => {
      const user = userEvent.setup(),
        p = props();
      const view = renderWithProviders(<BulkEditUserModal {...p} />);
      await user.click(screen.getByRole("checkbox", { name: tr("pages.users.updateAllUsers") }));
      await user.selectOptions(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") }), "clear");
      await user.click(screen.getByRole("button", { name: tr("Cancel") }));
      expect(p.onCancel).toHaveBeenCalledOnce();
      expect(request).not.toHaveBeenCalled();
      view.rerender(<BulkEditUserModal {...p} open={false} />);
      view.rerender(<BulkEditUserModal {...p} />);
      expect(screen.getByRole("combobox", { name: tr("pages.users.bulkBudgetAction") })).toHaveValue("");
      expect(screen.getByRole("checkbox")).not.toBeChecked();
    });
  });

/** 验证固定错误码覆盖及未知异常回退；纯映射测试，无副作用和清理需求。 */
it("maps all supported API error types", () => {
  for (const [code, key] of Object.entries({
    bulk_budget_invalid: "bulkBudgetInvalid",
    bulk_budget_too_large: "bulkBudgetTooLarge",
    bulk_budget_not_found: "bulkBudgetMissing",
    bulk_budget_allocation: "bulkBudgetAllocation",
  }))
    expect(bulkBudgetErrorKey({ body: { error: { type: code } } })).toBe("pages.users." + key);
  expect(bulkBudgetErrorKey(undefined)).toBe("pages.users.bulkBudgetFailed");
});
