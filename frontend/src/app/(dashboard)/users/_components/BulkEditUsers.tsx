import React, { useId, useState } from "react";
import { userBulkUpdateUserCall } from "@/components/networking";
import { toast } from "@/lib/toast";
import { MoneyCell } from "@/components/shared/table_cells";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useT } from "@/i18n";

interface BulkEditUserModalProps {
  open: boolean;
  onCancel: () => void;
  selectedUsers: any[];
  possibleUIRoles: Record<string, Record<string, string>> | null;
  accessToken: string | null;
  onSuccess: () => void;
  teams: any[] | null;
  userRole: string | null;
  userModels: string[];
  allowAllUsers?: boolean;
}

/** 根据批量接口固定错误码选择语义翻译键；参数为API异常，返回本地化键，不显示未经翻译的服务端诊断。 */
export function bulkBudgetErrorKey(error: unknown): string {
  const value = error as { body?: { error?: { type?: string } } } | undefined;
  switch (value?.body?.error?.type) {
    case "bulk_budget_invalid":
      return "pages.users.bulkBudgetInvalid";
    case "bulk_budget_too_large":
      return "pages.users.bulkBudgetTooLarge";
    case "bulk_budget_not_found":
      return "pages.users.bulkBudgetMissing";
    case "bulk_budget_allocation":
      return "pages.users.bulkBudgetAllocation";
    default:
      return "pages.users.bulkBudgetFailed";
  }
}

/** 用户批量预算表单；参数为目标用户、管理会话和关闭/刷新回调，返回真实原子接口驱动的可访问对话框。
 * 只支持设置预算上限或明确取消个人上限，零不等于留空；成功后刷新用户列表，失败保留输入，不修改角色/模型/团队或历史消费。 */
const BulkBudgetForm: React.FC<BulkEditUserModalProps> = ({
  open,
  onCancel,
  selectedUsers,
  accessToken,
  onSuccess,
  allowAllUsers = false,
}) => {
  const t = useT();
  const [loading, setLoading] = useState(false);
  const [updateAllUsers, setUpdateAllUsers] = useState(false);
  const [mode, setMode] = useState("");
  const [budget, setBudget] = useState("");
  const [error, setError] = useState("");
  const allId = useId(),
    modeId = useId(),
    budgetId = useId();

  /** 提交显式预算命令；参数为表单事件，无返回。失败保留对话框并提示整批未更新，成功才刷新和关闭。 */
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (loading) return;
    if (
      !accessToken ||
      (!updateAllUsers && selectedUsers.length === 0) ||
      !mode ||
      (mode === "set" && (budget.trim() === "" || !Number.isFinite(Number(budget)) || Number(budget) < 0))
    ) {
      setError(t("pages.users.bulkBudgetInvalid"));
      return;
    }
    setLoading(true);
    setError("");
    try {
      const value = mode === "clear" ? null : Number(budget);
      const result = await userBulkUpdateUserCall(
        accessToken,
        { max_budget: value },
        updateAllUsers ? undefined : selectedUsers.map((user) => user.user_id),
        updateAllUsers,
      );
      // 只接受真实原子成功，防止旧版或错误代理返回部分成功后关闭表单。
      if (
        !result.atomic ||
        result.failed_updates !== 0 ||
        result.successful_updates !== result.total_requested ||
        result.total_requested < 1 ||
        (!updateAllUsers && result.total_requested !== selectedUsers.length)
      ) {
        throw new Error(t("uiErrors.bulkUpdateInvalid"));
      }
      toast.success(t("pages.users.bulkBudgetSuccess", { count: result.successful_updates }));
      onSuccess();
      onCancel();
    } catch (failure) {
      setError(t(bulkBudgetErrorKey(failure)));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !loading) onCancel();
      }}
    >
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-[800px]">
        <DialogHeader>
          <DialogTitle>
            {updateAllUsers
              ? t("pages.users.bulkEditAll")
              : t("pages.users.bulkEditCount", { count: selectedUsers.length })}
          </DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">{t("pages.users.bulkBudgetHelp")}</p>
        {allowAllUsers && (
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Checkbox
                id={allId}
                checked={updateAllUsers}
                disabled={loading}
                onCheckedChange={(checked) => setUpdateAllUsers(checked === true)}
              />
              <label htmlFor={allId}>{t("pages.users.updateAllUsers")}</label>
            </div>
            {updateAllUsers && (
              <p className="text-sm text-warning">{t("pages.users.bulkBudgetAllWarning", { count: 1000 })}</p>
            )}
          </div>
        )}
        {!updateAllUsers && (
          <div>
            <h5 className="mb-2 text-sm font-semibold">
              {t("pages.users.selectedUsers", { count: selectedUsers.length })}
            </h5>
            <div className="max-h-[200px] overflow-y-auto rounded-md border border-border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("pages.users.email")}</TableHead>
                    <TableHead>{t("pages.users.userId")}</TableHead>
                    <TableHead>{t("pages.users.budget")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {selectedUsers.map((user) => (
                    <TableRow key={user.user_id}>
                      <TableCell>{user.user_email || t("pages.users.noEmail")}</TableCell>
                      <TableCell className="break-all text-xs">{user.user_id}</TableCell>
                      <TableCell>
                        <MoneyCell
                          value={user.max_budget}
                          decimals={2}
                          emptyText={t("pages.users.unlimited")}
                          showZero
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        )}
        {/* 由表单统一校验，避免浏览器原生错误语言与控制台当前语言不一致。 */}
        <form noValidate onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <label htmlFor={modeId} className="text-sm font-medium">
              {t("pages.users.bulkBudgetAction")}
            </label>
            <select
              id={modeId}
              value={mode}
              disabled={loading}
              onChange={(event) => {
                setMode(event.target.value);
                setError("");
              }}
              className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              <option value="">{t("pages.users.bulkBudgetKeep")}</option>
              <option value="set">{t("pages.users.bulkBudgetSet")}</option>
              <option value="clear">{t("pages.users.bulkBudgetClear")}</option>
            </select>
          </div>
          {mode === "set" && (
            <div className="space-y-2">
              <label htmlFor={budgetId} className="text-sm font-medium">
                {t("pages.users.maxBudget")}
              </label>
              <Input
                id={budgetId}
                type="number"
                min={0}
                step="any"
                value={budget}
                disabled={loading}
                onChange={(event) => setBudget(event.target.value)}
              />
              <p className="text-xs text-muted-foreground">{t("pages.users.bulkBudgetZeroHelp")}</p>
            </div>
          )}
          {mode === "clear" && <p className="text-sm text-muted-foreground">{t("pages.users.bulkBudgetClearHelp")}</p>}
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" disabled={loading} onClick={onCancel}>
              {t("Cancel")}
            </Button>
            <Button type="submit" disabled={loading || !mode || (!updateAllUsers && selectedUsers.length === 0)}>
              {t(loading ? "pages.users.saving" : "pages.users.saveChanges")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
};
/** 控制批量预算表单生命周期；参数为对话框属性，关闭时卸载表单，返回打开的表单或空节点。
 * 调用方重复打开时重新创建状态，避免上次“全部用户/取消上限”的选择泄漏到新批次；不在effect中重置状态。 */
const BulkEditUserModal: React.FC<BulkEditUserModalProps> = (props) =>
  props.open ? <BulkBudgetForm {...props} /> : null;

export default BulkEditUserModal;
