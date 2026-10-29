"use client";
import Link from "next/link";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { useT } from "@/i18n/I18nProvider";

/** 保留旧用量URL作为明确能力说明；无参数，返回已鉴权的迁移入口。
 * 旧/global/spend从未接真实账单，不能显示KV或假零值；不请求旧接口，不改变用户权限或消费。 */
export default function OldUsagePage() {
  useAuthorized();
  const t = useT();
  return (
    <section className="p-6">
      <h1 className="text-2xl font-semibold">{t("legacyUsage.title")}</h1>
      <p role="status" data-testid="old-usage-body">
        {t("legacyUsage.unavailable")}
      </p>
      <Link href="/ui/usage">{t("legacyUsage.openUsage")}</Link>
    </section>
  );
}
