import CopyButton from "@/components/shared/CopyButton";
import { QuotaGuide } from "@/components/shared/QuotaGuide";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import useTeams from "@/app/(dashboard)/hooks/useTeams";
import { formatNumberWithCommas } from "@/utils/dataUtils";
import { ArrowLeft, MoreVertical } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useEffect, useState } from "react";
import { isProxyAdminRole, isUserTeamAdminForSingleTeam } from "../../utils/roles";
import DeleteResourceModal from "../common_components/DeleteResourceModal";
import { KeyResponse } from "../key_team_helpers/key_list";
import { toast } from "@/lib/toast";
import { apiClient, keyDeleteCall, keyUpdateCall } from "../networking";
import { useResetKeySpend } from "@/app/(dashboard)/hooks/keys/useResetKeySpend";
import { useSetKeyBlockedState } from "@/app/(dashboard)/hooks/keys/useSetKeyBlockedState";
import { keyKeys } from "@/app/(dashboard)/hooks/keys/useKeys";
import { useQueryClient } from "@tanstack/react-query";
import { RegenerateKeyModal } from "../organisms/RegenerateKeyModal";
import { parseErrorMessage } from "../shared/errorUtils";
import { KeyEditView } from "./key_edit_view";
import { useT } from "@/i18n";

interface KeyInfoViewProps {
  keyId: string;
  onClose: () => void;
  keyData: KeyResponse | undefined;
  onKeyDataUpdate?: (data: Partial<KeyResponse>) => void;
  onDelete?: () => void;
  teams: any[] | null;
  backButtonText?: string;
}

/** 密钥管理页；参数含密钥快照、身份范围及导航回调，返回直接编辑表单。
 * 隐藏高级配置保留原值；保存、删除和轮换成功后更新查询缓存及导航，失败保留当前输入。 */
export default function KeyInfoView({
  onClose,
  keyData,
  teams,
  onKeyDataUpdate,
  onDelete,
  backButtonText,
}: KeyInfoViewProps) {
  const t = useT();
  const { accessToken, userId: userID, userRole, userRoleLabel, isViewOnly, premiumUser } = useAuthorized();
  const queryClient = useQueryClient();
  const { teams: teamsData } = useTeams();
  const [isDeleteModalOpen, setIsDeleteModalOpen] = useState(false);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [isRegenerateModalOpen, setIsRegenerateModalOpen] = useState(false);
  const [isResetSpendModalOpen, setIsResetSpendModalOpen] = useState(false);
  const [isBlockModalOpen, setIsBlockModalOpen] = useState(false);
  const { mutate: resetKeySpend, isPending: resetSpendLoading } = useResetKeySpend();
  const { mutate: setKeyBlockedState, isPending: blockLoading } = useSetKeyBlockedState();
  // 本地快照承接编辑和轮换响应，避免异步列表回读覆盖新状态。
  const [currentKeyData, setCurrentKeyData] = useState<KeyResponse | undefined>(keyData);
  const [keyDataUpdateHeldUntilModalClose, setKeyDataUpdateHeldUntilModalClose] = useState<Partial<KeyResponse> | null>(
    null,
  );

  // Update local state when keyData prop changes (but don't reset to undefined)
  useEffect(() => {
    if (keyData) {
      setCurrentKeyData(keyData);
    }
  }, [keyData]);

  // Use currentKeyData instead of keyData throughout the component
  if (!currentKeyData) {
    return (
      <div className="p-4">
        <Button variant="ghost" onClick={onClose} className="mb-4">
          <ArrowLeft className="size-4" />
          {backButtonText ?? t("keyEditor.back")}
        </Button>
        <p className="text-sm">{t("Key not found")}</p>
      </div>
    );
  }

  /** 保存精简表单的部分更新；参数为可见字段，返回保存 Promise。
   * 允许列表保护隐藏权限及元数据；空额度变为 null，失败保留输入并显示通知。 */
  const handleKeyUpdate = async (values: Record<string, unknown>) => {
    if (!accessToken || !canModifyKey) return;
    const fields = [
      "key_alias",
      "models",
      "allowed_routes",
      "max_budget",
      "rpm_limit",
      "tpm_limit",
      "route_template_id",
      "duration",
      "auto_rotate",
      "rotation_interval",
    ];
    const payload: Record<string, unknown> = { key: currentKeyData.token_id || currentKeyData.token };
    for (const field of fields) {
      if (field in values)
        payload[field] = ["max_budget", "rpm_limit", "tpm_limit"].includes(field)
          ? values[field] === "" || values[field] == null
            ? null
            : Number(values[field])
          : values[field];
    }
    try {
      const updated = await keyUpdateCall(accessToken, payload);
      setCurrentKeyData((previous) => (previous ? { ...previous, ...updated } : previous));
      onKeyDataUpdate?.(updated);
      await queryClient.invalidateQueries({ queryKey: keyKeys.lists() });
      toast.success(t("Key updated successfully"));
    } catch (error) {
      toast.fromError(parseErrorMessage(error));
    }
  };

  /** 删除当前密钥并刷新缓存；无参数，返回 Promise，失败显示通知，finally 释放加载状态。 */
  const handleDelete = async () => {
    try {
      setDeleteLoading(true);
      if (!accessToken) return;
      await keyDeleteCall(accessToken as string, currentKeyData.token_id || currentKeyData.token);
      toast.success(t("Key deleted successfully"));
      await queryClient.invalidateQueries({ queryKey: keyKeys.lists() });
      if (onDelete) {
        onDelete();
      }
      onClose();
    } catch (error) {
      console.error("Error deleting the key:", error);
      toast.fromError(error);
    } finally {
      setDeleteLoading(false);
      setIsDeleteModalOpen(false);
    }
  };

  /** 合并轮换响应；输入新密钥字段，返回无，暂存导航更新直到用户关闭新密钥弹窗。 */
  const handleRegenerateKeyUpdate = (updatedKeyData: Partial<KeyResponse>) => {
    const regeneratedAt = new Date();
    // Update local state immediately with ALL the new data
    setCurrentKeyData((prevData) => {
      if (!prevData) return undefined;
      const newData = {
        ...prevData,
        ...updatedKeyData, // This should include the new token (key-id)
        // Update the created_at to show when it was regenerated
        created_at: regeneratedAt.toLocaleString(),
      };
      return newData;
    });

    // 暂存导航变更，先让用户复制新密钥。

    setKeyDataUpdateHeldUntilModalClose({
      ...updatedKeyData,
      created_at: regeneratedAt.toLocaleString(),
    });
  };

  /** 关闭轮换弹窗；无参数和返回值，关闭后通知列表更新导航标识。 */
  const handleRegenerateModalClose = () => {
    setIsRegenerateModalOpen(false);
    if (keyDataUpdateHeldUntilModalClose) {
      setKeyDataUpdateHeldUntilModalClose(null);
      onKeyDataUpdate?.(keyDataUpdateHeldUntilModalClose);
    }
  };

  /** 对账成功后读取真实Key余额并刷新详情/列表缓存；失败抛给对账区明确提示，不用本地算术伪造退款。 */
  const refreshBudgetAfterReconciliation = async () => {
    const response = await apiClient.get<{ info: KeyResponse }>("/key/info", { accessToken, query: { key: currentKeyData.token_id } });
    if (!response.info || typeof response.info.budget_pending !== "number") throw new Error(t("uiErrors.budgetMissing"));
    setCurrentKeyData(response.info);
    onKeyDataUpdate?.(response.info);
    await queryClient.invalidateQueries({ queryKey: keyKeys.all });
  };
  // 使用原始角色标签排除effective Admin的只读查看者；服务端仍强制要求平台管理员登录会话。
  const canReconcileBudget = !isViewOnly && isProxyAdminRole(userRoleLabel ?? userRole ?? "");

  const canModifyKey =
    isProxyAdminRole(userRole || "") ||
    (teamsData &&
      isUserTeamAdminForSingleTeam(
        teamsData?.filter((team) => team.team_id === currentKeyData.team_id)[0]?.members_with_roles,
        userID || "",
      )) ||
    (userID === currentKeyData.user_id && userRole !== "Internal Viewer");

  const isKeyAdmin =
    isProxyAdminRole(userRole || "") ||
    Boolean(
      teamsData &&
      isUserTeamAdminForSingleTeam(
        teamsData?.filter((team) => team.team_id === currentKeyData.team_id)[0]?.members_with_roles,
        userID || "",
      ),
    );

  const canResetSpend = isKeyAdmin;
  const canBlockKey = isKeyAdmin;

  /** 重置当前周期消费；无参数和返回值，成功同步快照，失败保留原金额并显示通知。 */
  const handleResetSpend = () => {
    resetKeySpend(currentKeyData.token_id || currentKeyData.token, {
      onSuccess: () => {
        setCurrentKeyData((prevData) => (prevData ? { ...prevData, spend: 0 } : undefined));
        if (onKeyDataUpdate) {
          onKeyDataUpdate({ spend: 0 });
        }
        toast.success(t("Key spend reset to $0"));
        setIsResetSpendModalOpen(false);
      },
      onError: (error) => {
        toast.fromError(parseErrorMessage(error));
        console.error("Error resetting key spend:", error);
      },
    });
  };

  const isBlocked = currentKeyData.blocked === true;

  /** 切换密钥封禁；无参数和返回值，服务端成功后更新状态，失败保留现状。 */
  const handleToggleBlocked = () => {
    setKeyBlockedState(
      { keyToken: currentKeyData.token_id || currentKeyData.token, blocked: !isBlocked },
      {
        onSuccess: (response) => {
          const blocked = response.blocked === true;
          setCurrentKeyData((prevData) => (prevData ? { ...prevData, blocked } : undefined));
          if (onKeyDataUpdate) {
            onKeyDataUpdate({ blocked });
          }
          toast.success(blocked ? t("Key blocked") : t("Key unblocked"));
          setIsBlockModalOpen(false);
        },
        onError: (error) => {
          toast.fromError(parseErrorMessage(error));
          console.error("Error updating key blocked state:", error);
        },
      },
    );
  };

  const editorHeader = (
    <>
      <div className="mb-6 space-y-4">
        <Button variant="ghost" onClick={onClose} className="-ml-3">
          <ArrowLeft className="size-4" />
          {backButtonText ?? t("keyEditor.back")}
        </Button>
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0 space-y-2">
            <h1 className="break-words text-2xl font-semibold">{currentKeyData.key_alias || t("Virtual Key")}</h1>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span>{t("Key ID")}</span>
              <code className="break-all">{currentKeyData.token_id || currentKeyData.token}</code>
              <CopyButton value={currentKeyData.token_id || currentKeyData.token} label={t("Copy Key ID")} />
            </div>
            {isBlocked && <Badge variant="destructive">{t("Blocked")}</Badge>}
          </div>
          {canModifyKey && (
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button variant="outline" aria-label={t("More key actions")} />}>
                <MoreVertical className="size-4" />
                {t("keyEditor.actions")}
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => setIsRegenerateModalOpen(true)} disabled={!premiumUser}>
                  {t("Regenerate Key")}
                </DropdownMenuItem>
                {canBlockKey && (
                  <DropdownMenuItem onClick={() => setIsBlockModalOpen(true)}>
                    {t(isBlocked ? "Unblock Key" : "Block Key")}
                  </DropdownMenuItem>
                )}
                {canResetSpend && (
                  <DropdownMenuItem onClick={() => setIsResetSpendModalOpen(true)}>{t("Reset Spend")}</DropdownMenuItem>
                )}
                <DropdownMenuItem variant="destructive" onClick={() => setIsDeleteModalOpen(true)}>
                  {t("Delete Key")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>
      </div>
      <QuotaGuide scope="key" />
    </>
  );

  return (
    <div className="h-full min-h-0 w-full overflow-hidden">
      {/* Add RegenerateKeyModal */}
      <RegenerateKeyModal
        selectedToken={currentKeyData}
        visible={isRegenerateModalOpen}
        onClose={handleRegenerateModalClose}
        onKeyUpdate={handleRegenerateKeyUpdate}
      />

      {/* Delete Confirmation Modal */}
      <DeleteResourceModal
        isOpen={isDeleteModalOpen}
        title={t("Delete Key")}
        alertMessage={t("keyEditor.deleteWarning")}
        message={t("Are you sure you want to delete this Virtual Key?")}
        resourceInformationTitle={t("keyEditor.information")}
        resourceInformation={[
          {
            label: t("Key Alias"),
            value: currentKeyData?.key_alias || "-",
          },
          {
            label: t("Key ID"),
            value: currentKeyData?.token_id || currentKeyData?.token || "-",
            code: true,
          },
          {
            label: t("Team ID"),
            value: currentKeyData?.team_id || "-",
            code: true,
          },
          {
            label: t("Spend"),
            value: currentKeyData?.spend ? `$${formatNumberWithCommas(currentKeyData.spend, 4)}` : "$0.0000",
          },
        ]}
        onCancel={() => {
          setIsDeleteModalOpen(false);
        }}
        onOk={handleDelete}
        confirmLoading={deleteLoading}
        requiredConfirmation={currentKeyData?.key_alias}
      />

      {/* Reset Spend Confirmation Modal */}
      <Dialog open={isResetSpendModalOpen} onOpenChange={(open) => setIsResetSpendModalOpen(open)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("Reset Key Spend")}</DialogTitle>
          </DialogHeader>
          <p>
            {t("Reset spend for")}{" "}
            <strong>{currentKeyData?.key_alias || currentKeyData?.token_id || t("this key")}</strong> {t("to")}{" "}
            <strong>$0</strong>?
          </p>
          <p style={{ color: "#666", fontSize: "0.875rem", marginTop: 8 }}>
            {t("Current spend:")} <strong>${formatNumberWithCommas(currentKeyData.spend, 4)}</strong>
            {t(
              ". Spend history is preserved in logs. This resets the current period spend counter, the same as an automatic budget reset.",
            )}
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setIsResetSpendModalOpen(false)}>
              {t("Cancel")}
            </Button>
            <Button variant="destructive" onClick={handleResetSpend} disabled={resetSpendLoading}>
              {t("Reset")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={isBlockModalOpen} onOpenChange={(open) => setIsBlockModalOpen(open)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{isBlocked ? t("Unblock Key") : t("Block Key")}</DialogTitle>
          </DialogHeader>
          <p>
            {isBlocked ? t("Unblock") : t("Block")}{" "}
            <strong>{currentKeyData?.key_alias || currentKeyData?.token_id || t("this key")}</strong>?
          </p>
          <p style={{ color: "#666", fontSize: "0.875rem", marginTop: 8 }}>
            {isBlocked
              ? t("Requests using this key will be accepted again.")
              : t(
                  "Requests using this key will be rejected with a 401 error until it is unblocked. The key is not deleted and can be unblocked at any time.",
                )}
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setIsBlockModalOpen(false)}>
              {t("Cancel")}
            </Button>
            <Button
              variant={isBlocked ? "default" : "destructive"}
              onClick={handleToggleBlocked}
              disabled={blockLoading}
            >
              {isBlocked ? t("Unblock") : t("Block")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <div className="h-full min-h-0">
        <fieldset className="h-full min-h-0 min-w-0" disabled={!canModifyKey}>
          <KeyEditView
            header={editorHeader}
            keyData={currentKeyData}
            onCancel={onClose}
            onSubmit={handleKeyUpdate}
            teams={teams}
            accessToken={accessToken}
            userID={userID}
            userRole={userRole}
            premiumUser={premiumUser}
            canReconcileBudget={canReconcileBudget}
            onBudgetReconciled={refreshBudgetAfterReconciliation}
          />
        </fieldset>
      </div>
    </div>
  );
}
