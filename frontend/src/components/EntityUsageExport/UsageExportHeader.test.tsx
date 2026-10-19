import { I18nProvider, translate } from "@/i18n";
import { renderWithProviders, screen } from "../../../tests/test-utils";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import UsageExportHeader from "./UsageExportHeader";
import type { EntitySpendData } from "./types";

vi.mock("./EntityUsageExportModal", () => ({
  default: ({ isOpen, onClose }: { isOpen: boolean; onClose: () => void }) =>
    isOpen ? (
      <div data-testid="export-modal">
        <button onClick={onClose}>Close</button>
      </div>
    ) : null,
}));

const defaultProps = {
  dateValue: { from: new Date("2025-01-01"), to: new Date("2025-01-31") },
  entityType: "team" as const,
  spendData: {
    results: [],
    metadata: {
      total_spend: 0,
      total_api_requests: 0,
      total_successful_requests: 0,
      total_failed_requests: 0,
      total_tokens: 0,
    },
  } satisfies EntitySpendData,
};

describe("UsageExportHeader", () => {
  it("should render", () => {
    renderWithProviders(<UsageExportHeader {...defaultProps} />);
    expect(screen.getByRole("button", { name: /export data/i })).toBeInTheDocument();
  });

  it("should open the export modal when the export button is clicked", async () => {
    const user = userEvent.setup();
    renderWithProviders(<UsageExportHeader {...defaultProps} />);
    await user.click(screen.getByRole("button", { name: /export data/i }));
    expect(screen.getByTestId("export-modal")).toBeInTheDocument();
  });

  it("should close the export modal when onClose is called", async () => {
    const user = userEvent.setup();
    renderWithProviders(<UsageExportHeader {...defaultProps} />);
    await user.click(screen.getByRole("button", { name: /export data/i }));
    await user.click(screen.getByRole("button", { name: /close/i }));
    expect(screen.queryByTestId("export-modal")).not.toBeInTheDocument();
  });

  it("should not show filter dropdown when showFilters is false", () => {
    renderWithProviders(<UsageExportHeader {...defaultProps} showFilters={false} />);
    expect(screen.queryByText(/filter/i)).not.toBeInTheDocument();
  });

  it("should show filter dropdown when showFilters is true and options provided", () => {
    renderWithProviders(
      <UsageExportHeader
        {...defaultProps}
        showFilters
        filterLabel="Team"
        filterPlaceholder="Select teams"
        filterOptions={[
          { label: "Team A", value: "team-a" },
          { label: "Team B", value: "team-b" },
        ]}
        onFiltersChange={vi.fn()}
      />,
    );
    expect(screen.getByText("Team")).toBeInTheDocument();
  });

  it("should render a caller-supplied filter and its label without any built-in options", () => {
    renderWithProviders(
      <UsageExportHeader
        {...defaultProps}
        filterLabel="Filter by user"
        filterSlot={<div data-testid="custom-filter" />}
      />,
    );

    expect(screen.getByText("Filter by user")).toBeInTheDocument();
    expect(screen.getByTestId("custom-filter")).toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });

  it("should keep the filter visible and disabled with an explanation when the caller has no options", () => {
    renderWithProviders(
      <UsageExportHeader
        {...defaultProps}
        entityType="tag"
        showFilters
        filterLabel="Filter by tag"
        filterPlaceholder="Select tag to filter..."
        filterOptions={[]}
        onFiltersChange={vi.fn()}
      />,
    );

    expect(screen.getByText("Filter by tag")).toBeInTheDocument();
    const input = screen.getByPlaceholderText("No usage options in this range");
    expect(input).toBeDisabled();
    expect(screen.queryByPlaceholderText("Select tag to filter...")).not.toBeInTheDocument();
  });

  it("should stay usable when a carried-over selection outlives its options", async () => {
    const user = userEvent.setup();
    const onFiltersChange = vi.fn();
    renderWithProviders(
      <UsageExportHeader
        {...defaultProps}
        entityType="tag"
        showFilters
        filterLabel="Filter by tag"
        filterPlaceholder="Select tag to filter..."
        filterOptions={[]}
        selectedFilters={["prod"]}
        onFiltersChange={onFiltersChange}
      />,
    );

    expect(screen.getByPlaceholderText("No usage options in this range")).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "Clear Filter by tag" }));
    expect(onFiltersChange).toHaveBeenCalledWith([]);
  });

  it("should leave the filter enabled with its normal placeholder when options exist", () => {
    renderWithProviders(
      <UsageExportHeader
        {...defaultProps}
        entityType="tag"
        showFilters
        filterLabel="Filter by tag"
        filterPlaceholder="Select tag to filter..."
        filterOptions={[{ label: "prod", value: "prod" }]}
        onFiltersChange={vi.fn()}
      />,
    );

    const input = screen.getByPlaceholderText("Select tag to filter...");
    expect(input).toBeEnabled();
    expect(screen.queryByPlaceholderText("No tags with usage in this range")).not.toBeInTheDocument();
  });
});

/** 前置已打开的导出和仍可用的筛选；加载失败后禁用导出、关闭旧窗口且保留筛选，组件自动清理。 */
it("keeps filters usable while disabling incomplete exports", async () => {
  const user=userEvent.setup();
  const {rerender}=renderWithProviders(<UsageExportHeader {...defaultProps} filterSlot={<button>Clear filter</button>} />);
  await user.click(screen.getByRole("button",{name:"Export Data"}));
  expect(screen.getByTestId("export-modal")).toBeVisible();
  rerender(<UsageExportHeader {...defaultProps} disableExport filterSlot={<button>Clear filter</button>} />);
  expect(screen.getByRole("button",{name:"Export Data"})).toBeDisabled();
  expect(screen.getByRole("button",{name:"Clear filter"})).toBeEnabled();
  expect(screen.queryByTestId("export-modal")).not.toBeInTheDocument();
});

/** 前置空选项和禁用导出；双语验证可访问占位符及按钮名称，组件自动卸载，无后台数据。 */
it.each(["en", "zh-CN"] as const)("%s localizes empty filter options while export is disabled", locale => {
  renderWithProviders(<I18nProvider initialLocale={locale}><UsageExportHeader {...defaultProps} showFilters disableExport /></I18nProvider>);
  expect(screen.getByPlaceholderText(translate(locale,"usageLoad.noOptions"))).toBeVisible();
  expect(screen.getByRole("button",{name:translate(locale,"Export Data")})).toBeDisabled();
});
