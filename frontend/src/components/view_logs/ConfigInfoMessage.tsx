import React from "react";
import { useT } from "@/i18n";

/** 按状态提示请求及响应正文不可用的原因；show 控制可见性，未知状态使用中立说明，不修改设置。 */
export const ConfigInfoMessage: React.FC<{ show: boolean; status?: string }> = ({ show, status }) => {
  const t = useT();
  if (!show) return null;
  const states: Record<string, string> = { saved: "logsUX.captureNoDisplay", disabled: "logsUX.captureDisabled", expired: "logsUX.captureExpired", empty: "logsUX.captureEmpty", early_failure: "logsUX.captureEarlyFailure", too_large: "logsUX.captureTooLarge", capacity: "logsUX.captureCapacity", partial: "logsUX.capturePartial", unsupported: "logsUX.captureUnsupported", storage_failed: "logsUX.captureStorageFailed", settings_unavailable: "logsUX.captureStorageFailed" };
  return <div className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="note">{t(states[status ?? ""] ?? "logsUX.bodyUnavailable")}</div>;
};
