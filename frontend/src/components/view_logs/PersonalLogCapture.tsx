"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getPersonalLogCapture, setPersonalLogCapture } from "@/components/networking";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/i18n";

/** 展示当前用户的正文记录开关、全局覆盖与保留期限；凭据只发送到后台，不推断日志权限。 */
export function PersonalLogCapture({ accessToken }: { accessToken: string }) {
  const t = useT();
  const client = useQueryClient();
  const key = ["personal-log-capture", accessToken];
  const settings = useQuery({ queryKey: key, queryFn: () => getPersonalLogCapture(accessToken), enabled: !!accessToken });
  const update = useMutation({ mutationFn: (enabled: boolean) => setPersonalLogCapture(accessToken, enabled), onSuccess: (data) => client.setQueryData(key, data) });
  if (settings.isLoading) return <p className="mb-4 text-sm text-muted-foreground">{t("logsUX.captureLoading")}</p>;
  if (settings.isError || !settings.data) return <p role="alert" className="mb-4 text-sm text-destructive">{t("logsUX.captureLoadFailed")} <button type="button" className="underline" onClick={() => void settings.refetch()}>{t("logsUX.captureRetry")}</button></p>;
  const data = settings.data;
  /** 将后台提供的字节数按 MiB 格式化；仅显示，不改变后台计数。 */
  const mb = (n: number) => (n / (1024 * 1024)).toFixed(1);
  return (
    <section className="mb-4 rounded-lg border bg-card px-4 py-3" aria-label={t("logsUX.captureTitle")}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <label htmlFor="personal-log-capture" className="font-medium">{t("logsUX.captureTitle")}</label>
        <Switch id="personal-log-capture" checked={data.enabled} disabled={update.isPending} onCheckedChange={(enabled) => update.mutate(enabled)} />
      </div>
      <p className="mt-1 text-sm text-muted-foreground">{t("logsUX.captureDescription", { days: data.retention_days })}</p>
      <p className="mt-1 text-sm text-muted-foreground">{t("logsUX.captureUsage", { used: mb(data.used_bytes), limit: mb(data.limit_bytes) })}</p>
      {data.global_enabled && <p className="mt-1 text-sm text-amber-700">{t("logsUX.captureGlobal", { days: data.global_retention_days })}</p>}
      {update.isError && <p role="alert" className="mt-1 text-sm text-destructive">{t("logsUX.captureSaveFailed")}</p>}
    </section>
  );
}
