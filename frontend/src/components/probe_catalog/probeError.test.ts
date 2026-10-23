import { describe, expect, it } from "vitest";
import { ApiError } from "@/lib/http/client";
import { translate } from "@/i18n/translate";
import { probeErrorFeedback } from "./probeError";

const searchCodes = ["invalid_request", "unsupported_provider", "missing_credential", "invalid_api_base", "not_found", "store_unavailable", "upstream_http_error", "invalid_search_response", "request_timeout", "request_cancelled", "request_failed"];

describe("probe errors", () => {
  /** 验证每种稳定搜索错误码能完整切换中英文；本地错误仅替换固定说明，原上游状态码保留，无外部调用。 */
  it.each(searchCodes)("localizes search %s in both languages", (code) => {
    const body = { status: "error", error_type: code, upstream_status: 403, error: { code, type: code, message: "local English error" } };
    const feedback = probeErrorFeedback("search_tools", new ApiError("local English error", 403, body));
    expect(feedback.key).toBe(`probes.errors.${code}`);
    expect(feedback.failed).toBe(true);
    const zh = translate("zh-CN", feedback.key, feedback.vars);
    const en = translate("en", feedback.key, feedback.vars);
    expect(zh).not.toContain("local English error");
    expect(en).not.toContain("local English error");
    expect(zh).not.toContain("probes.errors.");
    expect(en).not.toContain("probes.errors.");
    expect(zh).not.toBe(en);
    if (code === "upstream_http_error") {
      expect(zh).toBe("搜索供应商返回 HTTP 403，测试失败。");
      expect(en).toBe("The search provider returned HTTP 403. The test failed.");
    }
  });

  /** 验证缺变量和配置错误使用插值，变量名、字段名保持原样，中文不夹杂本地英文校验句子。 */
  it.each([
    ["missing prompt variable: customer_name", "promptMissingVariable", "customer_name"],
    ["saved tools is not supported by prompt tests", "promptUnsupportedField", "tools"],
    ["version is not supported by prompt tests", "promptUnsupportedField", "version"],
    ["invalid prompt config: temperature", "promptConfigValue", "temperature"],
    ["unsupported prompt config: user_setting", "promptConfigUnsupported", "user_setting"],
  ])("interpolates local prompt error %s", (message, key, name) => {
    const body = { error: { type: "invalid_request", message } };
    const feedback = probeErrorFeedback("prompts", new ApiError(message, 400, body));
    expect(feedback).toEqual({ key: `probes.errors.${key}`, vars: { name }, failed: true });
    expect(translate("zh-CN", feedback.key, feedback.vars)).toContain(name);
    expect(translate("en", feedback.key, feedback.vars)).toContain(name);
  });

  /** 验证固定本地失败、普通异常和已翻译键的边界；响应类型不匹配或未知错误不被脑补成另一种业务失败。 */
  it("maps known local prompt failures but leaves genuine upstream details unchanged", () => {
    const message = "prompt store failure";
    const local = new ApiError(message, 500, { error: { type: "internal", message } });
    expect(probeErrorFeedback("prompts", local).key).toBe("probes.errors.promptStoreUnavailable");
    expect(probeErrorFeedback("prompts", new Error("probes.required"))).toEqual({ key: "probes.required", failed: true });
    const upstream = "Account quota was exceeded: 原始账号名称";
    const error = new ApiError(upstream, 403, { error: { type: "invalid_request", message: upstream } });
    const feedback = probeErrorFeedback("prompts", error);
    expect(translate("zh-CN", feedback.key, feedback.vars)).toBe(`请求失败（HTTP 403）：${upstream}`);
    expect(translate("en", feedback.key, feedback.vars)).toBe(`Request failed (HTTP 403): ${upstream}`);
    expect(probeErrorFeedback("search_tools", new Error(upstream))).toEqual({ key: "probes.failed", vars: { detail: upstream }, failed: true });
  });

  /** 验证同名上游错误码缺本地搜索包络时不能被翻译替换；坏状态包络仍显示原始错误，避免抹掉诊断证据。 */
  it("does not infer local ownership from an upstream error code alone", () => {
    const error = new ApiError("upstream-specific diagnosis", 500, { error: { code: "request_timeout", message: "upstream-specific diagnosis" } });
    expect(probeErrorFeedback("search_tools", error)).toEqual({ key: "probes.errors.http", vars: { status: 500, detail: "upstream-specific diagnosis" }, failed: true });
    const missingStatus = { status: "error", error_type: "upstream_http_error", error: { code: "upstream_http_error" } };
    const feedback = probeErrorFeedback("search_tools", { message: "original", body: missingStatus });
    expect(feedback).toEqual({ key: "probes.failed", vars: { detail: "original" }, failed: true });
  });
});
