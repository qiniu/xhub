import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "@/lib/toast";

vi.mock("@/lib/toast", () => ({
  toast: { error: vi.fn(), success: vi.fn(), fromError: vi.fn() },
}));

// ---- Hoisted shared mocks (safe to use inside vi.mock factories) ----
const { keyUpdateCallMock, keyDeleteCallMock, mockUseAuthorized } = vi.hoisted(() => {
  return {
    keyUpdateCallMock: vi.fn().mockResolvedValue({}),
    keyDeleteCallMock: vi.fn().mockResolvedValue({}),
    mockUseAuthorized: vi.fn(),
  };
});

// ---- Module mocks ----

// Mock useAuthorized hook FIRST (before component imports it)
vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({
  default: mockUseAuthorized,
}));

vi.mock("@/app/(dashboard)/hooks/organizations/useOrganizations", () => ({
  useOrganizations: () => ({ data: [] }),
}));

// Networking: wire the hoisted fns so we can assert calls later
vi.mock("../networking", () => {
  return {
    serverRootPath: "",
    getRouteTemplatesCall: vi.fn().mockResolvedValue([]),
    getRouteTemplateBindingCall: vi.fn().mockResolvedValue({
      route_template_id: "",
      effective: { scope_type: "platform" },
    }),
    keyUpdateCall: (...args: any[]) => keyUpdateCallMock(...args),
    keyDeleteCall: (...args: any[]) => keyDeleteCallMock(...args),
  };
});

// Roles: ensure 'Admin' has write access and include all role helper functions
vi.mock("../../utils/roles", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../utils/roles")>();
  return {
    ...actual,
    rolesWithWriteAccess: ["Admin"],
  };
});

// Helpers used in rendering
vi.mock("@/utils/dataUtils", () => ({
  copyToClipboard: async () => true,
  formatNumberWithCommas: (n: any) => String(n),
}));
vi.mock("../key_info_utils", () => ({
  extractLoggingSettings: () => ({}),
  formatMetadataForDisplay: (m: any) => JSON.stringify(m, null, 2),
  stripTagsFromMetadata: (m: any) => m,
}));
vi.mock("../callback_info_helpers", () => ({
  callback_map: {},
  mapInternalToDisplayNames: (x: any) => x,
  mapDisplayToInternalNames: (x: any) => x,
}));
vi.mock("../shared/errorUtils", () => ({
  parseErrorMessage: (e: any) => String(e),
}));

// Icons -> async factory & local React
vi.mock("@heroicons/react/outline", async () => {
  const React = await import("react");
  function ArrowLeftIcon() {
    return React.createElement("span");
  }
  (ArrowLeftIcon as any).displayName = "ArrowLeftIcon";
  function TrashIcon() {
    return React.createElement("span");
  }
  (TrashIcon as any).displayName = "TrashIcon";
  function RefreshIcon() {
    return React.createElement("span");
  }
  (RefreshIcon as any).displayName = "RefreshIcon";
  return { ArrowLeftIcon, TrashIcon, RefreshIcon };
});

// Heavy children -> async factories & local React
vi.mock("../organisms/RegenerateKeyModal", () => {
  function RegenerateKeyModal() {
    return null;
  }
  (RegenerateKeyModal as any).displayName = "RegenerateKeyModal";
  return { RegenerateKeyModal };
});
vi.mock("../object_permissions_view", () => {
  function ObjectPermissionsView() {
    return null;
  }
  (ObjectPermissionsView as any).displayName = "ObjectPermissionsView";
  return { __esModule: true, default: ObjectPermissionsView };
});
vi.mock("../logging_settings_view", () => {
  function LoggingSettingsView() {
    return null;
  }
  (LoggingSettingsView as any).displayName = "LoggingSettingsView";
  return { __esModule: true, default: LoggingSettingsView };
});
vi.mock("../common_components/AutoRotationView", () => {
  function AutoRotationView() {
    return null;
  }
  (AutoRotationView as any).displayName = "AutoRotationView";
  return { __esModule: true, default: AutoRotationView };
});

// Mock Next.js router to avoid "invariant expected app router to be mounted" error
vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    refresh: vi.fn(),
    back: vi.fn(),
    forward: vi.fn(),
    prefetch: vi.fn(),
  }),
}));

// Mock useTeams hook
vi.mock("@/app/(dashboard)/hooks/useTeams", () => ({
  default: vi.fn(() => ({
    teams: [],
    setTeams: vi.fn(),
  })),
}));

// Mock useProjects hook
vi.mock("@/app/(dashboard)/hooks/projects/useProjects", () => ({
  useProjects: vi.fn().mockReturnValue({ data: [], isLoading: false }),
}));

// Mock useUISettings hook
vi.mock("@/app/(dashboard)/hooks/uiSettings/useUISettings", () => ({
  useUISettings: vi.fn().mockReturnValue({ data: { values: {} }, isLoading: false }),
}));

// Mock useMCPServers hook (requires QueryClientProvider which is not available in this test)
vi.mock("@/app/(dashboard)/hooks/mcpServers/useMCPServers", () => ({
  useMCPServers: vi.fn().mockReturnValue({ data: [] }),
}));

vi.mock("@/app/(dashboard)/hooks/mcpServers/useMCPToolsets", () => ({
  useMCPToolsets: vi.fn().mockReturnValue({ data: [] }),
}));

// Mock useResetKeySpend hook (requires QueryClientProvider which is not available in this test)
vi.mock("@/app/(dashboard)/hooks/keys/useResetKeySpend", () => ({
  useResetKeySpend: vi.fn().mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
  }),
}));

vi.mock("@/app/(dashboard)/hooks/keys/useSetKeyBlockedState", () => ({
  useSetKeyBlockedState: vi.fn().mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
  }),
}));

// useQueryClient also needs a provider; the delete-path invalidation is covered in key_info_view.test.tsx
vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  };
});

// 隔离编辑表单子组件，验证父组件提交转换及提交后传回的预算周期；真实页面路径由密钥E2E覆盖。
vi.mock("./key_edit_view", async () => {
  const React = await import("react");
  // 接收父组件真实keyData和提交回调，展示周期原值；不重复实现业务格式化，不产生外部数据。
  function KeyEditView(props: any) {
    return React.createElement(
      "div",
      null,
      React.createElement("span", { "data-testid": "budget-reset-value" }, props.keyData.budget_duration ?? "null"),
      React.createElement(
        "button",
        {
          onClick: () => props.onSubmit((globalThis as any).__TEST_FORM_VALUES ?? {}),
        },
        "Mock Submit",
      ),
    );
  }
  (KeyEditView as any).displayName = "KeyEditViewMock";
  return { KeyEditView };
});

// ---- SUT import AFTER mocks ----
import KeyInfoView from "./key_info_view";

// ---- Test data helpers ----
const baseKeyData = {
  token_id: "tok_123",
  token: "tok_123",
  key_alias: "My Virtual Key",
  key_name: "sk-xxxx",
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
  spend: 0,
  max_budget: null,
  tpm_limit: null,
  rpm_limit: null,
  models: [] as string[],
  metadata: {} as Record<string, any>,
  object_permission: {} as Record<string, any>,
  auto_rotate: false,
  rotation_interval: null as any,
  last_rotation_at: null as any,
  key_rotation_at: null as any,
  next_rotation_at: null as any,
};

const renderView = (premiumUser: boolean) => {
  // Configure the mock for this test
  mockUseAuthorized.mockReturnValue({
    accessToken: "access_abc",
    userId: "user_1",
    userRole: "Admin",
    premiumUser,
    token: "token_123",
    userEmail: "test@example.com",
    disabledPersonalKeyCreation: false,
    showSSOBanner: false,
  });

  return render(
    <KeyInfoView
      keyId="tok_123"
      onClose={() => {}}
      keyData={baseKeyData as any}
      onKeyDataUpdate={() => {}}
      teams={[]}
    />,
  );
};

beforeEach(() => {
  vi.clearAllMocks();
  (globalThis as any).__TEST_FORM_VALUES = undefined;
});

// ---- Tests ----

/** 基础密钥编辑只发送现有契约字段；隔离表单和网络，验证实际父组件回调，不恢复已移除功能。每例重置mock，无外部数据。 */
describe("KeyInfoView current basic-edit contract", () => {
  beforeEach(() => {
    keyUpdateCallMock.mockReset().mockResolvedValue({});
  });

  /** 验证三个可编辑限额正常、零值和清空值；不改变预算规则，响应由网络mock隔离。 */
  for (const field of ["max_budget", "rpm_limit", "tpm_limit"]) {
    for (const [input, expected] of [["25", 25], ["0", 0], ["", null], [null, null]] as const) {
      it(`${field} preserves ${String(input)} as ${String(expected)}`, async () => {
        renderView(true);
        (globalThis as any).__TEST_FORM_VALUES = { [field]: input };
        fireEvent.click(screen.getByText("Mock Submit"));
        await waitFor(() => expect(keyUpdateCallMock).toHaveBeenCalledWith("access_abc", { key: "tok_123", [field]: expected }));
      });
    }
  }

  /** 旧表单残留字段不能重新写回；按原白名单保留真实支持字段，避免批次合并恢复假能力。 */
  it("omits retired and dedicated-flow fields while retaining supported values", async () => {
    renderView(true);
    const supported = { key_alias: "fixture", models: ["gpt-4o-mini"], allowed_routes: ["/v1/chat/completions"], route_template_id: "template", duration: "1d", auto_rotate: true, rotation_interval: "30d" };
    (globalThis as any).__TEST_FORM_VALUES = { ...supported, prompts: ["old"], skills: ["old"], guardrails: ["old"], metadata: { guardrails: ["old"] }, soft_budget: "25", budget_duration: "30d", max_parallel_requests: "10", mcp_toolsets: ["old"], object_permission: {} };
    fireEvent.click(screen.getByText("Mock Submit"));
    await waitFor(() => expect(keyUpdateCallMock).toHaveBeenCalledWith("access_abc", { key: "tok_123", ...supported }));
  });

  /** 返回值必须采用服务端最新记录，不用提交值猜测；重新渲染后的编辑器收到服务端值，无外部清理。 */
  it("refreshes editor state from the server response", async () => {
    keyUpdateCallMock.mockResolvedValueOnce({ budget_duration: "7d" });
    renderView(true);
    (globalThis as any).__TEST_FORM_VALUES = { key_alias: "renamed" };
    fireEvent.click(screen.getByText("Mock Submit"));
    await waitFor(() => expect(screen.getByTestId("budget-reset-value")).toHaveTextContent("7d"));
  });

  /** 服务端失败不能提示成功；验证实际catch路径，mock复位后无需清理。 */
  it("reports rejected updates without a success notification", async () => {
    keyUpdateCallMock.mockRejectedValueOnce(new Error("update rejected"));
    renderView(true);
    fireEvent.click(screen.getByText("Mock Submit"));
    await waitFor(() => expect(toast.fromError).toHaveBeenCalled());
    expect(toast.success).not.toHaveBeenCalled();
  });
});
