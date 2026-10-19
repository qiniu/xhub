import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../tests/test-utils";
import { KeyResponse } from "../key_team_helpers/key_list";
import { KeyEditView } from "./key_edit_view";
vi.mock("../networking", async (load) => ({
  ...(await load<typeof import("../networking")>()),
  modelAvailableCall: vi.fn().mockResolvedValue({ data: [] }),
  getRouteTemplatesCall: vi.fn().mockResolvedValue([]),
  getRouteTemplateBindingCall: vi.fn().mockResolvedValue({ route_template_id: "" }),
}));
describe("budget precision in the direct editor", () => {
  /** 金额边界回归；给定小数、整数、零或共享额度，编辑框必须完整保留精度而不引用团队额度，DOM 自动清理。 */
  it.each([0.00001, 0.1, 100, 0, null])("preserves budget %s", (max_budget) => {
    const key = {
      token: "hash",
      key_alias: "budget",
      models: [],
      max_budget,
      team_max_budget: 1200,
    } as unknown as KeyResponse;
    renderWithProviders(
      <KeyEditView
        keyData={key}
        accessToken={null}
        userID={null}
        userRole={null}
        onCancel={vi.fn()}
        onSubmit={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("Max Budget (USD)")).toHaveValue(max_budget);
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
  });
});
