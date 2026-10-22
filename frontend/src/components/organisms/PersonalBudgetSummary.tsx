import { useCurrentUser } from "@/app/(dashboard)/hooks/users/useCurrentUser";
import { useT } from "@/i18n";

/** 创建 Key 表单显示当前个人预算快照；无参数，返回双语预算说明。
 * 读取失败或加载中不冒充无限额；金额仅包含已记账消费，后台准入仍校验热费用和祖先限制。 */
export default function PersonalBudgetSummary() {
  const t = useT();
  const { data, isLoading, isError } = useCurrentUser();
  const valid = data && Number.isFinite(data.spend) && (data.max_budget === null || Number.isFinite(data.max_budget));
  let message = t("quotaGuide.personalUnavailable");
  if (isLoading) message = t("quotaGuide.personalLoading");
  else if (!isError && valid) {
    message = t("quotaGuide.personalSummary", {
      limit: data.max_budget === null ? t("quotaGuide.personalUnlimited") : `$${data.max_budget.toFixed(2)}`,
      spent: `$${data.spend.toFixed(2)}`,
      remaining:
        data.max_budget === null ? t("quotaGuide.personalUnlimited") : `$${(data.max_budget - data.spend).toFixed(2)}`,
    });
  }
  return (
    <section aria-label={t("quotaGuide.personalTitle")} className="mb-4 rounded border p-3 text-sm">
      <p className="font-medium">{t("quotaGuide.personalTitle")}</p>
      <p role="status">{message}</p>
      <p className="mt-1 text-muted-foreground">{t("quotaGuide.personalNote")}</p>
    </section>
  );
}
