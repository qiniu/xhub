import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chooseSelectOption, renderWithProviders, testQueryClient } from "../../../tests/test-utils";
import { LOG_FILTER_IDS } from "./log_filter_logic";
import { RequestLogsFilters } from "./RequestLogsFilters";
import { setActiveLocale } from "@/i18n";

const scope = vi.hoisted(() => ({ admin: true }));
vi.mock("@/app/(dashboard)/hooks/sessionIdentity/useSessionIdentity", () => ({
  useIsPlatformAdmin: () => scope.admin,
  useIsTeamAdminForAnyTeam: () => false,
}));
vi.mock("@/app/(dashboard)/hooks/useIsOrgAdmin", () => ({ default: () => false }));
vi.mock("@/app/(dashboard)/hooks/spendLogs/useSpendLogUsers", () => ({ useInfiniteSpendLogUsers: vi.fn() }));
import { useInfiniteSpendLogUsers } from "@/app/(dashboard)/hooks/spendLogs/useSpendLogUsers";
const emptyQuery = {
  data: { pages: [], pageParams: [] },
  fetchNextPage: vi.fn(),
  hasNextPage: false,
  isFetchingNextPage: false,
  isLoading: false,
  isError: false,
};
const logsWindow = { start_date: "2026-11-01 00:00:00", end_date: "2026-11-02 00:00:00" };

/** 渲染可变筛选草稿；参数指定错误视图，返回表单。状态由测试框架卸载，无后台写入。 */
function StatefulFilters({ errorsOnly = false }: { errorsOnly?: boolean }) {
  const [values, setValues] = useState<Record<string, unknown>>({});
  return (
    <RequestLogsFilters
      get={(id) => values[id]}
      set={(id, value) => setValues((old) => ({ ...old, [id]: value }))}
      teams={[]}
      logsWindow={logsWindow}
      errorsOnly={errorsOnly}
    />
  );
}

/** 渲染固定筛选草稿并返回变更观察器；测试框架卸载组件且清理查询缓存。 */
function renderFilters(errorsOnly = false) {
  const set = vi.fn();
  renderWithProviders(
    <RequestLogsFilters get={() => undefined} set={set} teams={[]} logsWindow={logsWindow} errorsOnly={errorsOnly} />,
  );
  return set;
}

describe("RequestLogsFilters", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    testQueryClient.clear();
    scope.admin = true;
    setActiveLocale("en");
    vi.mocked(useInfiniteSpendLogUsers).mockReturnValue(
      emptyQuery as unknown as ReturnType<typeof useInfiniteSpendLogUsers>,
    );
  });

  /** 前置普通日志；常用模型和账号直接可见，高级筛选折叠且不出现未实现字段；自动卸载。 */
  it("prioritizes common filters and removes unsupported or duplicated inputs", () => {
    renderFilters();
    expect(screen.getByLabelText("Model")).toBeVisible();
    expect(screen.getByPlaceholderText("Search an internal user")).toBeVisible();
    expect(screen.getByLabelText("Key Hash")).not.toBeVisible();
    expect(screen.queryByText("End User")).not.toBeInTheDocument();
    expect(screen.queryByText("Public model / search tool")).not.toBeInTheDocument();
    expect(screen.queryByText("Error Message")).not.toBeInTheDocument();
  });

  /** 前置错误日志；状态码/关键词直接可用，不提供与错误视图冲突的状态或缓存筛选；自动卸载。 */
  it("prioritizes error code and message only in the error view", () => {
    renderFilters(true);
    expect(screen.getByPlaceholderText("Select or type an error code")).toBeVisible();
    expect(screen.getByLabelText("Error Message")).toBeVisible();
    expect(screen.queryByText("Gateway response cache")).not.toBeInTheDocument();
    expect(screen.queryByText("Status", { exact: true })).not.toBeInTheDocument();
  });

  /** 前置普通成员；不呈现管理账号选择器，范围继续由后台决定；mock 在下一例重置。 */
  it("does not expose admin scope controls to ordinary members", () => {
    scope.admin = false;
    renderFilters();
    expect(screen.queryByPlaceholderText("Search an internal user")).not.toBeInTheDocument();
    expect(useInfiniteSpendLogUsers).not.toHaveBeenCalled();
  });

  /** 前置历史型号和已删除密钥；可直接输入精确名称，不依赖当前目录；只改本地草稿。 */
  it("accepts historical models and key aliases and preserves original text", async () => {
    const user = userEvent.setup();
    renderWithProviders(<StatefulFilters />);
    await user.type(screen.getByLabelText("Model"), "retired-模型");
    await user.click(screen.getByText("Advanced filters"));
    await user.type(screen.getByLabelText("Key Alias"), "deleted-key");
    expect(screen.getByLabelText("Model")).toHaveValue("retired-模型");
    expect(screen.getByLabelText("Key Alias")).toHaveValue("deleted-key");
  });

  /** 前置分页用户选项；选择后发送准确 user_id，文字搜索发送时间范围与查询，不跨权限拉取全目录；mock 清理。 */
  it("uses the scoped user lookup and passes its chosen value", async () => {
    vi.mocked(useInfiniteSpendLogUsers).mockReturnValue({
      ...emptyQuery,
      data: { pages: [{ data: ["alice"] }] },
    } as unknown as ReturnType<typeof useInfiniteSpendLogUsers>);
    const set = renderFilters();
    const user = userEvent.setup();
    await chooseSelectOption(user, screen.getByPlaceholderText("Search an internal user"), "alice");
    expect(set).toHaveBeenCalledWith(LOG_FILTER_IDS.USER_ID, "alice");
    expect(useInfiniteSpendLogUsers).toHaveBeenCalledWith(logsWindow, 50, undefined);
  });

  /** 前置用户目录失败；显示加载失败而非不存在用户；mock 清理。 */
  it("distinguishes failed option lookup from an empty directory", async () => {
    vi.mocked(useInfiniteSpendLogUsers).mockReturnValue({ ...emptyQuery, isError: true } as unknown as ReturnType<
      typeof useInfiniteSpendLogUsers
    >);
    renderFilters();
    await userEvent.click(screen.getByPlaceholderText("Search an internal user"));
    expect(await screen.findByText("Could not load options. Please try again.")).toBeVisible();
  });

  /** 前置用户列表仍有下一页；滚动后继续分页且不一次取全量，mock 清理。 */
  it("loads another bounded page of visible users", async () => {
    const fetchNextPage = vi.fn();
    vi.mocked(useInfiniteSpendLogUsers).mockReturnValue({
      ...emptyQuery,
      fetchNextPage,
      hasNextPage: true,
      data: { pages: [{ data: ["alice"] }] },
    } as unknown as ReturnType<typeof useInfiniteSpendLogUsers>);
    renderFilters();
    await userEvent.click(screen.getByPlaceholderText("Search an internal user"));
    const list = await screen.findByTestId("paginated-search-select-list");
    for (const [key, value] of Object.entries({ scrollTop: 90, clientHeight: 10, scrollHeight: 100 }))
      Object.defineProperty(list, key, { value, configurable: true });
    fireEvent.scroll(list);
    await waitFor(() => expect(fetchNextPage).toHaveBeenCalled());
  });

  /** 前置高级缓存条件；命中/未命中映射真实后台参数；组件和查询缓存由框架清理。 */
  it.each([
    ["Cache Hit", "hit"],
    ["Cache Miss", "miss"],
  ])("maps %s to %s", async (label, value) => {
    const user = userEvent.setup();
    const set = renderFilters();
    await user.click(screen.getByText("Advanced filters"));
    await user.click(screen.getByText("All Requests"));
    await user.click(await screen.findByRole("option", { name: label }));
    expect(set).toHaveBeenCalledWith(LOG_FILTER_IDS.CACHE_STATUS, value);
  });

  /** 前置错误视图；常见 499 和自定义有效状态均发送数字，非法值不能成为选项；自动清理。 */
  it("supports cancellation and valid custom codes without accepting arbitrary text", async () => {
    const user = userEvent.setup();
    const set = renderFilters(true);
    const input = screen.getByPlaceholderText("Select or type an error code");
    await user.click(input);
    await user.click(screen.getByRole("option", { name: "499 - Connection interrupted" }));
    expect(set).toHaveBeenCalledWith(LOG_FILTER_IDS.ERROR_CODE, "499");
    await user.click(input);
    await user.type(input, "418");
    await user.click(screen.getByRole("option", { name: "Use custom code: 418" }));
    expect(set).toHaveBeenCalledWith(LOG_FILTER_IDS.ERROR_CODE, "418");
    await user.click(input);
    await user.clear(input);
    await user.type(input, "oops");
    expect(within(screen.getByTestId("error-code-filter-list")).queryByRole("option")).not.toBeInTheDocument();
  });

  /** 前置中文界面；语义翻译覆盖高级字段与 499，不修改输入内容；locale 由测试 setup 重置。 */
  it("translates common and advanced filters into Chinese", async () => {
    setActiveLocale("zh-CN");
    renderFilters(true);
    expect(screen.getByLabelText("模型")).toBeVisible();
    await userEvent.click(screen.getByText("高级筛选"));
    expect(screen.getByLabelText("供应商连接")).toBeVisible();
    expect(screen.queryByText("Advanced filters")).not.toBeInTheDocument();
  });
});
