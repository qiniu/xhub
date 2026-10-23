import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { apiClient } from "@/components/networking";
import { I18nProvider, useI18n } from "@/i18n/I18nProvider";
import { translate, type Locale } from "@/i18n/translate";
import { ProbeCatalog } from "./ProbeCatalog";
import type { ProbeKind } from "./probeForm";
import { ApiError } from "@/lib/http/client";

vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: vi.fn(() => ({ accessToken: "session" })) }));
vi.mock("@/components/networking", () => ({ apiClient: { get: vi.fn(), request: vi.fn(), post: vi.fn(), delete: vi.fn() } }));

/** 在真实国际化上下文渲染组件；locale/kind 指定页面，网络边界由单测替身控制，RTL 自动卸载清理。 */
function renderCatalog(locale: Locale, kind: ProbeKind) {
  return render(<I18nProvider initialLocale={locale}><ProbeCatalog kind={kind} /></I18nProvider>);
}

/** 为组件测试生成有效保存记录；同一数据驱动列表/详情响应，避免用复制实现的分支冒充验证。 */
function promptRecord() {
  return { prompt_id: "prompt-id", name: "Existing prompt", model: "model-id", messages: [{ role: "user", content: "Hello {{name}}" }] };
}

/** 清除 HTTP 替身及语言存储；每个测试只使用隔离的内存记录，无后台持久化或外部服务。 */
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(useAuthorized).mockReturnValue({ accessToken: "session" } as ReturnType<typeof useAuthorized>);
  vi.mocked(apiClient.get).mockResolvedValue({ data: [] });
  localStorage.clear();
  document.cookie = "xhub_locale=; Max-Age=0; path=/";
});

describe.each<Locale>(["en", "zh-CN"])("probe catalog %s", (locale) => {
  /** 验证真实错误包络在两种语言页面完整翻译，保留上游 HTTP 状态和动态变量；测试响应失败不能出现完成提示。 */
  it.each([
    { kind: "search_tools" as const, code: "upstream_http_error", message: "search provider returned an unsuccessful HTTP status", key: "upstream_http_error", vars: { status: 403 } },
    { kind: "search_tools" as const, code: "request_timeout", message: "search provider request timed out", key: "request_timeout", vars: {} },
    { kind: "prompts" as const, code: "invalid_request", message: "missing prompt variable: customer_name", key: "promptMissingVariable", vars: { name: "customer_name" } },
  ])("localizes $kind $code error responses", async ({ kind, code, message, key, vars }) => {
    const user = userEvent.setup();
    const tr = (translationKey: string) => translate(locale, translationKey);
    const record = kind === "prompts" ? promptRecord() : { search_tool_id: "prompt-id", name: "Existing prompt", litellm_params: { search_provider: "tavily", api_key: "********" } };
    vi.mocked(apiClient.get).mockImplementation(async (path) => path.endsWith("/list") ? { data: [record] } : record);
    const body = kind === "prompts" ? { error: { type: code, message } } : { status: "error", error_type: code, upstream_status: 403, error: { code, type: code, message } };
    vi.mocked(apiClient.post).mockRejectedValue(new ApiError(message, 400, body));
    renderCatalog(locale, kind);
    await screen.findByRole("option", { name: "Existing prompt" });
    await user.selectOptions(screen.getByLabelText(tr("probes.record")), "prompt-id");
    await screen.findByRole("button", { name: tr("probes.save") });
    await user.click(screen.getByRole("button", { name: tr("probes.test") }));
    expect(await screen.findByRole("alert")).toHaveTextContent(translate(locale, `probes.errors.${key}`, vars));
    expect(screen.queryByText(tr("probes.completed"))).not.toBeInTheDocument();
    expect(screen.queryByTestId("probe-result")).not.toBeInTheDocument();
  });

  /** 验证创建的真实请求正文、变量输入和 completion 展示；用户文本不翻译，配置变动会撤销旧成功并阻止未保存测试。 */
  it("saves executable prompts and rejects stale or placeholder test results", async () => {
    const user = userEvent.setup();
    const tr = (key: string) => translate(locale, key);
    const record = promptRecord();
    vi.mocked(apiClient.request).mockResolvedValue(record);
    vi.mocked(apiClient.post).mockResolvedValue({ choices: [{ message: { content: "实际输出" } }], usage: { prompt_tokens: 8, completion_tokens: 2 } });
    renderCatalog(locale, "prompts");
    fireEvent.change(screen.getByLabelText(tr("probes.name")), { target: { value: "用户名称" } });
    fireEvent.change(screen.getByLabelText(tr("probes.model")), { target: { value: "model-id" } });
    fireEvent.change(screen.getByLabelText(tr("probes.messages")), { target: { value: '[{"role":"user","content":"用户 {{name}}"}]' } });
    await user.click(screen.getByRole("button", { name: tr("probes.create") }));
    expect(await screen.findByText(tr("probes.saved"))).toBeInTheDocument();
    expect(apiClient.request).toHaveBeenCalledWith("POST", "/prompts", { accessToken: "session", body: { prompt_id: "用户名称", name: "用户名称", model: "model-id", messages: [{ role: "user", content: "用户 {{name}}" }] } });
    fireEvent.change(screen.getByLabelText(tr("probes.variables")), { target: { value: '{"name":"张三"}' } });
    await user.click(screen.getByRole("button", { name: tr("probes.test") }));
    expect(await screen.findByText(tr("probes.completed"))).toBeInTheDocument();
    expect(apiClient.post).toHaveBeenCalledWith("/prompts/test", { accessToken: "session", body: { prompt_id: "prompt-id", prompt_variables: { name: "张三" } } });
    expect(screen.getByTestId("probe-result")).toHaveTextContent("实际输出");
    expect(screen.getByText(translate(locale, "probes.usage", { input: 8, output: 2 }))).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(tr("probes.model")), { target: { value: "different-model" } });
    expect(screen.queryByTestId("probe-result")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: tr("probes.test") }));
    expect(await screen.findByRole("alert")).toHaveTextContent(tr("probes.dirty"));
    expect(apiClient.post).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: tr("probes.save") }));
    await screen.findByText(tr("probes.saved"));
    vi.mocked(apiClient.post).mockResolvedValue({ status: "succeeded", output: "" });
    await user.click(screen.getByRole("button", { name: tr("probes.test") }));
    expect(await screen.findByRole("alert")).toHaveTextContent(tr("probes.invalidResponse"));
    expect(screen.queryByText(tr("probes.completed"))).not.toBeInTheDocument();
    expect(screen.queryByTestId("probe-result")).not.toBeInTheDocument();
  });

  /** 验证详情读回、搜索密钥留空更新、真实结果计数、失败状态及删除；模拟的仅是 HTTP 边界，真实外部链路由 E2E 验证。 */
  it("reads and edits search configuration without exposing keys and shows upstream failure", async () => {
    const user = userEvent.setup();
    const tr = (key: string) => translate(locale, key);
    const record = { search_tool_id: "search-id", name: "搜索配置", litellm_params: { search_provider: "serper", api_key: "********", api_base: "https://example.test/search", region: "us" } };
    vi.mocked(apiClient.get).mockImplementation(async (path) => path.endsWith("/list") ? { data: [record] } : record);
    vi.mocked(apiClient.request).mockResolvedValue(record);
    vi.mocked(apiClient.post).mockResolvedValue({ status: "success", results_count: 0, results: [] });
    vi.mocked(apiClient.delete).mockResolvedValue({ deleted: true });
    renderCatalog(locale, "search_tools");
    await screen.findByRole("option", { name: "搜索配置" });
    await user.selectOptions(screen.getByLabelText(tr("probes.record")), "search-id");
    await screen.findByRole("button", { name: tr("probes.save") });
    expect(apiClient.get).toHaveBeenCalledWith("/search_tools/search-id", { accessToken: "session" });
    expect(screen.getByLabelText(tr("probes.apiKey"))).toHaveValue("");
    fireEvent.change(screen.getByLabelText(tr("probes.apiBase")), { target: { value: "https://new.example.test/search" } });
    await user.click(screen.getByRole("button", { name: tr("probes.save") }));
    await screen.findByText(tr("probes.saved"));
    expect(apiClient.request).toHaveBeenCalledWith("PUT", "/search_tools/search-id", { accessToken: "session", body: { name: "搜索配置", search_tool_name: "搜索配置", litellm_params: { search_provider: "serper", api_base: "https://new.example.test/search", region: "us" } } });
    fireEvent.change(screen.getByLabelText(tr("probes.query")), { target: { value: "用户搜索词" } });
    await user.click(screen.getByRole("button", { name: tr("probes.test") }));
    expect(await screen.findByText(tr("probes.completed"))).toBeInTheDocument();
    expect(screen.getByText(translate(locale, "probes.resultsCount", { count: 0 }))).toBeInTheDocument();
    expect(apiClient.post).toHaveBeenCalledWith("/search_tools/test_connection", { accessToken: "session", body: { search_tool_id: "search-id", query: "用户搜索词" } });
    vi.mocked(apiClient.post).mockRejectedValue(new Error("provider returned HTTP 403"));
    await user.click(screen.getByRole("button", { name: tr("probes.test") }));
    expect(await screen.findByRole("alert")).toHaveTextContent("provider returned HTTP 403");
    expect(screen.queryByTestId("probe-result")).not.toBeInTheDocument();
    expect(screen.queryByText(tr("probes.completed"))).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: tr("probes.delete") }));
    expect(await screen.findByText(tr("probes.deleted"))).toBeInTheDocument();
    expect(apiClient.delete).toHaveBeenCalledWith("/search_tools/search-id", { accessToken: "session" });
    expect(screen.getByLabelText(tr("probes.apiKey"))).toHaveValue("");
  });

  /** 验证缺配置或非法变量时不会发送无效探测，列表故障明确显示失败而不伪装成空目录；无需后台清理。 */
  it("rejects invalid input and does not disguise a failed list as empty", async () => {
    const user = userEvent.setup();
    const tr = (key: string) => translate(locale, key);
    vi.mocked(apiClient.get).mockRejectedValue(new Error("catalog store failure"));
    renderCatalog(locale, "prompts");
    expect(await screen.findByRole("alert")).toHaveTextContent("catalog store failure");
    expect(screen.queryByText(tr("probes.noRecords"))).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: tr("probes.create") }));
    expect(await screen.findByRole("alert")).toHaveTextContent(tr("probes.required"));
    expect(apiClient.request).not.toHaveBeenCalled();
    expect(apiClient.post).not.toHaveBeenCalled();
  });
});

/** 测试专用语言切换入口；调用正式上下文 API，不替换或扫描 DOM 文案。 */
function LocaleSwitch() {
  const { setLocale } = useI18n();
  return <button onClick={() => setLocale("zh-CN")}>Switch locale</button>;
}

/** 验证已编辑表单切换中英文后所有本区字段同步变更，用户实际正文和英文默认示例保持原文。 */
it("switches locale without rewriting input or example prompts", async () => {
  const user = userEvent.setup();
  render(<I18nProvider initialLocale="en"><LocaleSwitch /><ProbeCatalog kind="prompts" /></I18nProvider>);
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "原始名称" } });
  await user.click(screen.getByRole("button", { name: "Switch locale" }));
  expect(screen.getByLabelText("名称")).toHaveValue("原始名称");
  expect(screen.getByLabelText("消息（JSON）")).toHaveValue(JSON.stringify([{ role: "user", content: "Say hello to {{name}}." }], null, 2));
  expect(screen.getByRole("button", { name: "执行测试" })).toBeInTheDocument();
  expect(screen.queryByLabelText("Messages (JSON)")).not.toBeInTheDocument();
});

/** 验证登录信息尚在加载时不制造会话丢失提示；用户主动操作时仍明确拒绝无身份请求。 */
it("waits for the session and guards manual actions without it", async () => {
  vi.mocked(useAuthorized).mockReturnValue({ accessToken: null } as ReturnType<typeof useAuthorized>);
  renderCatalog("en", "prompts");
  expect(apiClient.get).not.toHaveBeenCalled();
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Create" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("A login session is required.");
  expect(apiClient.request).not.toHaveBeenCalled();
});

/** 验证非法变量、保存失败和删除失败保留原记录且无成功提示；读取使用有效详情，测试结束只清理内存替身。 */
it("does not claim success or forget a record when validation or persistence fails", async () => {
  const user = userEvent.setup();
  const record = promptRecord();
  vi.mocked(apiClient.get).mockImplementation(async (path) => path.endsWith("/list") ? { data: [record] } : record);
  vi.mocked(apiClient.request).mockRejectedValue(new Error("catalog write failed"));
  vi.mocked(apiClient.delete).mockRejectedValue(new Error("catalog delete failed"));
  renderCatalog("en", "prompts");
  await screen.findByRole("option", { name: "Existing prompt" });
  await user.selectOptions(screen.getByLabelText("Saved configuration"), "prompt-id");
  await screen.findByRole("button", { name: "Save changes" });
  fireEvent.change(screen.getByLabelText("Prompt variables (JSON)"), { target: { value: "[]" } });
  await user.click(screen.getByRole("button", { name: "Run test" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Prompt variables must be a JSON object.");
  expect(apiClient.post).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "Save changes" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("catalog write failed");
  expect(screen.queryByText("Configuration saved. Run a test to verify it works.")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Delete" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("catalog delete failed");
  expect(screen.getByLabelText("Saved configuration")).toHaveValue("prompt-id");
  expect(screen.queryByText("Configuration deleted.")).not.toBeInTheDocument();
});

/** 验证 200 但缺列表结构也必须显示契约失败，不能把旧占位或坏响应当作可编辑空目录。 */
it("rejects a successful HTTP response with an invalid list contract", async () => {
  vi.mocked(apiClient.get).mockResolvedValue({ status: "success" });
  renderCatalog("en", "search_tools");
  expect(await screen.findByRole("alert")).toHaveTextContent("The server returned an invalid configuration.");
  expect(screen.queryByText("No saved configurations.")).not.toBeInTheDocument();
});
