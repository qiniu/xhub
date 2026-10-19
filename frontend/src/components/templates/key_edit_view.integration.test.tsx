import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chooseSelectOption, renderWithProviders } from "../../../tests/test-utils";
import { I18nProvider, useI18n, translate, setActiveLocale, LOCALE_COOKIE } from "@/i18n";
import { modelAvailableCall } from "../networking";
import { KeyResponse } from "../key_team_helpers/key_list";
import { KeyEditView } from "./key_edit_view";

vi.mock("../networking", async (load) => ({
  ...(await load<typeof import("../networking")>()),
  modelAvailableCall: vi.fn().mockResolvedValue({ data: [{ id: "gpt-4" }, { id: "gpt-4o-mini" }] }),
  getRouteTemplatesCall: vi.fn().mockResolvedValue([{ id: "template-1", name: "User template" }]),
  getRouteTemplateBindingCall: vi
    .fn()
    .mockResolvedValue({ route_template_id: "", effective: { scope_type: "platform" } }),
}));
vi.mock("../organisms/create_key_button", () => ({ fetchTeamModels: vi.fn().mockResolvedValue(["team-model"]) }));
const key = {
  token: "hash-1",
  token_id: "hash-1",
  key_alias: "用户原始 alias",
  models: ["gpt-4"],
  allowed_routes: ["llm_api_routes"],
  max_budget: 10,
  rpm_limit: 20,
  tpm_limit: 1000,
  auto_rotate: false,
  metadata: { guardrails: ["existing"] },
  object_permission: { agents: ["agent"] },
} as unknown as KeyResponse;

/** 提供真实编辑器和可控保存回调；参数覆盖密钥快照，返回渲染及保存函数，DOM 由框架清理。 */
function setup(overrides: Partial<KeyResponse> = {}, onSubmit = vi.fn().mockResolvedValue(undefined)) {
  const onCancel = vi.fn();
  const view = renderWithProviders(
    <KeyEditView
      keyData={{ ...key, ...overrides }}
      onSubmit={onSubmit}
      onCancel={onCancel}
      accessToken="session"
      userID="owner"
      userRole="Admin"
      teams={[]}
    />,
  );
  return { ...view, onSubmit, onCancel };
}
/** 切换真实 i18n 上下文；仅用于测试，返回按钮，点击时修改语言且保留用户表单内容。 */
function LanguageSwitch() {
  const { setLocale } = useI18n();
  return <button onClick={() => setLocale("zh-CN")}>中文</button>;
}

beforeEach(() => {
  vi.clearAllMocks();
  setActiveLocale("en");
  localStorage.clear();
  document.cookie = LOCALE_COOKIE + "=;max-age=0;path=/";
  vi.mocked(modelAvailableCall).mockResolvedValue({ data: [{ id: "gpt-4" }, { id: "gpt-4o-mini" }] });
});
describe("simplified key editor", () => {
  /** 正常基础编辑；前置含历史权限的密钥，只显示可见字段且提交不含隐藏权限，自动清理 DOM。 */
  it("only exposes basic settings and preserves hidden configuration", async () => {
    const { onSubmit } = setup();
    expect(screen.getByLabelText("Key Alias")).toHaveValue(key.key_alias);
    for (const label of [
      "Object Permissions",
      "Guardrails",
      "Policies",
      "Metadata",
      "Skills",
      "Logging Settings",
      "Max Parallel Requests",
      "Soft Budget",
    ])
      expect(screen.queryByText(label, { exact: true })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Key Alias"), { target: { value: "updated 用户" } });
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    const payload = onSubmit.mock.calls[0][0];
    expect(payload).toMatchObject({
      token: "hash-1",
      key_alias: "updated 用户",
      models: ["gpt-4"],
    });
    for (const field of [
      "metadata",
      "object_permission",
      "guardrails",
      "allowed_routes",
      "duration",
      "rotation_interval",
      "soft_budget",
    ])
      expect(payload).not.toHaveProperty(field);
  });
  /** 空模型与继承路由边界；无模型限制时保存空数组和空模板，不转换成宽权限标记，清理 DOM。 */
  it("submits empty models and an inherited route template", async () => {
    const { onSubmit } = setup({ models: [], route_template_id: "" });
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ models: [], route_template_id: "" })),
    );
  });
  /** 数值边界覆盖固定值、零及共享；改动真实数字输入，保存精确保留输入语义，清理 DOM。 */
  it.each(["12", "0", ""])("retains rate allocation %s", async (value) => {
    const { onSubmit } = setup();
    for (const name of ["RPM Limit", "TPM Limit"]) fireEvent.change(screen.getByLabelText(name), { target: { value } });
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ rpm_limit: value, tpm_limit: value })),
    );
  });
  /** 失败输入不产生保存；非法负数、小数和溢出均显示可定位错误，清理 DOM。 */
  it.each(["-1", "1.5", "2147483648"])("rejects invalid RPM %s", async (value) => {
    const { onSubmit } = setup();
    fireEvent.change(screen.getByLabelText("RPM Limit"), { target: { value } });
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    expect(await screen.findByText("Must be a non-negative whole number")).toBeVisible();
    expect(onSubmit).not.toHaveBeenCalled();
  });
  /** 金额失败输入不发出保存；负数显示对应字段错误，允许零及小数的边界由精度测试覆盖，清理 DOM。 */
  it("rejects a negative budget", async () => {
    const { onSubmit } = setup();
    fireEvent.change(screen.getByLabelText("Max Budget (USD)"), { target: { value: "-0.01" } });
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    expect(await screen.findByText(translate("en", "keyEditor.invalidBudget"))).toBeVisible();
    expect(onSubmit).not.toHaveBeenCalled();
  });
  /** 生命周期能力边界；只保存指定期限，后台未实现的自动轮换禁用且不随请求发送，清理 DOM。 */
  it("saves expiry and explains unavailable automatic rotation", async () => {
    const { onSubmit } = setup();
    fireEvent.change(screen.getByLabelText("Expire Key"), { target: { value: "30d" } });
    expect(screen.getByRole("switch", { name: "Enable Auto-Rotation" })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByText(translate("en", "keyEditor.rotationUnavailable"))).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ duration: "30d" })));
    expect(onSubmit.mock.calls[0][0]).not.toHaveProperty("auto_rotate");
    expect(onSubmit.mock.calls[0][0]).not.toHaveProperty("rotation_interval");
  });
  /** 非法期限失败路径；空期限之外的非法格式阻止请求并提示，清理 DOM。 */
  it.each(["0d", "-1h", "tomorrow"])("rejects expiry %s", async (value) => {
    const { onSubmit } = setup();
    fireEvent.change(screen.getByLabelText("Expire Key"), { target: { value } });
    await userEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    expect(await screen.findByText(translate("en", "keyEditor.invalidDuration"))).toBeVisible();
    expect(onSubmit).not.toHaveBeenCalled();
  });
  /** 路由模板选择写入真实选择值；使用可访问名称操作选项，返回模板 ID 而非展示名，清理 DOM。 */
  it("selects a route template", async () => {
    const { onSubmit } = setup();
    const user = userEvent.setup();
    await chooseSelectOption(user, screen.getByRole("combobox", { name: "Route Templates" }), "User template");
    await user.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ route_template_id: "template-1" })),
    );
  });
  /** 模型加载失败仍保留原选择并可取消；前置拒绝请求，提示可恢复错误，清理 DOM。 */
  it("preserves selections when model loading fails", async () => {
    vi.mocked(modelAvailableCall).mockRejectedValueOnce(new Error("offline"));
    const { onCancel } = setup();
    expect(await screen.findByRole("alert")).toHaveTextContent(translate("en", "keyEditor.modelsError"));
    expect(screen.getByText("gpt-4", { exact: true })).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();
  });
  /** 语言切换覆盖标签、共享额度提示、未支持状态和非法提示，原始别名不被翻译，清理 DOM 与存储。 */
  it("switches all editor copy without changing user input", async () => {
    renderWithProviders(
      <I18nProvider initialLocale="en">
        <LanguageSwitch />
        <KeyEditView
          keyData={key}
          onSubmit={vi.fn()}
          onCancel={vi.fn()}
          accessToken="session"
          userID="owner"
          userRole="Admin"
        />
      </I18nProvider>,
    );
    expect(screen.getByLabelText("Max Budget (USD)")).toHaveAttribute(
      "placeholder",
      translate("en", "keyEditor.budgetPlaceholder"),
    );
    await userEvent.click(screen.getByRole("button", { name: "中文" }));
    expect(screen.getByLabelText(translate("zh-CN", "Key Alias"))).toHaveValue("用户原始 alias");
    expect(screen.getByLabelText(translate("zh-CN", "Max Budget (USD)"))).toHaveAttribute(
      "placeholder",
      translate("zh-CN", "keyEditor.budgetPlaceholder"),
    );
    expect(screen.getByText(translate("zh-CN", "keyEditor.rotationUnavailable"))).toBeVisible();
    expect(screen.queryByText("Key Lifecycle")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("密钥有效期"), { target: { value: "0d" } });
    await userEvent.click(screen.getByRole("button", { name: translate("zh-CN", "Save Changes") }));
    expect(await screen.findByText(translate("zh-CN", "keyEditor.invalidDuration"))).toBeVisible();
  });
});
