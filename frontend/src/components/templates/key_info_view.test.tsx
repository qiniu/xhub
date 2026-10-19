import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { renderWithProviders } from "../../../tests/test-utils";
import { KeyResponse } from "../key_team_helpers/key_list";
import { keyDeleteCall, keyUpdateCall } from "../networking";
import { toast } from "@/lib/toast";
import KeyInfoView from "./key_info_view";

const editor = vi.hoisted(() => ({
  submit: undefined as undefined | ((values: Record<string, unknown>) => Promise<void>),
}));
vi.mock("./key_edit_view", () => ({
  /** 隔离页面协调逻辑；输入编辑器参数，返回可操作保存按钮和原始标题，无持久数据。 */
  KeyEditView: ({ onSubmit, header }: { onSubmit: typeof editor.submit; header: React.ReactNode }) => {
    editor.submit = onSubmit;
    return (
      <>
        {header}
        <button onClick={() => onSubmit?.({ key_alias: "renamed" })}>Save fixture</button>
      </>
    );
  },
}));
vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: vi.fn() }));
vi.mock("@/app/(dashboard)/hooks/useTeams", () => ({ default: () => ({ teams: [] }) }));
vi.mock("../networking", async (load) => ({
  ...(await load<typeof import("../networking")>()),
  keyUpdateCall: vi.fn(),
  keyDeleteCall: vi.fn().mockResolvedValue({}),
}));
vi.mock("@/lib/toast", () => ({ toast: { success: vi.fn(), fromError: vi.fn() } }));
const reset = vi.hoisted(() => vi.fn());
const block = vi.hoisted(() => vi.fn());
vi.mock("@/app/(dashboard)/hooks/keys/useResetKeySpend", () => ({
  useResetKeySpend: () => ({ mutate: reset, isPending: false }),
}));
vi.mock("@/app/(dashboard)/hooks/keys/useSetKeyBlockedState", () => ({
  useSetKeyBlockedState: () => ({ mutate: block, isPending: false }),
}));
const snapshot = {
  token: "hash",
  token_id: "stable-id",
  key_alias: "Original 用户",
  user_id: "owner",
  spend: 2,
  models: [],
  blocked: false,
  metadata: { hidden: true },
} as unknown as KeyResponse;
const auth = { accessToken: "session", userId: "owner", userRole: "Admin", premiumUser: true };
/** 渲染页面协调层；参数覆盖密钥数据，返回导航和更新监听器；DOM 自动清理，网络全部隔离。 */
function setup(keyData: KeyResponse | undefined = snapshot) {
  const close = vi.fn(),
    update = vi.fn(),
    deleted = vi.fn();
  renderWithProviders(
    <KeyInfoView
      keyId="stable-id"
      keyData={keyData}
      teams={[]}
      onClose={close}
      onKeyDataUpdate={update}
      onDelete={deleted}
    />,
  );
  return { close, update, deleted };
}
/** 打开非日常操作菜单；无参数，返回 Promise，不触发业务变更。 */
async function actions() {
  await userEvent.click(screen.getByRole("button", { name: "More key actions" }));
}
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useAuthorized).mockReturnValue(auth as ReturnType<typeof useAuthorized>);
  vi.mocked(keyUpdateCall).mockResolvedValue({ ...snapshot, key_alias: "renamed" });
});
describe("simple key details", () => {
  /** 页面直接编辑且说明默认折叠；无旧页签或高级卡片，返回操作正常，自动清理 DOM。 */
  it("shows the direct editor and collapsed guide", async () => {
    const { close } = setup();
    expect(screen.getByRole("heading", { name: snapshot.key_alias })).toBeVisible();
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(screen.getByText("How quota allocation works").closest("details")).not.toHaveAttribute("open");
    await userEvent.click(screen.getByRole("button", { name: "Back to keys" }));
    expect(close).toHaveBeenCalledOnce();
  });
  /** 保存仅传允许字段并精确保留共享与零；伪造隐藏配置也不能覆盖，成功同步响应，清理 DOM。 */
  it("allowlists updates and normalizes numeric allocations", async () => {
    const { update } = setup();
    await act(async () =>
      editor.submit?.({
        token: "evil",
        key_alias: "renamed",
        max_budget: "0.00001",
        rpm_limit: "",
        tpm_limit: "0",
        metadata: {},
        object_permission: {},
      }),
    );
    expect(keyUpdateCall).toHaveBeenCalledWith("session", {
      key: "stable-id",
      key_alias: "renamed",
      max_budget: 0.00001,
      rpm_limit: null,
      tpm_limit: 0,
    });
    expect(update).toHaveBeenCalledWith(expect.objectContaining({ key_alias: "renamed" }));
    expect(screen.getByRole("heading", { name: "renamed" })).toBeVisible();
  });
  /** 后台保存失败保持快照并显示错误；前置拒绝请求，不误报成功，清理 DOM。 */
  it("keeps the previous data when saving fails", async () => {
    vi.mocked(keyUpdateCall).mockRejectedValueOnce(new Error("quota exceeded"));
    const { update } = setup();
    await act(async () => editor.submit?.({ key_alias: "renamed" }));
    expect(update).not.toHaveBeenCalled();
    expect(toast.fromError).toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: snapshot.key_alias })).toBeVisible();
  });
  /** 只读用户不能触发写入；前置其他用户的浏览身份，隐藏菜单且禁用编辑，清理 DOM。 */
  it("disables mutation for viewers", async () => {
    vi.mocked(useAuthorized).mockReturnValue({ ...auth, userId: "other", userRole: "Internal Viewer" } as ReturnType<
      typeof useAuthorized
    >);
    setup();
    expect(screen.queryByRole("button", { name: "More key actions" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save fixture" })).toBeDisabled();
    await act(async () => editor.submit?.({ key_alias: "forbidden" }));
    expect(keyUpdateCall).not.toHaveBeenCalled();
  });
  /** 缺失密钥显示可返回状态；无接口写入，清理 DOM。 */
  it("handles missing keys", () => {
    renderWithProviders(<KeyInfoView keyId="missing" keyData={undefined} teams={[]} onClose={vi.fn()} />);
    expect(screen.getByText("Key not found")).toBeVisible();
  });
  /** 删除必须确认别名；成功调用稳定记录 ID，再刷新和返回，清理 DOM。 */
  it("deletes after alias confirmation", async () => {
    const { close, deleted } = setup();
    await actions();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Delete Key" }));
    await userEvent.type(screen.getByPlaceholderText(snapshot.key_alias), snapshot.key_alias);
    await userEvent.click(screen.getByRole("button", { name: "Delete", exact: true }));
    await waitFor(() => expect(keyDeleteCall).toHaveBeenCalledWith("session", "stable-id"));
    await waitFor(() => expect(close).toHaveBeenCalledOnce());
    expect(deleted).toHaveBeenCalledOnce();
  });
  /** 封禁以后台确认响应为准；前置管理员，确认后更新状态，清理 DOM。 */
  it("blocks after confirmation", async () => {
    block.mockImplementation((_input, callbacks) => callbacks.onSuccess({ blocked: true }));
    const { update } = setup();
    await actions();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Block Key" }));
    await userEvent.click(screen.getByRole("button", { name: "Block", exact: true }));
    expect(block).toHaveBeenCalledWith({ keyToken: "stable-id", blocked: true }, expect.any(Object));
    expect(update).toHaveBeenCalledWith({ blocked: true });
    expect(screen.getByText("Blocked", { exact: true })).toBeVisible();
  });
  /** 重置消费成功后更新快照；前置管理员和非零消费，确认对话框后触发，清理 DOM。 */
  it("resets spend through the confirmation dialog", async () => {
    reset.mockImplementation((_id, callbacks) => callbacks.onSuccess());
    const { update } = setup();
    await actions();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Reset Spend" }));
    await userEvent.click(screen.getByRole("button", { name: "Reset", exact: true }));
    expect(reset).toHaveBeenCalledWith("stable-id", expect.any(Object));
    expect(update).toHaveBeenCalledWith({ spend: 0 });
  });
});
