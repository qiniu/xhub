import { extractProxyErrorMessage } from "@/lib/http/client";
import type { TranslateVars } from "@/i18n/translate";
import { probeObject, type ProbeKind } from "./probeForm";

/** 探测操作的可翻译反馈；vars 中的模型、变量名和真实上游详情原样插值，不翻译用户内容。 */
export interface ProbeFeedback { key: string; vars?: TranslateVars; failed?: boolean }

const searchCodes = new Set([
  "invalid_request", "unsupported_provider", "missing_credential", "invalid_api_base", "not_found", "store_unavailable",
  "upstream_http_error", "invalid_search_response", "request_timeout", "request_cancelled", "request_failed",
]);

const promptMessages: Record<string, string> = {
  "invalid prompt test request body": "promptInvalidInput",
  "prompt test request must be a JSON object": "promptInvalidInput",
  "invalid prompt test fields": "promptInvalidInput",
  "prompt_id required": "promptMissingID",
  "model must be a non-empty string": "promptMissingModel",
  "model required in saved prompt or test request": "promptMissingModel",
  "streaming prompt tests are not supported": "promptStreamingUnsupported",
  "prompt version selection is not supported": "promptVersionUnsupported",
  "saved prompt must use either messages or content": "promptMixedFormat",
  "invalid saved prompt messages": "promptInvalidMessages",
  "saved prompt messages must contain only text role and content": "promptInvalidMessages",
  "saved prompt text messages required": "promptMessagesRequired",
  "prompt messages require a supported role and non-empty text content": "promptMessageRole",
  "only simple {{name}} prompt variables are supported": "promptVariableSyntax",
  "rendered prompt message must not be empty": "promptEmptyRendered",
  "saved prompt config must be an object": "promptInvalidConfig",
  "prompt config must use only one token limit": "promptTokenLimits",
  "prompt store unavailable": "promptStoreUnavailable",
  "prompt store failure": "promptStoreUnavailable",
  "prompt not found": "promptNotFound",
  "could not encode prompt test request": "promptEncodingFailure",
};

/** 只识别本地提示词校验的固定消息和明确前缀；message 为服务端原文，返回翻译反馈或 null，未知上游文案保持原样。 */
function promptFeedback(message: string): ProbeFeedback | null {
  const fixed = promptMessages[message];
  if (fixed) return { key: `probes.errors.${fixed}`, failed: true };
  const missing = /^missing prompt variable: ([A-Za-z_][A-Za-z0-9_]*)$/.exec(message);
  if (missing) return { key: "probes.errors.promptMissingVariable", vars: { name: missing[1] }, failed: true };
  const unsupported = /^(?:saved )?([A-Za-z_][A-Za-z0-9_]*) is not supported by prompt tests$/.exec(message);
  if (unsupported) return { key: "probes.errors.promptUnsupportedField", vars: { name: unsupported[1] }, failed: true };
  const config = /^(invalid|unsupported) prompt config: (.+)$/.exec(message);
  if (!config) return null;
  const key = config[1] === "invalid" ? "promptConfigValue" : "promptConfigUnsupported";
  return { key: `probes.errors.${key}`, vars: { name: config[2] }, failed: true };
}

/** 读取错误包络中可展示的数值状态；未知类型返回 undefined，避免把任意供应商字符串冒充 HTTP 状态。 */
function responseStatus(value: unknown): number | undefined {
  return typeof value === "number" ? value : undefined;
}

/** 将探测失败转换为界面反馈；kind 限定协议，error 可为 ApiError 或本地异常。已知错误国际化，未知上游原文及 HTTP 状态保留。 */
export function probeErrorFeedback(kind: ProbeKind, error: unknown): ProbeFeedback {
  const detail = extractProxyErrorMessage(error);
  if (detail.startsWith("probes.")) return { key: detail, failed: true };
  const envelope = probeObject(error);
  const body = probeObject(envelope?.body);
  const cause = probeObject(body?.error);
  const code = cause?.code;
  const status = responseStatus(envelope?.status);
  // 只消费搜索测试自己的错误包络，不把供应商恰好同名的错误码当成本地错误翻译。
  const localSearch = kind === "search_tools" && body?.status === "error" && body.error_type === code;
  if (localSearch && typeof code === "string" && searchCodes.has(code)) {
    const upstream = typeof body.upstream_status === "number" ? body.upstream_status : status;
    if (code !== "upstream_http_error" || upstream !== undefined) {
      return { key: `probes.errors.${code}`, vars: { status: upstream }, failed: true };
    }
  }
  const localPrompt = kind === "prompts" && ["invalid_request", "unavailable", "internal", "not_found"].includes(String(cause?.type));
  if (localPrompt) {
    const translated = promptFeedback(detail);
    if (translated) return translated;
  }
  if (status !== undefined) return { key: "probes.errors.http", vars: { status, detail }, failed: true };
  return { key: "probes.failed", vars: { detail }, failed: true };
}
