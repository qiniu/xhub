import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { t } from "@/i18n";
import { LogDetailsDrawer } from "./LogDetailsDrawer";
import { sessionSpendLogsCall } from "../../networking";
import { LogEntry } from "../columns";
import { AutoRouterModelGroupsProvider } from "@/components/shared/table_cells";

const detailCapture = vi.hoisted(() => ({ error: false, retry: vi.fn(), data: null as Record<string, unknown> | null, render: vi.fn(), header: vi.fn() }));

vi.mock("../../networking", () => ({
  sessionSpendLogsCall: vi.fn(),
}));

vi.mock("@/app/(dashboard)/hooks/logDetails/useLogDetails", () => ({
  useLogDetails: () => ({ data: detailCapture.data, isLoading: false, isError: detailCapture.error, refetch: detailCapture.retry }),
}));

vi.mock("./LogDetailContent", () => ({
  LogDetailContent: (props: unknown) => { detailCapture.render(props); return null; },
  GuardrailJumpLink: () => null,
}));

vi.mock("./DrawerHeader", () => ({
  DrawerHeader: (props: unknown) => { detailCapture.header(props); return null; },
}));

vi.mock("@/app/(dashboard)/hooks/models/useModels", () => ({
  useAutoRouterModelGroups: vi.fn(() => new Set(["smart-router"])),
}));

const makeLog = (overrides: Partial<LogEntry>): LogEntry => ({
  request_id: "req",
  api_key: "",
  team_id: "",
  model: "",
  model_id: "",
  call_type: "acompletion",
  spend: 0,
  total_tokens: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  startTime: "2026-07-08T10:00:00.000Z",
  endTime: "2026-07-08T10:00:01.000Z",
  cache_hit: "false",
  messages: [],
  response: {},
  ...overrides,
});

const sessionLogs = [
  makeLog({
    request_id: "llm-early",
    model: "llm-early",
    startTime: "2026-07-08T10:00:00.000Z",
    endTime: "2026-07-08T10:00:02.000Z",
  }),
  makeLog({
    request_id: "mcp-early",
    model: "tool-early",
    call_type: "call_mcp_tool",
    startTime: "2026-07-08T10:00:01.000Z",
    endTime: "2026-07-08T10:00:06.000Z",
  }),
  makeLog({
    request_id: "llm-late",
    model: "llm-late",
    startTime: "2026-07-08T10:00:02.000Z",
    endTime: "2026-07-08T10:00:05.000Z",
  }),
  makeLog({
    request_id: "mcp-late",
    model: "tool-late",
    call_type: "call_mcp_tool",
    startTime: "2026-07-08T10:00:03.000Z",
    endTime: "2026-07-08T10:00:03.500Z",
  }),
];

/** 渲染隔离会话抽屉；参数 logs 为列表夹具，返回重渲染入口；供侧栏测试调用，DOM 由框架清理。 */
const renderSessionDrawer = (logs = sessionLogs) => {
  vi.mocked(sessionSpendLogsCall).mockResolvedValue({ data: logs, total: logs.length, total_pages: 1 });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const drawer = (open: boolean) => (
    <QueryClientProvider client={queryClient}>
      <LogDetailsDrawer
        open={open}
        onClose={() => {}}
        logEntry={null}
        sessionId="session-1"
        sessionApiKey="key-1"
        sessionUserId="user-1"
        accessToken="token"
      />
    </QueryClientProvider>
  );
  const { rerender } = render(drawer(true));
  return { rerender, drawer };
};

const sidebarEventNames = () =>
  screen.queryAllByText(/^(llm-early|llm-late|tool-early|tool-late)$/).map((el) => el.textContent);

/** 前置列表中只有 error 状态、详情接口含诊断；验证懒加载正文和 HTTP 元数据传递给详情组件，finally 还原模拟数据。 */
it("merges complete lazy-loaded error diagnostics", async () => {
  detailCapture.data = { error: "full upstream diagnostic", metadata: { http_status: 502 }, response: { error: { trace: "upstream-123" } } };
  detailCapture.render.mockClear();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    render(<QueryClientProvider client={queryClient}><LogDetailsDrawer open onClose={() => {}} logEntry={makeLog({ status: "error", metadata: { model_group: "keep-existing" } })} accessToken="token" /></QueryClientProvider>);
    await waitFor(() => expect(detailCapture.render).toHaveBeenCalledWith(expect.objectContaining({ logEntry: expect.objectContaining({ error: "full upstream diagnostic", metadata: { model_group: "keep-existing", http_status: 502 }, response: { error: { trace: "upstream-123" } } }) })));
  } finally {
    detailCapture.data = null;
    queryClient.clear();
  }
});

describe("LogDetailsDrawer session sidebar sorting", () => {
  /** 前置不同来源的关联日志；仅缓存键和提示词使用推断标签，缺失来源不凭 ID 猜测；测试框架清理 DOM。 */
  it.each([
    ["cache_key", "logsUX.relatedCache"], ["prompt", "logsUX.relatedPrompt"],
    ["explicit", "Session"], [undefined, "Session"],
  ])("labels the logged session source %s accurately", async (source, label) => {
    renderSessionDrawer([makeLog({model:"source-model", session_source:source})]);
    await screen.findByText("source-model");
    expect(screen.getByText(t(label))).toBeVisible();
  });
  /** 前置含未定价零账单的会话，验证合计不冒充免费；响应缓存零值仍为真实零，自动卸载且查询客户端仅存内存。 */
  it.each([false, true])("preserves session pricing state with cache hit %s", async (cached) => {
    renderSessionDrawer([makeLog({ model: "unknown-price", spend: 0, cache_hit: cached,
      metadata: { cost_breakdown: { pricing_status: "unpriced" } } })]);
    await screen.findByText("unknown-price");
    if (cached) expect(screen.getAllByText(/\$0\.00/).length).toBeGreaterThan(0);
    else expect(screen.queryByText(/\$0\.00/)).not.toBeInTheDocument();
  });
  /** 前置四条会话日志含提示词缓存，验证侧栏明确统计响应缓存且不混算；渲染由测试框架自动清理。 */
  it("labels response cache separately from provider prompt cache", async () => {
    renderSessionDrawer(sessionLogs.map(log => ({ ...log, metadata: { cached_tokens: 4 } })));
    expect(await screen.findByText(t("Response cache hits") + ": 0/4")).toBeVisible();
  });

  it("loads only the clicked caller's session", async () => {
    renderSessionDrawer();

    await waitFor(() =>
      expect(sessionSpendLogsCall).toHaveBeenCalledWith("token", "session-1", 1, 100, {
        apiKey: "key-1",
        userId: "user-1",
      }),
    );
  });

  it("waits for caller resolution before loading a deep-linked session", async () => {
    vi.mocked(sessionSpendLogsCall).mockClear();
    vi.mocked(sessionSpendLogsCall).mockResolvedValue({ data: [], total: 0, total_pages: 1 });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <LogDetailsDrawer
          open
          onClose={() => {}}
          logEntry={null}
          sessionId="shared"
          accessToken="token"
          sessionCallerResolved={false}
        />
      </QueryClientProvider>,
    );

    expect(sessionSpendLogsCall).not.toHaveBeenCalled();

    rerender(
      <QueryClientProvider client={queryClient}>
        <LogDetailsDrawer
          open
          onClose={() => {}}
          logEntry={makeLog({ request_id: "representative", api_key: "key-1", user: "user-1" })}
          sessionId="shared"
          sessionApiKey="key-1"
          sessionUserId="user-1"
          accessToken="token"
          sessionCallerResolved
        />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(sessionSpendLogsCall).toHaveBeenCalledTimes(1));
    expect(sessionSpendLogsCall).toHaveBeenCalledWith("token", "shared", 1, 100, {
      apiKey: "key-1",
      userId: "user-1",
    });
  });

  it("defaults to duration order, longest call first across LLM and MCP calls", async () => {
    renderSessionDrawer();
    await waitFor(() => expect(sidebarEventNames()).toHaveLength(4));
    expect(sidebarEventNames()).toEqual(["tool-early", "llm-late", "llm-early", "tool-late"]);
  });

  it("switches to chronological order across LLM and MCP calls when Start time is selected", async () => {
    renderSessionDrawer();
    await waitFor(() => expect(sidebarEventNames()).toHaveLength(4));

    fireEvent.click(screen.getByText("Start time"));

    await waitFor(() => expect(sidebarEventNames()).toEqual(["llm-early", "tool-early", "llm-late", "tool-late"]));

    fireEvent.click(screen.getByText("Duration"));

    await waitFor(() => expect(sidebarEventNames()).toEqual(["tool-early", "llm-late", "llm-early", "tool-late"]));
  });

  it("resets the sort mode back to duration when the drawer is closed and reopened", async () => {
    const { rerender, drawer } = renderSessionDrawer();
    await waitFor(() => expect(sidebarEventNames()).toHaveLength(4));

    fireEvent.click(screen.getByText("Start time"));
    await waitFor(() => expect(sidebarEventNames()).toEqual(["llm-early", "tool-early", "llm-late", "tool-late"]));

    rerender(drawer(false));
    rerender(drawer(true));

    await waitFor(() => expect(sidebarEventNames()).toEqual(["tool-early", "llm-late", "llm-early", "tool-late"]));
  });
});

describe("LogDetailsDrawer session sidebar auto-router icon", () => {
  const routedSessionLogs = [
    makeLog({ request_id: "routed", model: "claude-opus-4-8", model_group: "smart-router" }),
    makeLog({ request_id: "direct", model: "claude-haiku-4-5", model_group: "claude-haiku" }),
  ];

  const renderRoutedSession = () => {
    vi.mocked(sessionSpendLogsCall).mockResolvedValue({ data: routedSessionLogs, total: 2, total_pages: 1 });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <QueryClientProvider client={queryClient}>
        <AutoRouterModelGroupsProvider>
          <LogDetailsDrawer open onClose={() => {}} logEntry={null} sessionId="session-1" accessToken="token" />
        </AutoRouterModelGroupsProvider>
      </QueryClientProvider>,
    );
  };

  const rowFor = (label: string): HTMLElement => {
    const row = Array.from(document.body.querySelectorAll("button")).find((button) =>
      button.textContent?.includes(label),
    );
    if (!row) throw new Error(`no sidebar row for ${label}`);
    return row;
  };

  it("marks the auto-routed entry with the router icon and leaves a direct call on the default icon", async () => {
    renderRoutedSession();

    expect(await screen.findByText("claude-opus-4-8")).toBeInTheDocument();

    const routedRow = rowFor("claude-opus-4-8");
    const directRow = rowFor("claude-haiku-4-5");

    expect(routedRow.querySelector(".lucide-waypoints")).not.toBeNull();
    expect(routedRow.querySelector(".lucide-sparkles")).toBeNull();
    expect(directRow.querySelector(".lucide-sparkles")).not.toBeNull();
    expect(directRow.querySelector(".lucide-waypoints")).toBeNull();
  });
});

/** 前置列表仍是执行中、详情已完成；验证抽屉使用最新详情状态，错误状态优先；finally 清除详情夹具，DOM 自动卸载。 */
it("uses the latest task lifecycle status from detail in the header", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  detailCapture.data = { status: "completed" };
  try {
    render(<QueryClientProvider client={client}><LogDetailsDrawer open onClose={() => {}} logEntry={makeLog({ status: "executing" })} accessToken="token" /></QueryClientProvider>);
    await waitFor(() => expect(detailCapture.header).toHaveBeenLastCalledWith(expect.objectContaining({ statusLabel: t("Completed") })));
  } finally { detailCapture.data = null; client.clear(); }
});

/** 前置懒加载接口失败；抽屉显示明确错误并提供重试，不能伪装成空正文；finally 清理 mock 与缓存。 */
it("shows lazy detail failure with a retry action", async () => {
  detailCapture.error = true;
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  try {
    render(<QueryClientProvider client={client}><LogDetailsDrawer open onClose={()=>{}} logEntry={makeLog({})} accessToken="token" /></QueryClientProvider>);
    expect(await screen.findByRole("alert")).toHaveTextContent(t("logsUX.detailFailed"));
    expect(detailCapture.render).toHaveBeenLastCalledWith(expect.objectContaining({isDetailsError:true}));
    fireEvent.click(screen.getByRole("button",{name:t("Refresh"),exact:true}));
    expect(detailCapture.retry).toHaveBeenCalled();
  } finally {detailCapture.error=false;detailCapture.retry.mockClear();client.clear();}
});

/** 前置桌面视口和已保存宽度；左右方向键调整并持久化且限制边界，卸载后恢复本地偏好和查询缓存。 */
it("resizes the details panel with the keyboard and preserves its preference", async () => {
  const key = "xhub.logDetails.widthPercent";
  const previous = window.localStorage.getItem(key);
  const previousWidth = window.innerWidth;
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 1280 });
  window.localStorage.setItem(key, "70");
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  try {
    render(<QueryClientProvider client={client}><LogDetailsDrawer open onClose={()=>{}} logEntry={makeLog({})} accessToken="token" /></QueryClientProvider>);
    const handle = await screen.findByRole("separator", {name:t("logsUX.resizeDrawer")});
    await waitFor(() => expect(handle).toHaveAttribute("aria-valuenow", "70"));
    fireEvent.keyDown(handle, {key:"ArrowLeft"});
    expect(handle).toHaveAttribute("aria-valuenow", "75");
    expect(window.localStorage.getItem(key)).toBe("75");
    for (let i=0; i<20; i++) fireEvent.keyDown(handle, {key:"ArrowRight"});
    expect(handle).toHaveAttribute("aria-valuenow", "50");
    expect(window.localStorage.getItem(key)).toBe("50");
    fireEvent.pointerDown(handle, {pointerId:1});
    fireEvent(handle, new MouseEvent("pointermove", {bubbles:true, clientX:256}));
    expect(handle).toHaveAttribute("aria-valuenow", "80");
    fireEvent.pointerUp(handle, {pointerId:1});
    fireEvent(handle, new MouseEvent("pointermove", {bubbles:true, clientX:512}));
    expect(handle).toHaveAttribute("aria-valuenow", "80");
  } finally {
    if (previous === null) window.localStorage.removeItem(key); else window.localStorage.setItem(key,previous);
    Object.defineProperty(window, "innerWidth", { configurable:true, value:previousWidth });
    client.clear();
  }
});

/** 前置非法保存偏好；抽屉必须使用安全宽度，不能把 NaN 交给页面；最后恢复本地存储。 */
it("falls back safely when a saved width is invalid", async () => {
  const key = "xhub.logDetails.widthPercent";
  const previous = window.localStorage.getItem(key);
  window.localStorage.setItem(key, "broken");
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  try {
    render(<QueryClientProvider client={client}><LogDetailsDrawer open onClose={()=>{}} logEntry={makeLog({})} accessToken="token" /></QueryClientProvider>);
    const handle = await screen.findByRole("separator", {name:t("logsUX.resizeDrawer")});
    expect(Number(handle.getAttribute("aria-valuenow"))).toBeGreaterThanOrEqual(40);
    expect(Number(handle.getAttribute("aria-valuenow"))).toBeLessThanOrEqual(100);
  } finally {if (previous === null) window.localStorage.removeItem(key); else window.localStorage.setItem(key,previous); client.clear();}
});

/** 前置 1030px 桌面视口及低于最小值的已保存宽度；辅助技术读到的当前值不得小于最小值，结束后恢复偏好和视口。 */
it("keeps the accessible current width within a fractional viewport minimum", async () => {
  const key = "xhub.logDetails.widthPercent";
  const previous = window.localStorage.getItem(key);
  const previousWidth = window.innerWidth;
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 1030 });
  window.localStorage.setItem(key, "40");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    render(<QueryClientProvider client={client}><LogDetailsDrawer open onClose={() => {}} logEntry={makeLog({})} accessToken="token" /></QueryClientProvider>);
    const handle = await screen.findByRole("separator", { name: t("logsUX.resizeDrawer") });
    await waitFor(() => expect(handle).toHaveAttribute("aria-valuemin", "63"));
    expect(Number(handle.getAttribute("aria-valuenow"))).toBeGreaterThanOrEqual(Number(handle.getAttribute("aria-valuemin")));
    expect(Number(handle.getAttribute("aria-valuenow"))).toBeLessThanOrEqual(Number(handle.getAttribute("aria-valuemax")));
  } finally {
    if (previous === null) window.localStorage.removeItem(key); else window.localStorage.setItem(key, previous);
    Object.defineProperty(window, "innerWidth", { configurable: true, value: previousWidth });
    client.clear();
  }
});

/** 前置手机宽度并展示真实日志；验证自动全宽及收起侧栏，最后恢复视口和查询缓存。 */
it("uses full width and collapses the sidebar on narrow screens", async () => {
  const previousWidth = window.innerWidth;
  Object.defineProperty(window, "innerWidth", { configurable:true, value:375 });
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  detailCapture.header.mockClear();
  try {
    render(<QueryClientProvider client={client}><LogDetailsDrawer open onClose={()=>{}} logEntry={makeLog({})} accessToken="token" /></QueryClientProvider>);
    await waitFor(() => expect(detailCapture.header).toHaveBeenLastCalledWith(expect.objectContaining({isSidebarCollapsed:true})));
    expect(screen.queryByRole("separator", {name:t("logsUX.resizeDrawer")})).not.toBeInTheDocument();
    expect(screen.getByRole("dialog")).toHaveStyle({width:"100%"});
    // 窄屏展开时侧栏及其折叠按钮必须高于详情头部，避免按钮被遮挡。
    act(() => detailCapture.header.mock.lastCall?.[0].onToggleSidebar());
    const collapse = screen.getByRole("button", { name: t("Collapse trace sidebar") });
    expect(collapse).toHaveClass("max-sm:z-floating");
    expect(screen.getByRole("button", { name: t("Copy trace id") }).closest("[class*='max-sm:z-sticky']")).toBeInTheDocument();
    fireEvent.click(collapse);
    await waitFor(() => expect(detailCapture.header).toHaveBeenLastCalledWith(expect.objectContaining({isSidebarCollapsed:true})));
  } finally {Object.defineProperty(window,"innerWidth",{configurable:true,value:previousWidth});client.clear();}
});
