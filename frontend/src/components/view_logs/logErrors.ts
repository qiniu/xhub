import type { LogEntry } from "./columns";

/** 判断日志是否失败：兼容后台 error 和旧版 failure 状态、HTTP 错误码及独立错误正文；供表格和详情共享，不改变输入。 */
export function isRequestFailure(log: Pick<LogEntry, "status" | "metadata" | "error">): boolean {
  return [log.status, log.metadata?.status].some((status) =>
    typeof status === "string" && ["error", "failure", "failed"].includes(status.toLowerCase()),
  ) || Number(log.metadata?.http_status) >= 400 || Boolean(log.error?.trim());
}

/** 返回失败详情：新日志保存了正文时优先展示完整响应，其他情况保留旧日志 error 优先的规则；空值回退旧版元数据。 */
export function fullErrorDetails(log: Pick<LogEntry, "error" | "response" | "metadata" | "body_capture_status">): string {
  if (log.body_capture_status === "saved") {
    const response = log.response;
    if (typeof response === "string" && response.trim()) return response;
    if (response && typeof response === "object" && Object.keys(response).length > 0) {
      if ("body" in response && typeof response.body === "string" && Object.keys(response).length === 1) return response.body;
      return JSON.stringify(response, null, 2);
    }
  }
  if (log.error?.trim()) return log.error;
  const response = log.response;
  if (typeof response === "string" && response.trim()) return response;
  if (response && typeof response === "object" && Object.keys(response).length > 0) {
    return JSON.stringify(response, null, 2);
  }
  return log.metadata?.error_information ? JSON.stringify(log.metadata.error_information, null, 2) : "";
}
