import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { LogEntry } from "../columns";
import { LogDetailContent } from "./LogDetailContent";
import { I18nProvider } from "@/i18n";
import { translate } from "@/i18n/translate";

/**
 * 为上游响应折叠组件测试构造最小日志；参数为覆盖字段，返回独立 LogEntry。
 * 仅供本文件渲染使用，不调用后台或持久化数据，测试结束由自动卸载清理。
 */
function logEntry(overrides: Partial<LogEntry> = {}): LogEntry {
  return {
    request_id: "collapse-test", model: "test-model", call_type: "chat",
    startTime: "2026-10-10T00:00:00Z", endTime: "2026-10-10T00:00:01Z",
    messages: [], response: {}, request_tags: {},
    metadata: { upstream_response: { status_code: 200, headers: { "X-Trace": ["complete"] }, usage_reported: false } },
    ...overrides,
  } as LogEntry;
}

describe("上游响应折叠", () => {
  /** 有真实首响应字节耗时时展示正确口径，中英文均不将其误称首 Token；纯组件测试，自动卸载清理。 */
  it.each(["zh-CN", "en"] as const)("%s 显示首响应字节耗时", (locale) => {
    render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({ ttft_ms: 1200 })} /></I18nProvider>);
    expect(screen.getByText(translate(locale, "logsUX.firstResponse"))).toBeVisible();
  });
  /** 前置后台未定价或真实免费零值，验证中英文指标与费用明细语义一致；纯组件渲染，自动卸载清理。 */
  it.each(["zh-CN", "en"] as const)("%s 指标区分未知费用和真实免费", (locale) => {
    const sample = logEntry({ spend: 0, cache_hit: false, metadata: { cost_breakdown: { pricing_status: "unpriced", total_cost: 0 } } });
    const { rerender } = render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={sample} /></I18nProvider>);
    const costLabel = locale === "en" ? "Cost" : "费用";
    expect(screen.getByText(costLabel, { exact: true }).parentElement).toHaveTextContent("-");
    expect(screen.getByText(translate(locale, "Pricing unavailable"))).toBeVisible();
    rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={{ ...sample, cache_hit: true }} /></I18nProvider>);
    expect(screen.getByText(costLabel, { exact: true }).parentElement).toHaveTextContent("$0.00");
  });
  /** 前置日志凭证与协议不同；验证中英文标签及原始凭证名称，空凭证不误用协议；自动卸载清理。 */
  it.each(["zh-CN", "en"] as const)("%s 请求详情显示凭证名称", (locale) => {
    const { rerender } = render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({
      credential_name: "七牛-production", api_base: "https://upstream.example/v1", custom_llm_provider: "openai",
    })} /></I18nProvider>);
    const label = locale === "zh-CN" ? "凭证名称" : "Credential Name";
    expect(screen.getByText(label)).toBeVisible();
    expect(screen.getByText("七牛-production")).toBeVisible();
    expect(screen.getByText("https://upstream.example/v1")).toBeVisible();
    expect(screen.queryByText("openai")).not.toBeInTheDocument();
    rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({ custom_llm_provider: "openai" })} /></I18nProvider>);
    expect(screen.getByText(label).parentElement).toHaveTextContent("-");
    expect(screen.queryByText("openai")).not.toBeInTheDocument();
  });
  /** 前置含普通请求备注，验证中英文原样显示并在无备注时隐藏；自动卸载，无后台数据。 */
  it.each(["zh-CN", "en"] as const)("%s 日志显示通用备注", (locale) => {
    const note = "make e2e Affinity | 原生端点独立七牛偏好";
    const { rerender } = render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({ label: note })} /></I18nProvider>);
    expect(screen.getByRole("region", { name: locale === "zh-CN" ? "备注" : "Label" })).toHaveTextContent(note);
    rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry()} /></I18nProvider>);
    expect(screen.queryByText(note)).not.toBeInTheDocument();
  });
  /** 前置正常诊断日志；验证默认折叠、鼠标展开和键盘收起及未上报用量提示；自动卸载清理。 */
  it("默认折叠并支持点击展开和键盘收起", async () => {
    const user = userEvent.setup();
    render(<LogDetailContent logEntry={logEntry()} />);
    const region = screen.getByRole("region", { name: "Upstream Response" });
    const trigger = within(region).getByRole("button", { name: "Upstream Response", exact: true });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(within(region).queryByText("HTTP Status: 200")).not.toBeInTheDocument();
    expect(within(region).getByRole("button", { name: "Copy upstream response" })).toBeVisible();
    await user.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(within(region).getByText("HTTP Status: 200")).toBeVisible();
    expect(within(region).getByText("Not reported")).toBeVisible();
    await user.click(within(region).getByText("View raw response headers and usage"));
    expect(within(region).getByText("{}")).toBeVisible();
    trigger.focus();
    await user.keyboard("{Enter}");
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(within(region).queryByText("HTTP Status: 200")).not.toBeInTheDocument();
  });

  /** 前置旧日志无诊断字段；验证不渲染空折叠区域且请求详情仍显示；自动卸载清理。 */
  it("无上游诊断时不显示折叠区域", () => {
    render(<LogDetailContent logEntry={logEntry({ metadata: {} })} />);
    expect(screen.queryByRole("region", { name: "Upstream Response" })).not.toBeInTheDocument();
    expect(screen.getByText("Request Details")).toBeVisible();
  });

  /** 前置失败响应且用量解析出错；验证错误主提示不受折叠影响、展开后显示解析失败与尾部响应头；自动卸载清理。 */
  it("失败诊断展开后完整可见", async () => {
    const user = userEvent.setup();
    render(<LogDetailContent logEntry={logEntry({ status: "error", error: "upstream failed", metadata: {
      http_status: 502, upstream_response: { status_code: 502, headers: {}, trailers: { "X-Trailer": ["last"] },
        usage_reported: false, usage_parse_error: "invalid upstream usage" },
    } })} />);
    expect(screen.getByText("Service error")).toBeVisible();
    expect(screen.getByRole("region", { name: "Full Error Details" })).toHaveTextContent("upstream failed");
    const region = screen.getByRole("region", { name: "Upstream Response" });
    expect(within(region).queryByRole("alert")).not.toBeInTheDocument();
    await user.click(within(region).getByRole("button", { name: "Upstream Response", exact: true }));
    expect(within(region).getByRole("alert")).toHaveTextContent("invalid upstream usage");
    await user.click(within(region).getByText("View raw response headers and usage"));
    expect(region).toHaveTextContent("last");
  });

  /** 前置同一详情组件连续展示两个请求；验证下一请求重新默认折叠；自动卸载，无数据副作用。 */
  it("切换请求时重置为折叠", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<LogDetailContent logEntry={logEntry()} />);
    await user.click(screen.getByRole("button", { name: "Upstream Response", exact: true }));
    rerender(<LogDetailContent logEntry={logEntry({ request_id: "next-request" })} />);
    expect(screen.getByRole("button", { name: "Upstream Response", exact: true })).toHaveAttribute("aria-expanded", "false");
  });
});

/** 前置包含上游和计费用量的日志；双语验证零值、缺失、上游请求 ID 与原始详情，卸载组件清理。 */
it.each(["zh-CN", "en"] as const)("%s 以可读字段显示上游用量", async (locale) => {
  const user = userEvent.setup();
  const info = { status_code: 200, headers: { "X-Request-ID": ["upstream-id"] }, usage_reported: true,
    usage: { input_tokens: 0, output_tokens: 15, total_tokens: 15, input_tokens_details: { cached_tokens: 0 }, extra: "original" }, billing_usage: { prompt_tokens: 12, completion_tokens: 3, cache_creation_input_tokens: 4 } };
  const { rerender } = render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({ metadata: { upstream_response: info } })} /></I18nProvider>);
  const region = screen.getByRole("region", { name: translate(locale, "Upstream Response") });
  await user.click(within(region).getByRole("button", { name: translate(locale, "Upstream Response"), exact: true }));
  expect(region).toHaveTextContent(translate(locale, "logsUX.upstreamSummary"));
  expect(region).toHaveTextContent("upstream-id");
  const upstream = within(region).getByRole("region", { name: translate(locale, "Upstream Usage") });
  expect(upstream).toHaveTextContent(translate(locale, "logsUX.inputTokens"));
  expect(upstream).toHaveTextContent("0");
  expect(upstream).toHaveTextContent("15");
  expect(upstream).toHaveTextContent(translate(locale, "logsUX.cacheReadTokens"));
  const billing = within(region).getByRole("region", { name: translate(locale, "Billing Usage") });
  expect(billing).toHaveTextContent("12");
  expect(billing).toHaveTextContent("3");
  expect(billing).toHaveTextContent(translate(locale, "logsUX.cacheWriteTokens"));
  await user.click(within(region).getByText(translate(locale, "logsUX.upstreamRaw")));
  expect(within(region).getByText(/original/)).toBeVisible();
  rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({ request_id: "other", metadata: { upstream_response: { status_code: 0, headers: {}, usage_reported: false } } })} /></I18nProvider>);
  await user.click(screen.getByRole("button", { name: translate(locale, "Upstream Response"), exact: true }));
  expect(within(screen.getByRole("region", { name: translate(locale, "Upstream Response") })).getByText(translate(locale, "Not reported"))).toBeVisible();
  expect(screen.getByRole("region", { name: translate(locale, "Upstream Response") })).toHaveTextContent(translate(locale, "logDiagnostics.noResponse"));
});

/** 前置带缓存用量和响应缓存标识的日志；验证问号提示随语言切换且不把价格折扣当作保证，组件自动卸载。 */
it.each(["zh-CN", "en"] as const)("%s 缓存解释按语言显示", async (locale) => {
  const user = userEvent.setup();
  render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({ cache_hit: "true", cache_key: "fixture", metadata: { cached_tokens: 10, additional_usage_values: { cache_creation_input_tokens: 3 } } })} /></I18nProvider>);
  for (const [labelKey, helpKey] of [["Response Cache", "logsUX.responseCacheHelp"], ["Cache Key", "logsUX.cacheKeyHelp"], ["Prompt Cache Read Tokens", "logsUX.promptCacheReadHelp"], ["Prompt Cache Creation Tokens", "logsUX.promptCacheWriteHelp"]]) {
    const label = translate(locale, labelKey);
    const icon = screen.getByRole("img", { name: translate(locale, "{label} info", { label }) });
    await user.hover(icon);
    expect(await screen.findByText(translate(locale, helpKey))).toBeVisible();
    await user.unhover(icon);
  }
});

/** 前置带降级诊断的日志组件；验证中英文提示和能力插值在折叠时也可见，旧日志无提示；自动卸载，无持久副作用。 */
it.each(["zh-CN","en"] as const)("%s displays the web search downgrade reason", (locale) => {
  const {rerender}=render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({metadata:{upstream_response:{status_code:200,headers:{},usage_reported:true,dropped_capabilities:["web_search"]}}})} /></I18nProvider>);
  expect(screen.getByRole("status")).toHaveTextContent("web_search");
  expect(screen.getByRole("status")).toHaveTextContent(locale==="en"?"This request did not use web search":"本次请求未使用联网搜索");
  rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry()} /></I18nProvider>);
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});

/** 前置已采集或历史缺省的推理强度；双语验证原值、协议参数和未知提示，费用保持原始精度，渲染后自动卸载。 */
it.each(["en", "zh-CN"] as const)("%s 请求详情展示明确推理强度及未知历史", (locale) => {
  const { rerender } = render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({reasoning_effort:"high",spend:0.00308641})} /></I18nProvider>);
  const label = locale === "en" ? "Reasoning effort" : "推理强度";
  expect(screen.getByText(label)).toBeVisible();
  expect(screen.getByText("high (reasoning_effort=high)")).toBeVisible();
  expect(screen.getAllByText("$0.00308641").length).toBeGreaterThan(0);
  rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({reasoning_effort:"none"})} /></I18nProvider>);
  expect(screen.getByText("none (reasoning_effort=none)")).toBeVisible();
  rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry()} /></I18nProvider>);
  expect(screen.getByText(locale === "en" ? "Not recorded" : "未记录")).toBeVisible();
  expect(screen.queryByText("high (reasoning_effort=high)")).not.toBeInTheDocument();
});

/** 前置取消日志含独立传输事实；验证中英文、上游证据缺失提示和原错误不被翻译，自动卸载清理。 */
it.each(["zh-CN", "en"] as const)("%s 取消诊断不推断用户停止或供应商正常", (locale) => {
 const failure={kind:"request_canceled",phase:"response_transfer",cancellation_source:"unknown",elapsed_ms:60123,transport_error:"context canceled"};
 const {rerender}=render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({metadata:{upstream_response:{status_code:0,failure}}})} /></I18nProvider>);
 const region=screen.getByRole("region",{name:translate(locale,"logDiagnostics.title")});
 expect(region).toHaveTextContent(translate(locale,"logDiagnostics.cancelUnknown"));
 expect(region).toHaveTextContent(translate(locale,"logDiagnostics.noUpstreamError"));
 expect(region).toHaveTextContent("60123");
 rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({metadata:{upstream_response:{status_code:200,failure,error:{error:{code:"vendor_disconnect",message:"Original vendor error"}}}}})} /></I18nProvider>);
 expect(region).toHaveTextContent("Original vendor error");
 expect(region).not.toHaveTextContent(translate(locale,"logDiagnostics.noUpstreamError"));
});

/** 写客户端失败日志以中英文说明未知来源；普通日志不显示该提示，组件自动清理。 */
it.each(["zh-CN", "en"] as const)("%s 客户端写入失败不推断责任", (locale) => {
  const failure = {phase:"client_write",kind:"client_write_error",transport_error:"synthetic write failure"};
  const {rerender} = render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({metadata:{upstream_response:{status_code:200,failure}}})} /></I18nProvider>);
  const region = screen.getByRole("region", {name:translate(locale,"logDiagnostics.title")});
  expect(region).toHaveTextContent(translate(locale,"logDiagnostics.clientWriteError"));
  expect(region).toHaveTextContent("synthetic write failure");
  rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry()} /></I18nProvider>);
  expect(screen.queryByText(translate(locale,"logDiagnostics.clientWriteError"))).not.toBeInTheDocument();
});

/** 前置读取失败与旧日志；中英文验证正文省略说明、字节证据和原始错误，旧日志不误标；组件自动卸载。 */
it.each(["zh-CN", "en"] as const)("%s 读取失败明确说明正文未保存", (locale) => {
  const failure = {phase: "request_body", kind: "body_too_large", bytes_read: 67108864, body_limit: 67108864, request_body_omitted: true, transport_error: "http: request body too large"};
  const {rerender} = render(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({metadata:{upstream_response:{status_code:0,failure}}})} /></I18nProvider>);
  const region = screen.getByRole("region", {name:translate(locale,"logDiagnostics.title")});
  expect(region).toHaveTextContent(translate(locale,"logDiagnostics.bodyOmitted"));
  expect(region).toHaveTextContent("67108864");
  expect(region).toHaveTextContent("http: request body too large");
  rerender(<I18nProvider initialLocale={locale}><LogDetailContent logEntry={logEntry({metadata:{upstream_response:{status_code:0,failure:{...failure,request_body_omitted:undefined}}}})} /></I18nProvider>);
  expect(screen.queryByText(translate(locale,"logDiagnostics.bodyOmitted"))).not.toBeInTheDocument();
});
