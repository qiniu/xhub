import { screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { renderWithProviders, testQueryClient } from "../../../tests/test-utils";
import RequestLogsPanel from "./RequestLogsPanel";
import { useLogDetails } from "@/app/(dashboard)/hooks/logDetails/useLogDetails";
import { uiSpendLogDetailsCall } from "../networking";

vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: () => ({ accessToken: "detail-session" }) }));
vi.mock("../networking", async (load) => ({
  ...await load<typeof import("../networking")>(),
  uiSpendLogsCall: vi.fn().mockResolvedValue({ data: [], total: 0, page: 1, page_size: 50, total_pages: 0 }),
  uiSpendLogDetailsCall: vi.fn(),
}));
vi.mock("@/components/key_team_helpers/filter_helpers", () => ({ fetchAllTeams: vi.fn().mockResolvedValue([]) }));
vi.mock("./LogDetailsDrawer", () => ({
  /** 抽屉夹具保留真实详情 hook，只省略无关布局；参数来自真实面板，无后台写入，框架卸载清理。 */
  LogDetailsDrawer: ({ open, logEntry }: { open: boolean; logEntry?: { request_id: string } | null }) => {
    const detail = useLogDetails(logEntry?.request_id, undefined, open);
    return <div role="status">{open && detail.isSuccess ? detail.data?.response?.message : "waiting"}</div>;
  },
}));

beforeEach(() => {
  testQueryClient.clear();
  vi.mocked(uiSpendLogDetailsCall).mockReset();
});

/** 前置无缓存且日志不在当前页；深链接与抽屉共享一次完整正文读取，finally 卸载组件并清理缓存。 */
it("loads the body only once when a cold deep link opens the drawer", async () => {
  vi.mocked(uiSpendLogDetailsCall).mockResolvedValue({ request_id: "historical-detail", response: { message: "Original body" } });
  const view = renderWithProviders(<RequestLogsPanel accessToken="detail-session" token="session-cookie" userRole="Admin" userID="viewer" isActive />,
    { searchParams: "?log_id=historical-detail" });
  try {
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Original body"));
    expect(uiSpendLogDetailsCall).toHaveBeenCalledTimes(1);
    expect(uiSpendLogDetailsCall).toHaveBeenCalledWith("detail-session", "historical-detail", "");
  } finally { view.unmount(); testQueryClient.clear(); }
});
