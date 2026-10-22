import { useT } from "@/i18n";

/** 个人月度刷新日期选择器；value为空表示批量保持、0不刷新、1至31为日期。
 * 单人及批量编辑调用，onChange返回数值字符串；只更新表单，不直接修改用户消费。 */
export function UserBudgetReset({ id, value, onChange, allowKeep = false, disabled = false }: {
  id?: string; value: string; onChange: (value: string) => void; allowKeep?: boolean; disabled?: boolean;
}) {
  const t = useT();
  return <select id={id} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}
    className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm">
    {allowKeep && <option value="">{t("userBudgetReset.keep")}</option>}
    <option value="0">{t("userBudgetReset.none")}</option>
    {Array.from({ length: 31 }, (_, i) => i + 1).map(day => <option key={day} value={day}>{t("userBudgetReset.day", { day })}</option>)}
  </select>;
}
