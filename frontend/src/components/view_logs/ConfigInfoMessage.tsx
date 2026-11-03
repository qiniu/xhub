import React from "react";
import { useT } from "@/i18n";

/** 提示历史日志未提供请求及响应正文；show 控制可见性，返回双语说明，不推测历史配置或修改设置。 */
export const ConfigInfoMessage: React.FC<{ show: boolean }> = ({ show }) => {
  const t = useT();
  if (!show) return null;
  return <div className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="note">{t("logsUX.bodyUnavailable")}</div>;
};
