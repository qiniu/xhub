import { useT } from "@/i18n";

/** 展示当前Skill发布与安装支持边界；无参数或副作用，目录、详情和发布弹窗复用，不由enabled推断执行能力。 */
export default function SkillSupportNotice() {
  const t = useT();
  return (
    <section className="rounded-lg border border-border bg-muted/40 p-4" aria-label={t("skillHub.supportTitle")}>
      <h2 className="font-medium">{t("skillHub.supportTitle")}</h2>
      <p className="mt-2 text-sm text-muted-foreground">{t("skillHub.supportHelp")}</p>
    </section>
  );
}
