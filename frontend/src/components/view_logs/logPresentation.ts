import type { LogEntry } from "./columns";

/** 从已记录的错误正文提取短摘要；参数为日志，返回最多 240 字符，供详情首屏使用。
 * 优先结构化 message，未知格式保留原始文本片段；不推断责任方，完整正文仍由原始错误区域展示。
 */
export function logErrorSummary(log: Pick<LogEntry, "error" | "response" | "metadata">): string {
  let payload: unknown = log.error || log.response;
  if (typeof payload === "string") {
    try {
      payload = JSON.parse(payload);
    } catch {
      /* 纯文本错误保留原文。 */
    }
  }
  const object = payload && typeof payload === "object" ? (payload as Record<string, unknown>) : {};
  const error = object.error && typeof object.error === "object" ? (object.error as Record<string, unknown>) : {};
  const candidate = log.metadata?.error_information?.error_message ?? error.message ?? object.message ?? payload;
  if (typeof candidate !== "string") return "";
  const text = candidate.replace(/\s+/g, " ").trim();
  return text.length > 240 ? `${text.slice(0, 240)}…` : text;
}

/** 根据记录中的 HTTP 状态生成中性摘要键；参数为日志，返回翻译键，不把取消或状态码推断为责任方。供列表与详情共用，无副作用。 */
export function logOutcomeKey(log: Pick<LogEntry, "metadata">): string {
  const status = Number(log.metadata?.http_status);
  if (status === 499) return "logsUX.interrupted";
  if (status === 429) return "logsUX.limited";
  if (status === 413) return "logsUX.bodyRejected";
  if (status === 401 || status === 403) return "logsUX.denied";
  if (status >= 500) return "logsUX.serviceFailed";
  return "logsUX.requestFailed";
}

/** 生成列表初始列可见性；参数是错误视图标志，返回列映射。只影响展示，所有字段仍可通过列选择器恢复。 */
export function logColumnVisibility(errorsOnly: boolean): Record<string, boolean> {
  return {
    endpoints: false,
    type: false,
    session_id: false,
    ttft_ms: false,
    tps: false,
    team_alias: false,
    key_hash: false,
    end_user: false,
    request_tags: false,
    spend: !errorsOnly,
    total_tokens: !errorsOnly,
    diagnosis: errorsOnly,
    key_alias: false,
  };
}
