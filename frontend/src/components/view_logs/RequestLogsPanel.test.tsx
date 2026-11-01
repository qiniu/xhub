import { QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import moment from "moment";
import { NuqsTestingAdapter, type UrlUpdateEvent } from "nuqs/adapters/testing";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { render, renderWithProviders, testQueryClient } from "../../../tests/test-utils";
import type { LogEntry } from "./columns";
import RequestLogsPanel from "./RequestLogsPanel";
import { translate } from "@/i18n/translate";

vi.mock("../networking", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../networking")>();
  return {
    ...actual,
    uiSpendLogsCall: vi.fn(),
    uiSpendLogDetailsCall: vi.fn().mockResolvedValue(null),
    keyInfoV1Call: vi.fn().mockResolvedValue({ info: {} }),
    allEndUsersCall: vi.fn().mockResolvedValue([]),
  };
});

vi.mock("@/components/key_team_helpers/filter_helpers", () => ({
  fetchAllTeams: vi.fn().mockResolvedValue([]),
}));

vi.mock("./LogDetailsDrawer", () => ({
  LogDetailsDrawer: function LogDetailsDrawerMock({
    open,
    logEntry,
    sessionId,
    onClose,
    allLogs = [],
    onSelectLog,
  }: {
    open: boolean;
    logEntry?: { request_id: string } | null;
    sessionId?: string | null;
    onClose: () => void;
    allLogs?: { request_id: string }[];
    onSelectLog?: (log: { request_id: string }) => void;
  }) {
    const nextLog = allLogs.find((log) => log.request_id !== logEntry?.request_id);
    return (
      <div data-testid="log-details-drawer" data-log-id={logEntry?.request_id ?? ""} data-session-id={sessionId ?? ""}>
        {open ? "open" : "closed"}
        <button type="button" onClick={onClose}>
          close-drawer
        </button>
        <button type="button" onClick={() => nextLog && onSelectLog?.(nextLog)}>
          select-next-log
        </button>
      </div>
    );
  },
}));

const debounce = vi.hoisted(() => ({ settled: null as string | null }));

vi.mock("@tanstack/react-pacer/debouncer", () => ({
  useDebouncedValue: vi.fn((value: unknown) => [debounce.settled ?? value, { cancel: vi.fn(), flush: vi.fn() }]),
}));

import { useDebouncedValue } from "@tanstack/react-pacer/debouncer";
import { DEBOUNCE_WAIT_MS } from "@/utils/debounceConstants";
import { keyInfoV1Call, uiSpendLogsCall, uiSpendLogDetailsCall } from "../networking";

const logEntry = (overrides: Partial<LogEntry>): LogEntry => ({
  request_id: "req-1",
  api_key: "key-1",
  team_id: "team-1",
  model: "gpt-4o",
  model_id: "model-1",
  call_type: "acompletion",
  spend: 0.01,
  total_tokens: 10,
  prompt_tokens: 5,
  completion_tokens: 5,
  startTime: "2026-07-07T09:50:13Z",
  endTime: "2026-07-07T09:50:14Z",
  cache_hit: "false",
  messages: [],
  response: {},
  ...overrides,
});

const respondWith = (data: LogEntry[]) =>
  vi.mocked(uiSpendLogsCall).mockResolvedValue({
    data,
    total: data.length,
    page: 1,
    page_size: 50,
    total_pages: 1,
  });

const defaultProps = {
  accessToken: "test-token",
  token: "test-token",
  userRole: "Admin",
  userID: "user-1",
  isActive: true,
};

const row = (requestId: string) => document.querySelector(`[data-row-id="${requestId}"]`);
const lastCall = () => vi.mocked(uiSpendLogsCall).mock.calls.at(-1)?.[0];

const onUrlUpdate = vi.fn<(event: UrlUpdateEvent) => void>();
const renderPanel = (searchParams?: string) =>
  renderWithProviders(<RequestLogsPanel {...defaultProps} />, { searchParams, onUrlUpdate });

const renderPanelWithHistory = () => {
  const stack = [""];
  const handleUrlUpdate = (event: UrlUpdateEvent) => {
    onUrlUpdate(event);
    if (event.options.history === "push") {
      stack.push(event.queryString);
    } else {
      stack[stack.length - 1] = event.queryString;
    }
  };
  const tree = (searchParams: string) => (
    <NuqsTestingAdapter searchParams={searchParams} onUrlUpdate={handleUrlUpdate} hasMemory>
      <QueryClientProvider client={testQueryClient}>
        <RequestLogsPanel {...defaultProps} />
      </QueryClientProvider>
    </NuqsTestingAdapter>
  );
  const view = render(tree(""));
  return {
    goBack: () => {
      const current = stack[stack.length - 1] ?? "";
      stack.pop();
      const target = stack[stack.length - 1] ?? "";
      view.rerender(tree(current));
      view.rerender(tree(target));
    },
  };
};
const urlParams = () => onUrlUpdate.mock.calls.at(-1)?.[0].searchParams ?? new URLSearchParams();
const historyModes = () => onUrlUpdate.mock.calls.map(([event]) => event.options.history);

/** 前置日志列表已加载；按列选择器恢复低频字段供交互回归，无持久化数据。 */
async function showColumn(id: string) {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Columns" }));
  await user.click(await screen.findByTestId(`view-option-${id}`));
  await user.keyboard("{Escape}");
}

describe("RequestLogsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    testQueryClient.clear();
    respondWith([]);
    vi.mocked(uiSpendLogDetailsCall).mockReset().mockResolvedValue(null);
    debounce.settled = null;
  });

  /** 前置流式与同步日志；TPS 默认隐藏，通过列菜单开启后保留真实数值与无样本状态，finally 清理组件和缓存。 */
  it("restores the optional TPS column through its accessible menu", async () => {
    respondWith([
      logEntry({ request_id: "streamed-speed", streamed: true, tps: 25 }),
      logEntry({ request_id: "sync-speed", streamed: false, tps: null }),
    ]);
    const view = renderPanel();
    const user = userEvent.setup();
    try {
      await screen.findByText("streamed-speed");
      expect(screen.queryByRole("columnheader", { name: "TPS (tokens/s)", exact: true })).not.toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "Columns", exact: true }));
      const option = await screen.findByRole("menuitemcheckbox", { name: "TPS (tokens/s)", exact: true });
      expect(option).not.toBeChecked();
      await user.click(option);
      expect(option).toBeChecked();
      await user.keyboard("{Escape}");
      const index = screen.getAllByRole("columnheader").findIndex(cell => cell.textContent === "TPS (tokens/s)");
      expect(index).toBeGreaterThanOrEqual(0);
      expect(within(screen.getByRole("row", { name: /streamed-speed/ })).getAllByRole("cell")[index]).toHaveTextContent("25.00");
      expect(within(screen.getByRole("row", { name: /sync-speed/ })).getAllByRole("cell")[index]).toHaveTextContent("—");
    } finally { view.unmount(); testQueryClient.clear(); }
  });

  /** 前置正常/错误日志包含历史 Hash 且密钥接口会失败；验证文本不请求密钥、行点击仍打开日志详情。
   * 参数 errorsOnly 控制面板种类；测试框架卸载组件，beforeEach 清理查询缓存、会话状态与 mock。 */
  it.each([false, true])("never fetches key info when clicking a hash in errorsOnly=%s", async (errorsOnly) => {
    const user = userEvent.setup();
    vi.mocked(keyInfoV1Call).mockRejectedValue(new Error("Not found"));
    respondWith([logEntry({ request_id: "req-historical-key", metadata: { user_api_key: "deleted-key-hash" } })]);
    renderWithProviders(<RequestLogsPanel {...defaultProps} errorsOnly={errorsOnly} />, { onUrlUpdate });

    await showColumn("key_hash");
    const hash = await screen.findByText("deleted-key-hash");
    expect(screen.queryByRole("button", { name: "deleted-key-hash" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "deleted-key-hash" })).not.toBeInTheDocument();
    await user.click(hash);
    await waitFor(() => expect(screen.getByTestId("log-details-drawer")).toHaveAttribute("data-log-id", "req-historical-key"));
    expect(keyInfoV1Call).not.toHaveBeenCalled();
    expect(urlParams().get("log_id")).toBe("req-historical-key");
  });

  /** 前置隔离查询缓存和错误面板；验证请求真实查询封装携带错误筛选和逐条模式，beforeEach 清理会话缓存。 */
  it("queries only failed requests for the error panel", async () => {
    renderWithProviders(<RequestLogsPanel {...defaultProps} errorsOnly />);
    await waitFor(() => expect(uiSpendLogsCall).toHaveBeenCalled());
    expect(lastCall()?.params).toMatchObject({ status_filter: "error", group_by_session: false });
    expect(screen.getByRole("heading", { name: "Error Logs" })).toBeInTheDocument();
  });

  /** 前置页码接口返回不足一页的行但总数为 40；验证不能把短页当末页且 Next 无游标可翻页；测试框架清理缓存和组件。 */
  it("uses the offset server total and advances without a session cursor", async () => {
    vi.mocked(uiSpendLogsCall).mockImplementation(async ({ page }) => ({
      data: [logEntry({ request_id: "offset-" + page })], total: 40,
      page: page ?? 1, page_size: 25, total_pages: 2,
    }));
    renderPanel();
    await waitFor(() => expect(row("offset-1")).not.toBeNull());
    expect(screen.getByTestId("pagination-range")).toHaveTextContent("Showing 1-25 of 40");
    fireEvent.click(screen.getByRole("button", { name: "Go to next page" }));
    await waitFor(() => expect(row("offset-2")).not.toBeNull());
    expect(lastCall()?.page).toBe(2);
    expect(lastCall()?.params?.session_cursor).toBeUndefined();
    expect(screen.getByRole("button", { name: "Go to next page" })).toBeDisabled();
  });

  describe("server-grouped session pagination (#38060)", () => {
    it("requests session-grouped pages of 25 rows by default without a cursor", async () => {
      renderPanel();

      await waitFor(() => expect(uiSpendLogsCall).toHaveBeenCalled());
      expect(lastCall()?.params?.group_by_session).toBe(true);
      expect(lastCall()?.params?.session_cursor).toBeUndefined();
      expect(lastCall()?.page_size).toBe(25);
    });

    it("offers the same page sizes as the other tables", async () => {
      const user = userEvent.setup();
      respondWith([logEntry({ request_id: "req-a" })]);
      renderPanel();

      await waitFor(() => expect(row("req-a")).not.toBeNull());
      await user.click(screen.getByTestId("pagination-page-size"));

      const options = await screen.findAllByRole("option");
      expect(options.map((option) => option.textContent)).toEqual(["25", "50", "100"]);
    });

    it("counts the rendered rows in the footer instead of the server's session total", async () => {
      const lastPage = {
        data: [logEntry({ request_id: "req-a" }), logEntry({ request_id: "req-b" }), logEntry({ request_id: "req-c" })],
        total: 40,
        page: 1,
        page_size: 25,
        total_pages: 2,
        next_session_cursor: null,
        has_more: false,
      };
      vi.mocked(uiSpendLogsCall).mockResolvedValue(lastPage);
      renderPanel();

      await waitFor(() => expect(row("req-a")).not.toBeNull());
      expect(screen.getByTestId("pagination-range")).toHaveTextContent("Showing 1-3 of 3");
      expect(screen.getByTestId("pagination-next")).toBeDisabled();
    });

    it("keeps Next enabled from the server total while more session pages remain", async () => {
      const firstPage = {
        data: Array.from({ length: 25 }, (_, index) => logEntry({ request_id: `req-${index}` })),
        total: 80,
        page: 1,
        page_size: 25,
        total_pages: 4,
        next_session_cursor: "2026-07-07 09:50:13|key-1|sess-1",
        has_more: true,
      };
      vi.mocked(uiSpendLogsCall).mockResolvedValue(firstPage);
      renderPanel();

      await waitFor(() => expect(row("req-0")).not.toBeNull());
      expect(screen.getByTestId("pagination-range")).toHaveTextContent("Showing 1-25 of 80");
      expect(screen.getByTestId("pagination-next")).toBeEnabled();
    });

    it("renders every row the server returns without client-side collapsing", async () => {
      respondWith([
        logEntry({ request_id: "req-a", session_id: "sess-1", session_total_count: 3 }),
        logEntry({ request_id: "req-b", session_id: "sess-1", session_total_count: 3 }),
        logEntry({ request_id: "req-c", session_id: "sess-1", session_total_count: 3 }),
      ]);
      renderPanel();

      await waitFor(() => expect(row("req-a")).not.toBeNull());
      expect(row("req-b")).not.toBeNull();
      expect(row("req-c")).not.toBeNull();
    });

    it("shows the session's call count on the server-picked representative row", async () => {
      respondWith([
        logEntry({
          request_id: "req-llm",
          call_type: "acompletion",
          session_id: "sess-1",
          session_total_count: 3,
          session_llm_count: 2,
          mcp_tool_call_count: 1,
        }),
      ]);
      renderPanel();

      await waitFor(() => expect(row("req-llm")).not.toBeNull());
      await showColumn("type");
      expect(within(row("req-llm") as HTMLElement).getByText("3")).toBeInTheDocument();
    });

    it("passes the server keyset cursor when navigating to the next page", async () => {
      const firstPage = Array.from({ length: 50 }, (_, index) => logEntry({ request_id: `req-${index}` }));
      vi.mocked(uiSpendLogsCall).mockResolvedValue({
        data: firstPage,
        total: 80,
        page: 1,
        page_size: 50,
        total_pages: 2,
        next_session_cursor: "2026-07-07 09:50:13|key-1|sess-1",
        has_more: true,
      });
      renderPanel();

      await waitFor(() => expect(row("req-0")).not.toBeNull());
      fireEvent.click(screen.getByTestId("pagination-next"));

      await waitFor(() => {
        const call = lastCall();
        expect(call?.params?.session_cursor).toBe("2026-07-07 09:50:13|key-1|sess-1");
        expect(call?.page).toBe(2);
      });
    });

    it("jumps straight to the last page without a cursor when the last-page button is clicked", async () => {
      const firstPage = Array.from({ length: 25 }, (_, index) => logEntry({ request_id: `req-${index}` }));
      const lastPage = Array.from({ length: 10 }, (_, index) => logEntry({ request_id: `req-last-${index}` }));
      vi.mocked(uiSpendLogsCall).mockImplementation(async ({ page }) =>
        page === 3
          ? {
              data: lastPage,
              total: 60,
              page: 3,
              page_size: 25,
              total_pages: 3,
              next_session_cursor: null,
              has_more: false,
            }
          : {
              data: firstPage,
              total: 60,
              page: 1,
              page_size: 25,
              total_pages: 3,
              next_session_cursor: "2026-07-07 09:50:13|key-1|sess-1",
              has_more: true,
            },
      );
      renderPanel();

      await waitFor(() => expect(row("req-0")).not.toBeNull());
      expect(screen.getByTestId("pagination-page")).toHaveTextContent("Page 1 of 3");
      fireEvent.click(screen.getByTestId("pagination-last"));

      await waitFor(() => expect(row("req-last-0")).not.toBeNull());
      expect(lastCall()?.page).toBe(3);
      expect(lastCall()?.params?.session_cursor).toBeUndefined();
      expect(screen.getByTestId("pagination-page")).toHaveTextContent("Page 3 of 3");
      expect(screen.getByTestId("pagination-range")).toHaveTextContent("Showing 51-60 of 60");
      expect(vi.mocked(uiSpendLogsCall).mock.calls.filter(([options]) => options.page === 2)).toHaveLength(0);
    });

    it("drops the cursor and returns to the first page when a filter changes", async () => {
      const firstPage = Array.from({ length: 50 }, (_, index) => logEntry({ request_id: `req-${index}` }));
      vi.mocked(uiSpendLogsCall).mockResolvedValue({
        data: firstPage,
        total: 80,
        page: 1,
        page_size: 50,
        total_pages: 2,
        next_session_cursor: "2026-07-07 09:50:13|key-1|sess-1",
        has_more: true,
      });
      renderPanel();

      await waitFor(() => expect(row("req-0")).not.toBeNull());
      fireEvent.click(screen.getByTestId("pagination-next"));
      await waitFor(() => expect(lastCall()?.page).toBe(2));

      fireEvent.change(screen.getByTestId("datatable-search"), { target: { value: "req-elsewhere" } });

      await waitFor(() => {
        const call = lastCall();
        expect(call?.page).toBe(1);
        expect(call?.params?.session_cursor).toBeUndefined();
      });
    });

    it("ignores another next click while the next page is still fetching", async () => {
      const firstPage = Array.from({ length: 50 }, (_, index) => logEntry({ request_id: `req-${index}` }));
      const firstResponse = {
        data: firstPage,
        total: 150,
        page: 1,
        page_size: 50,
        total_pages: 3,
        next_session_cursor: "2026-07-07 09:50:13|key-1|sess-1",
        has_more: true,
      };
      vi.mocked(uiSpendLogsCall)
        .mockResolvedValueOnce(firstResponse)
        .mockImplementation(() => new Promise(() => {}));
      renderPanel();

      await waitFor(() => expect(row("req-0")).not.toBeNull());
      fireEvent.click(screen.getByTestId("pagination-next"));
      await waitFor(() => expect(lastCall()?.page).toBe(2));

      fireEvent.click(screen.getByTestId("pagination-next"));

      expect(lastCall()?.page).toBe(2);
      expect(vi.mocked(uiSpendLogsCall).mock.calls.filter(([options]) => options.page === 3)).toHaveLength(0);
    });

    it("still moves to the next page while a live-tail refetch of the current page is in flight", async () => {
      const firstPage = Array.from({ length: 50 }, (_, index) => logEntry({ request_id: `req-${index}` }));
      const firstResponse = {
        data: firstPage,
        total: 150,
        page: 1,
        page_size: 50,
        total_pages: 3,
        next_session_cursor: "2026-07-07 09:50:13|key-1|sess-1",
        has_more: true,
      };
      vi.mocked(uiSpendLogsCall)
        .mockResolvedValueOnce(firstResponse)
        .mockImplementation(() => new Promise(() => {}));
      renderPanel();

      await waitFor(() => expect(row("req-0")).not.toBeNull());
      void testQueryClient.refetchQueries({ queryKey: ["logs", "table"] });
      await waitFor(() => expect(uiSpendLogsCall).toHaveBeenCalledTimes(2));

      fireEvent.click(screen.getByTestId("pagination-next"));

      await waitFor(() => {
        const call = lastCall();
        expect(call?.page).toBe(2);
        expect(call?.params?.session_cursor).toBe("2026-07-07 09:50:13|key-1|sess-1");
      });
    });

    /** 目的：验证英文默认界面切换自定义时间会清除分页游标；前置两页日志夹具；结果为第一页请求，框架回收组件与查询缓存，无后台写入。 */
    it("drops the cursor and returns to the first page when Custom Range is toggled", async () => {
      const user = userEvent.setup();
      const firstPage = Array.from({ length: 50 }, (_, index) => logEntry({ request_id: `req-${index}` }));
      vi.mocked(uiSpendLogsCall).mockResolvedValue({
        data: firstPage,
        total: 80,
        page: 1,
        page_size: 50,
        total_pages: 2,
        next_session_cursor: "2026-07-07 09:50:13|key-1|sess-1",
        has_more: true,
      });
      renderPanel();

      await waitFor(() => expect(row("req-0")).not.toBeNull());
      fireEvent.click(screen.getByTestId("pagination-next"));
      await waitFor(() => expect(lastCall()?.page).toBe(2));

      await user.click(screen.getByRole("button", { name: translate("en", "Last 24 Hours") }));
      await user.click(await screen.findByRole("button", { name: "Custom Range" }));

      await waitFor(() => {
        const call = lastCall();
        expect(call?.page).toBe(1);
        expect(call?.params?.session_cursor).toBeUndefined();
      });
    });

    it("leaves single-call rows untouched", async () => {
      respondWith([
        logEntry({ request_id: "req-solo-a", session_id: "sess-a", session_total_count: 1 }),
        logEntry({ request_id: "req-solo-b" }),
      ]);
      renderPanel();

      await waitFor(() => expect(row("req-solo-a")).not.toBeNull());
      expect(row("req-solo-b")).not.toBeNull();
    });
  });

  describe("search by any id (LIT-3981, LIT-4741)", () => {
    it("sends the typed id to the server as search on the first page instead of filtering the loaded rows", async () => {
      renderPanel();

      await waitFor(() => expect(uiSpendLogsCall).toHaveBeenCalled());

      fireEvent.change(screen.getByTestId("datatable-search"), { target: { value: "req-on-another-page" } });

      await waitFor(() => {
        const call = lastCall();
        if (!call) throw new Error("uiSpendLogsCall was not called");
        expect(call.params?.search).toBe("req-on-another-page");
        expect(call.page).toBe(1);
      });
      expect(lastCall()?.params?.request_id).toBeUndefined();
      expect(lastCall()?.params?.session_cursor).toBeUndefined();
    });

    it("sends the debounced value to the server while the box shows what is being typed", async () => {
      debounce.settled = "settled-id";
      renderPanel();

      await waitFor(() => expect(uiSpendLogsCall).toHaveBeenCalled());

      fireEvent.change(screen.getByTestId("datatable-search"), { target: { value: "still-typing" } });

      expect(screen.getByTestId("datatable-search")).toHaveValue("still-typing");
      await waitFor(() =>
        expect(useDebouncedValue).toHaveBeenLastCalledWith("still-typing", { wait: DEBOUNCE_WAIT_MS }),
      );
      await waitFor(() => expect(lastCall()?.params?.search).toBe("settled-id"));
      const sentLiveValue = vi
        .mocked(uiSpendLogsCall)
        .mock.calls.some(([options]) => options.params?.search === "still-typing");
      expect(sentLiveValue).toBe(false);
    });

    it("shows a Search chip whose remove button clears the box and restores the unsearched listing", async () => {
      const user = userEvent.setup();
      vi.mocked(uiSpendLogsCall).mockImplementation(async ({ params }) => {
        const data =
          params?.search === "sess-42"
            ? [logEntry({ request_id: "req-sess", session_id: "sess-42" })]
            : [logEntry({ request_id: "req-initial" })];
        return { data, total: data.length, page: 1, page_size: 50, total_pages: 1 };
      });
      renderPanel();

      await waitFor(() => expect(row("req-initial")).not.toBeNull());
      fireEvent.change(screen.getByTestId("datatable-search"), { target: { value: "sess-42" } });
      await waitFor(() => expect(row("req-sess")).not.toBeNull());
      expect(row("req-initial")).toBeNull();
      expect(screen.getByTestId("filter-chip-search")).toHaveTextContent("Search:sess-42");

      await user.click(screen.getByRole("button", { name: "Remove Search filter" }));

      expect(screen.getByTestId("datatable-search")).toHaveValue("");
      await waitFor(() => expect(row("req-initial")).not.toBeNull());
      expect(row("req-sess")).toBeNull();
    });
  });

  describe("time range", () => {
    /** 目的：验证英文时间快捷项提交十五分钟查询窗口；前置默认日志面板；检查请求起止差值，框架清理组件与查询缓存，无后台写入。 */
    it("requests a ~15 minute window when Last 15 Minutes is picked", async () => {
      const user = userEvent.setup();
      renderPanel();

      await waitFor(() => expect(uiSpendLogsCall).toHaveBeenCalled());
      await user.click(screen.getByRole("button", { name: translate("en", "Last 24 Hours") }));
      await user.click(await screen.findByRole("button", { name: translate("en", "Last 15 Minutes") }));

      const windowSeconds = () => {
        const call = lastCall();
        if (!call) throw new Error("no call");
        return moment
          .utc(call.end_date, "YYYY-MM-DD HH:mm:ss")
          .diff(moment.utc(call.start_date, "YYYY-MM-DD HH:mm:ss"), "seconds");
      };

      await waitFor(() => expect(windowSeconds()).toBeGreaterThanOrEqual(15 * 60));
      expect(windowSeconds()).toBeLessThanOrEqual(16 * 60);
    });

    /** 目的：验证英文界面重置筛选恢复二十四小时窗口；前置选择十五分钟；检查按钮及刷新请求，框架清理组件和查询缓存，无后台写入。 */
    it("restores the default 24 hour window when filters are reset", async () => {
      const user = userEvent.setup();
      renderPanel();

      await waitFor(() => expect(uiSpendLogsCall).toHaveBeenCalled());
      await user.click(screen.getByRole("button", { name: translate("en", "Last 24 Hours") }));
      await user.click(await screen.findByRole("button", { name: translate("en", "Last 15 Minutes") }));
      expect(await screen.findByRole("button", { name: translate("en", "Last 15 Minutes") })).toBeInTheDocument();

      await user.click(screen.getByRole("button", { name: "Reset Filters" }));

      expect(await screen.findByRole("button", { name: translate("en", "Last 24 Hours") })).toBeInTheDocument();

      await user.click(screen.getByTestId("datatable-refresh"));

      await waitFor(() => {
        const call = lastCall();
        if (!call) throw new Error("no call");
        const diff = moment
          .utc(call.end_date, "YYYY-MM-DD HH:mm:ss")
          .diff(moment.utc(call.start_date, "YYYY-MM-DD HH:mm:ss"), "seconds");
        expect(diff).toBeGreaterThanOrEqual(24 * 60 * 60);
      });
    });
  });

  describe("shareable log links (?log_id=)", () => {
    const drawer = () => screen.getByTestId("log-details-drawer");

    it("clicking a row writes ?log_id=<request_id> to the URL and opens the drawer", async () => {
      const user = userEvent.setup();
      respondWith([logEntry({ request_id: "req-1" })]);
      renderPanel();

      await waitFor(() => expect(row("req-1")).not.toBeNull());
      await user.click(row("req-1") as HTMLElement);

      await waitFor(() => expect(urlParams().get("log_id")).toBe("req-1"));
      expect(historyModes()).toEqual(["push"]);
      await waitFor(() => {
        expect(drawer()).toHaveTextContent("open");
      });
      expect(drawer()).toHaveAttribute("data-log-id", "req-1");
    });

    it("opens the drawer on load when ?log_id= matches a log in the loaded page", async () => {
      respondWith([logEntry({ request_id: "req-1" }), logEntry({ request_id: "req-2" })]);
      renderPanel("?log_id=req-2");

      await waitFor(() => {
        expect(drawer()).toHaveTextContent("open");
      });
      expect(drawer()).toHaveAttribute("data-log-id", "req-2");
    });

    it("fetches the log by request_id and opens the drawer when it is not in the loaded page", async () => {
      vi.mocked(uiSpendLogDetailsCall).mockResolvedValue(logEntry({ request_id: "req-old", startTime: "2020-01-01T00:00:00Z" }));
      renderPanel("?log_id=req-old");

      await waitFor(() => {
        expect(drawer()).toHaveTextContent("open");
      });
      expect(drawer()).toHaveAttribute("data-log-id", "req-old");

      expect(uiSpendLogDetailsCall).toHaveBeenCalledWith("test-token", "req-old", "");
      expect(vi.mocked(uiSpendLogsCall).mock.calls.every(([options]) => !options.params?.request_id)).toBe(true);
    });

    it("opens the drawer when ?log_id= is the log's litellm_call_id rather than its request_id", async () => {
      respondWith([logEntry({ request_id: "chatcmpl-provider", litellm_call_id: "call-1" })]);
      renderPanel("?log_id=call-1");

      await waitFor(() => {
        expect(drawer()).toHaveTextContent("open");
      });
      expect(drawer()).toHaveAttribute("data-log-id", "chatcmpl-provider");
    });

    it("fetches by litellm_call_id and opens the drawer when that log is not in the loaded page", async () => {
      vi.mocked(uiSpendLogDetailsCall).mockResolvedValue(logEntry({ request_id: "chatcmpl-old", litellm_call_id: "call-old" }));
      renderPanel("?log_id=call-old");

      await waitFor(() => {
        expect(drawer()).toHaveTextContent("open");
      });
      expect(drawer()).toHaveAttribute("data-log-id", "chatcmpl-old");
    });

    it("opens the exact request_id row when another log in the page carries that id as its litellm_call_id", async () => {
      respondWith([
        logEntry({ request_id: "chatcmpl-other", litellm_call_id: "victim-req" }),
        logEntry({ request_id: "victim-req", litellm_call_id: "victim-call" }),
      ]);
      renderPanel("?log_id=victim-req");

      await waitFor(() => {
        expect(drawer()).toHaveTextContent("open");
      });
      expect(drawer()).toHaveAttribute("data-log-id", "victim-req");
    });

    it("closing the drawer removes ?log_id= from the URL and closes the drawer", async () => {
      const user = userEvent.setup();
      respondWith([logEntry({ request_id: "req-1" })]);
      renderPanel();

      await waitFor(() => expect(row("req-1")).not.toBeNull());
      await user.click(row("req-1") as HTMLElement);
      await waitFor(() => expect(drawer()).toHaveTextContent("open"));

      await user.click(screen.getByRole("button", { name: "close-drawer" }));

      await waitFor(() => expect(urlParams().get("log_id")).toBeNull());
      await waitFor(() => expect(drawer()).toHaveTextContent("closed"));
    });

    it("switching logs inside the drawer replaces the URL, so back closes the drawer in one step", async () => {
      const user = userEvent.setup();
      respondWith([logEntry({ request_id: "req-1" }), logEntry({ request_id: "req-2" })]);
      const { goBack } = renderPanelWithHistory();

      await waitFor(() => expect(row("req-1")).not.toBeNull());
      await user.click(row("req-1") as HTMLElement);
      await waitFor(() => expect(drawer()).toHaveAttribute("data-log-id", "req-1"));

      await user.click(screen.getByRole("button", { name: "select-next-log" }));
      await waitFor(() => expect(drawer()).toHaveAttribute("data-log-id", "req-2"));
      expect(urlParams().get("log_id")).toBe("req-2");
      expect(historyModes()).toEqual(["push", "replace"]);

      goBack();
      await waitFor(() => expect(drawer()).toHaveTextContent("closed"));
      expect(drawer()).toHaveAttribute("data-log-id", "");
    });

    it("clicking a session id writes ?session_id= and ?log_id= and opens the session drawer", async () => {
      const user = userEvent.setup();
      respondWith([logEntry({ request_id: "req-solo", session_id: "sess-solo", session_total_count: 1 })]);
      renderPanel();

      await waitFor(() => expect(row("req-solo")).not.toBeNull());
      await showColumn("session_id");
      await user.click(within(row("req-solo") as HTMLElement).getByText("sess-solo"));

      await waitFor(() => expect(urlParams().get("session_id")).toBe("sess-solo"));
      expect(urlParams().get("log_id")).toBe("req-solo");
      expect(historyModes()).toEqual(["push"]);
      await waitFor(() => {
        expect(drawer()).toHaveTextContent("open");
      });
      expect(drawer()).toHaveAttribute("data-session-id", "sess-solo");
    });

    it("clicking a log row clears a lingering ?session_id= so the drawer shows the clicked log", async () => {
      const user = userEvent.setup();
      respondWith([
        logEntry({ request_id: "req-a", session_id: "sess-a", session_total_count: 1 }),
        logEntry({ request_id: "req-b" }),
      ]);
      renderPanel();

      await waitFor(() => expect(row("req-a")).not.toBeNull());
      await showColumn("session_id");
      await user.click(within(row("req-a") as HTMLElement).getByText("sess-a"));
      await waitFor(() => expect(urlParams().get("session_id")).toBe("sess-a"));

      await user.click(row("req-b") as HTMLElement);

      await waitFor(() => expect(urlParams().get("log_id")).toBe("req-b"));
      expect(urlParams().get("session_id")).toBeNull();
      await waitFor(() => {
        expect(drawer()).toHaveAttribute("data-log-id", "req-b");
      });
      expect(drawer()).toHaveAttribute("data-session-id", "");
    });

    it("closing a drawer opened via a session id clears both params", async () => {
      const user = userEvent.setup();
      respondWith([logEntry({ request_id: "req-solo", session_id: "sess-solo", session_total_count: 1 })]);
      renderPanel();

      await waitFor(() => expect(row("req-solo")).not.toBeNull());
      await showColumn("session_id");
      await user.click(within(row("req-solo") as HTMLElement).getByText("sess-solo"));
      await waitFor(() => expect(drawer()).toHaveTextContent("open"));

      await user.click(screen.getByRole("button", { name: "close-drawer" }));

      await waitFor(() => expect(drawer()).toHaveTextContent("closed"));
      expect(urlParams().get("session_id")).toBeNull();
      expect(urlParams().get("log_id")).toBeNull();
    });

    it("opens a deep-linked multi-call session log in session mode", async () => {
      respondWith([logEntry({ request_id: "req-llm", session_id: "sess-1", session_total_count: 3 })]);
      renderPanel("?log_id=req-llm");

      await waitFor(() => {
        expect(drawer()).toHaveTextContent("open");
      });
      expect(drawer()).toHaveAttribute("data-session-id", "sess-1");
      expect(drawer()).toHaveAttribute("data-log-id", "req-llm");
    });

    it("clicking a multi-call session's row writes ?session_id= alongside ?log_id=", async () => {
      const user = userEvent.setup();
      respondWith([
        logEntry({ request_id: "req-llm", session_id: "sess-1", session_total_count: 3 }),
        logEntry({ request_id: "req-llm-2", session_id: "sess-1", session_total_count: 3 }),
      ]);
      renderPanel();

      await waitFor(() => expect(row("req-llm")).not.toBeNull());
      await user.click(row("req-llm") as HTMLElement);

      await waitFor(() => expect(urlParams().get("session_id")).toBe("sess-1"));
      expect(urlParams().get("log_id")).toBe("req-llm");
      await waitFor(() => expect(drawer()).toHaveAttribute("data-session-id", "sess-1"));
    });

    it("selecting another log while a session view is open keeps the session open", async () => {
      const user = userEvent.setup();
      respondWith([
        logEntry({ request_id: "req-llm", session_id: "sess-1", session_total_count: 3 }),
        logEntry({ request_id: "req-unenriched" }),
      ]);
      renderPanel("?log_id=req-llm");

      await waitFor(() => expect(drawer()).toHaveAttribute("data-session-id", "sess-1"));

      await user.click(screen.getByRole("button", { name: "select-next-log" }));

      await waitFor(() => expect(drawer()).toHaveAttribute("data-log-id", "req-unenriched"));
      expect(urlParams().get("session_id")).toBe("sess-1");
      expect(drawer()).toHaveAttribute("data-session-id", "sess-1");
    });
  });

  /** 前置旧会话保存了无效开关；不显示或发送未支持筛选；beforeEach 清理会话和组件。 */
  it("does not offer or send the unsupported health check filter", async () => {
    sessionStorage.setItem("excludeInternalHealthChecks", "true");
    renderPanel();
    await waitFor(() => expect(uiSpendLogsCall).toHaveBeenCalled());
    expect(lastCall()?.params?.exclude_internal_health_checks).toBeUndefined();
    expect(screen.queryByRole("switch", { name: "Hide Health Checks" })).not.toBeInTheDocument();
  });

  /** 前置默认刷新状态；验证唯一开关可停止刷新，异常持久化内容不会使页面崩溃；会话与组件由框架清理。 */
  it("uses one refresh switch and tolerates malformed stored settings", async () => {
    sessionStorage.setItem("isLiveTail", "{broken");
    renderPanel();
    const toggle = screen.getByRole("switch", { name: "Refresh every 15 seconds" });
    expect(toggle).toBeChecked();
    await userEvent.click(toggle);
    expect(toggle).not.toBeChecked();
    expect(sessionStorage.getItem("isLiveTail")).toBe("false");
  });

  /** 前置列表接口失败；验证显示加载失败而非无日志，重试成功可恢复；mock 与缓存自动清理。 */
  it("distinguishes a failed query from an empty result and can retry", async () => {
    vi.mocked(uiSpendLogsCall).mockRejectedValueOnce(new Error("database unavailable"));
    renderPanel();
    expect(await screen.findByRole("alert")).toHaveTextContent("Logs could not be loaded");
    expect(screen.queryByText("No records in this time range")).not.toBeInTheDocument();
    respondWith([logEntry({ request_id: "recovered" })]);
    await userEvent.click(screen.getByRole("button", { name: "Refresh", exact: true }));
    expect(await screen.findByText("recovered")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  /** 前置详情接口返回不可访问；验证不将失败吞掉，且不会打开其他记录；mock 与缓存自动清理。 */
  it("shows an explicit error when the linked detail is unavailable", async () => {
    vi.mocked(uiSpendLogDetailsCall).mockRejectedValue(new Error("not found"));
    renderPanel("?log_id=private-log");
    expect(await screen.findByRole("alert")).toHaveTextContent("This log could not be opened");
    expect(screen.getByTestId("log-details-drawer")).toHaveTextContent("closed");
  });
});
