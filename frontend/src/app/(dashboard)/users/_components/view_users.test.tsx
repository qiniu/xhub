/* @vitest-environment jsdom */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "../../../../../tests/test-utils";
import { translate } from "@/i18n/translate";
import ViewUserDashboard from "./view_users";
import { toast } from "@/lib/toast";
import { userDeleteCall } from "@/components/networking";

const userListCall = vi.fn();

// Mock the networking module
vi.mock("@/components/networking", () => ({
  userListCall: (...args: unknown[]) => userListCall(...args),
  userDeleteCall: vi.fn().mockResolvedValue({}),
  modelAvailableCall: vi.fn().mockResolvedValue({ data: [] }),
  userUpdateUserCall: vi.fn().mockResolvedValue({}),
  getProxyBaseUrl: vi.fn().mockReturnValue("http://localhost:4000"),
  getProxyUISettings: vi.fn().mockResolvedValue({
    PROXY_BASE_URL: null,
    PROXY_LOGOUT_URL: null,
    DEFAULT_TEAM_DISABLED: false,
    SSO_ENABLED: false,
  }),
}));

// The detail view has its own test; stub it so this file covers the parent's swap.
vi.mock("./view_users/user_info_view", () => ({
  default: function UserInfoViewMock({ userId, startInEditMode }: { userId: string; startInEditMode?: boolean }) {
    return <div data-testid="user-info-view">{`detail:${userId}:${String(Boolean(startInEditMode))}`}</div>;
  },
}));

vi.mock("./default-user-settings/DefaultUserSettingsForm", () => ({
  DefaultUserSettingsForm: function DefaultUserSettingsFormMock() {
    const [value, setValue] = React.useState("");
    return (
      <section aria-label="Default user settings panel">
        <label>
          Default setting
          <input value={value} onChange={(event) => setValue(event.target.value)} />
        </label>
      </section>
    );
  },
}));

const makeUser = (userId: string, email: string) => ({
  user_id: userId,
  user_email: email,
  user_alias: null,
  user_role: "Admin",
  spend: 100.5,
  max_budget: null,
  models: [],
  key_count: 2,
  created_at: "2024-01-01T00:00:00Z",
  updated_at: "2024-01-01T00:00:00Z",
  sso_user_id: null,
  budget_duration: null,
});

const createQueryClient = () =>
  new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: 0,
      },
    },
  });

const defaultProps = {
  accessToken: "test-token",
  token: "test-token",
  userRole: "Admin",
  userID: "admin-user-id",
  teams: [],
};

const renderDashboard = (overrides: Partial<typeof defaultProps> = {}) =>
  renderWithProviders(
    <QueryClientProvider client={createQueryClient()}>
      <ViewUserDashboard {...defaultProps} {...overrides} />
    </QueryClientProvider>,
  );

describe("ViewUserDashboard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(userDeleteCall).mockResolvedValue({});
    userListCall.mockResolvedValue({
      users: [makeUser("user-1", "test@example.com")],
      total: 1,
      page: 1,
      page_size: 25,
      total_pages: 1,
    });
  });

  /** 无角色接口替身时仍提供真实两种账户角色；前置管理员页面，核对筛选选项，渲染由测试框架清理。 */
  it("offers account role filters without a role API", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await screen.findByText("test@example.com");
    await user.click(screen.getByRole("button", { name: "Filters", exact: true }));
    await user.click(screen.getByPlaceholderText(translate("en", "pages.users.selectRole")));
    expect(
      await screen.findByRole("option", { name: translate("en", "Platform administrator"), exact: true }),
    ).toBeInTheDocument();
    expect(screen.getByRole("option", { name: translate("en", "Regular user"), exact: true })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "available_roles" })).not.toBeInTheDocument();
  });

  it("should render the ViewUserDashboard component", async () => {
    renderDashboard();

    expect(await screen.findByText("test@example.com")).toBeInTheDocument();

    expect(screen.queryByRole("tab", { name: "Default User Settings" })).not.toBeInTheDocument();
  });

  it("does not offer a default user settings tab", async () => {
    renderDashboard();

    expect(await screen.findByText("test@example.com")).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Default User Settings" })).not.toBeInTheDocument();
  });

  /** 验证创建及批量导入入口位于管理工具栏，使用当前翻译名称；前置管理员渲染，组件测试自动清理。 */
  it("renders invite and bulk invite as toolbar actions alongside the other admin controls", async () => {
    renderDashboard();

    const inviteButton = await screen.findByRole("button", { name: /\+ create user/i });
    const bulkInviteButton = screen.getByRole("button", { name: translate("en", "userCsv.title") });
    const toolbar = screen.getByTestId("toggle-user-selection").parentElement;

    expect(inviteButton.parentElement).toBe(toolbar);
    expect(bulkInviteButton.parentElement).toBe(toolbar);
  });

  it("shows the users table without admin controls for non-proxy admins", async () => {
    renderDashboard({ userRole: "Internal User" });

    expect(await screen.findByText("test@example.com")).toBeInTheDocument();
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(screen.queryByTestId("toggle-user-selection")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /\+ create user/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: translate("en", "userCsv.title") })).not.toBeInTheDocument();
  });

  it("keeps actions unavailable while the user list is loading", () => {
    userListCall.mockReturnValue(new Promise(() => undefined));

    renderDashboard();

    expect(screen.getByText("Loading users…")).toBeInTheDocument();
    expect(screen.queryByTestId("toggle-user-selection")).not.toBeInTheDocument();
    expect(screen.queryByTestId("bulk-edit-users")).not.toBeInTheDocument();
  });

  it("should show delete modal after choosing delete from the row actions menu", async () => {
    const user = userEvent.setup();
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("test@example.com")).toBeInTheDocument();
    });

    expect(screen.queryByText("Delete User?")).not.toBeInTheDocument();

    await user.click(screen.getByTestId("user-actions-user-1"));
    await user.click(await screen.findByTestId("user-action-delete"));

    await waitFor(() => {
      expect(screen.getByText("Delete User?")).toBeInTheDocument();
    });
    expect(
      screen.getByText("Are you sure you want to delete this user? This action cannot be undone."),
    ).toBeInTheDocument();
    expect(screen.getAllByText("user-1").length).toBeGreaterThan(0);
  });

  it("should swap to the detail view when the identity cell is clicked", async () => {
    const user = userEvent.setup();
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("test@example.com")).toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: /user-1/ }));

    expect(await screen.findByTestId("user-info-view")).toHaveTextContent("detail:user-1:false");
    expect(screen.queryByText("test@example.com")).not.toBeInTheDocument();
  });

  it("does not offer an edit action in the row menu", async () => {
    const user = userEvent.setup();
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("test@example.com")).toBeInTheDocument();
    });

    await user.click(screen.getByTestId("user-actions-user-1"));
    expect(screen.queryByTestId("user-action-edit")).not.toBeInTheDocument();
  });


  /** 删除后重新读取真实列表形状；前置两行，连续删除后总数变为空；模拟查询和 DOM 由框架清理。 */
  it("refreshes the total after consecutive deletions", async () => {
    const user = userEvent.setup();
    let rows = [makeUser("user-1", "one@example.com"), makeUser("user-2", "two@example.com")];
    userListCall.mockImplementation(async () => ({ users: [...rows], total: rows.length, page: 1, page_size: 25, total_pages: 1 }));
    vi.mocked(userDeleteCall).mockImplementation(async (_token, id) => { rows = rows.filter(row => row.user_id !== id); return {}; });
    renderDashboard();
    for (const [id, email, count] of [["user-1", "one@example.com", 1], ["user-2", "two@example.com", 0]] as const) {
      await screen.findByText(email);
      await user.click(screen.getByTestId("user-actions-" + id));
      await user.click(await screen.findByTestId("user-action-delete"));
      await user.click(screen.getByRole("button", { name: "Delete", exact: true }));
      await waitFor(() => expect(screen.queryByText(email)).not.toBeInTheDocument());
      expect(screen.getByTestId("pagination-range")).toHaveTextContent(count ? "Showing 1-1 of 1" : "No results");
    }
  });

  /** 最后一页仅一行时删除后回前页；前置26条分页响应，验证重新请求第1页并显示25条，不修改外部数据。 */
  it.each([false, true])("returns to the last remaining page after deleting its only row (refresh failure: %s)", async (failRefresh) => {
    const user = userEvent.setup();
    let deleted = false;
    userListCall.mockImplementation(async (_token, _ids, page) => {
      if (deleted && page === 2 && failRefresh) throw new Error("List unavailable");
      return {
      users: page === 2 ? (deleted ? [] : [makeUser("last", "last@example.com")]) : [makeUser("first", "first@example.com")],
      total: deleted ? 25 : 26, page, page_size: 25, total_pages: deleted ? 1 : 2,
    }; });
    vi.mocked(userDeleteCall).mockImplementation(async () => { deleted = true; return {}; });
    renderDashboard();
    await screen.findByText("first@example.com");
    await user.click(screen.getByTestId("pagination-next"));
    await screen.findByText("last@example.com");
    await user.click(screen.getByTestId("user-actions-last"));
    await user.click(await screen.findByTestId("user-action-delete"));
    await user.click(screen.getByRole("button", { name: "Delete", exact: true }));
    await screen.findByText("first@example.com");
    await waitFor(() => expect(screen.getByTestId("pagination-range")).toHaveTextContent("Showing 1-25 of 25"));
    expect(screen.getByTestId("pagination-next")).toBeDisabled();
  });

  /** 后台确认删除但列表刷新失败时不能恢复旧行；覆盖唯一行、连续删除和其他筛选缓存，模拟缓存由框架清理。 */
  it("keeps confirmed deletions when refetch fails without corrupting other filters", async () => {
    const user = userEvent.setup();
    const warning = vi.spyOn(toast, "warning");
    const client = createQueryClient();
    const other = { users: [makeUser("other", "other@example.com")], total: 1, page: 1, page_size: 25, total_pages: 1 };
    client.setQueryDefaults(["userList"], { gcTime: Infinity });
    client.setQueryData(["userList", { search: "other" }], other);
    userListCall.mockResolvedValueOnce({ ...other, users: [makeUser("user-1", "one@example.com"), makeUser("user-2", "two@example.com")], total: 2 })
      .mockRejectedValue(new Error("List unavailable"));
    renderWithProviders(<QueryClientProvider client={client}><ViewUserDashboard {...defaultProps} /></QueryClientProvider>);
    for (const [id, email, count] of [["user-1", "one@example.com", 1], ["user-2", "two@example.com", 0]] as const) {
      await screen.findByText(email);
      await user.click(screen.getByTestId("user-actions-" + id));
      await user.click(await screen.findByTestId("user-action-delete"));
      await user.click(screen.getByRole("button", { name: "Delete", exact: true }));
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      expect(screen.queryByText(email)).not.toBeInTheDocument();
      expect(screen.getByTestId("pagination-range")).toHaveTextContent(count ? "Showing 1-1 of 1" : "No results");
    }
    expect(warning).toHaveBeenCalledWith(translate("en", "consoleDirectory.userDeleteRefreshFailed"));
    expect(client.getQueryData(["userList", { search: "other" }])).toEqual(other);
    warning.mockRestore();
  });

  /** 删除接口失败时保留行和总数；前置一行及拒绝响应，验证失败不会伪装已删，模拟数据自动清理。 */
  it("keeps rows and total when deletion fails", async () => {
    const user = userEvent.setup();
    vi.mocked(userDeleteCall).mockRejectedValue(new Error("Deletion rejected"));
    renderDashboard();
    await screen.findByText("test@example.com");
    await user.click(screen.getByTestId("user-actions-user-1"));
    await user.click(await screen.findByTestId("user-action-delete"));
    await user.click(screen.getByRole("button", { name: "Delete", exact: true }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByText("test@example.com")).toBeInTheDocument();
    expect(screen.getByTestId("pagination-range")).toHaveTextContent("Showing 1-1 of 1");
  });

  describe("bulk edit selection", () => {
    beforeEach(() => {
      userListCall.mockResolvedValue({
        users: [makeUser("user-1", "ada@example.com"), makeUser("user-2", "grace@example.com")],
        total: 2,
        page: 1,
        page_size: 25,
        total_pages: 1,
      });
    });

    it("reveals selection checkboxes only while selection mode is on", async () => {
      const user = userEvent.setup();
      renderDashboard();

      await waitFor(() => {
        expect(screen.getByText("ada@example.com")).toBeInTheDocument();
      });

      expect(screen.queryByTestId("datatable-select-all")).not.toBeInTheDocument();

      await user.click(screen.getByTestId("toggle-user-selection"));
      expect(screen.getByTestId("datatable-select-all")).toBeInTheDocument();

      await user.click(screen.getByTestId("toggle-user-selection"));
      expect(screen.queryByTestId("datatable-select-all")).not.toBeInTheDocument();
    });

    it("counts the selected rows in the bulk edit button and enables it once a row is picked", async () => {
      const user = userEvent.setup();
      renderDashboard();

      await waitFor(() => {
        expect(screen.getByText("ada@example.com")).toBeInTheDocument();
      });

      await user.click(screen.getByTestId("toggle-user-selection"));

      const bulkEdit = screen.getByTestId("bulk-edit-users");
      expect(bulkEdit).toHaveTextContent(translate("en", "pages.users.bulkEditCount", { count: 0 }));
      expect(bulkEdit).toBeDisabled();

      await user.click(screen.getByTestId("datatable-select-row-user-2"));
      expect(screen.getByTestId("bulk-edit-users")).toHaveTextContent(
        translate("en", "pages.users.bulkEditCount", { count: 1 }),
      );
      expect(screen.getByTestId("bulk-edit-users")).toBeEnabled();

      await user.click(screen.getByTestId("datatable-select-all"));
      expect(screen.getByTestId("bulk-edit-users")).toHaveTextContent(
        translate("en", "pages.users.bulkEditCount", { count: 2 }),
      );
    });

    it("clears the selection when selection mode is cancelled", async () => {
      const user = userEvent.setup();
      renderDashboard();

      await waitFor(() => {
        expect(screen.getByText("ada@example.com")).toBeInTheDocument();
      });

      await user.click(screen.getByTestId("toggle-user-selection"));
      await user.click(screen.getByTestId("datatable-select-row-user-1"));
      expect(screen.getByTestId("bulk-edit-users")).toHaveTextContent(
        translate("en", "pages.users.bulkEditCount", { count: 1 }),
      );

      await user.click(screen.getByTestId("toggle-user-selection"));
      await user.click(screen.getByTestId("toggle-user-selection"));

      expect(screen.getByTestId("bulk-edit-users")).toHaveTextContent(
        translate("en", "pages.users.bulkEditCount", { count: 0 }),
      );
    });
  });

  describe("server-side query wiring", () => {
    it("requests page 1 with the default created_at desc sort", async () => {
      renderDashboard();

      await waitFor(() => {
        expect(userListCall).toHaveBeenCalled();
      });

      const [, userIds, page, pageSize, , , , , sortBy, sortOrder] = userListCall.mock.calls[0];
      expect(userIds).toBeNull();
      expect(page).toBe(1);
      expect(pageSize).toBe(25);
      expect(sortBy).toBe("created_at");
      expect(sortOrder).toBe("desc");
    });

    it("sends the clicked column as sort_by and resets to the first page", async () => {
      const user = userEvent.setup();
      renderDashboard();

      await waitFor(() => {
        expect(screen.getByText("test@example.com")).toBeInTheDocument();
      });

      await user.click(screen.getByTestId("sort-header-user_email"));

      await waitFor(() => {
        const latest = userListCall.mock.calls[userListCall.mock.calls.length - 1];
        expect(latest[8]).toBe("user_email");
        expect(latest[9]).toBe("asc");
        expect(latest[2]).toBe(1);
      });
    });

    it("sends the toolbar search as the combined search param instead of user_email", async () => {
      const user = userEvent.setup();
      renderDashboard();

      await waitFor(() => {
        expect(screen.getByText("test@example.com")).toBeInTheDocument();
      });

      const searchedUserId = "a6f5c02b-0163-45ce-815f-f88d10e95686";
      await user.type(screen.getByPlaceholderText("Search by email or ID…"), searchedUserId);

      await waitFor(() => {
        const latest = userListCall.mock.calls[userListCall.mock.calls.length - 1];
        expect(latest[11]).toBe(searchedUserId);
      });
      const latest = userListCall.mock.calls[userListCall.mock.calls.length - 1];
      expect(latest[1]).toBeNull();
      expect(latest[4]).toBeNull();
      expect(latest[2]).toBe(1);
    });

    it("replaces the previous rows with the loading state while the search request is pending", async () => {
      renderDashboard();
      expect(await screen.findByText("test@example.com")).toBeInTheDocument();

      userListCall.mockReturnValue(new Promise(() => undefined));
      fireEvent.change(screen.getByPlaceholderText("Search by email or ID…"), { target: { value: "zzznomatch" } });

      expect(await screen.findByText("Loading users…")).toBeInTheDocument();
      expect(screen.queryByText("test@example.com")).not.toBeInTheDocument();
    });
  });
});
