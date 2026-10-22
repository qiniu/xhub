import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { translate } from "@/i18n/translate";
import PersonalBudgetSummary from "./PersonalBudgetSummary";
const state = vi.hoisted(() => ({ locale: "en" as "en" | "zh-CN", result: {} as any }));
vi.mock("@/i18n", () => ({
  useT: () => (key: string, values?: Record<string, unknown>) => translate(state.locale, key, values as any),
}));
vi.mock("@/app/(dashboard)/hooks/users/useCurrentUser", () => ({ useCurrentUser: () => state.result }));
afterEach(cleanup);
// 双语展示个人总额/已用/剩余并保留 0 和负余额；纯组件状态，不写外部数据，每例清理 DOM。
it.each(["en", "zh-CN"] as const)("personal budget states %s", (locale) => {
  state.locale = locale;
  for (const limit of [500, 0, null]) {
    state.result = { data: { max_budget: limit, spend: 10 }, isLoading: false, isError: false };
    const view = render(<PersonalBudgetSummary />);
    expect(screen.getByRole("status").textContent).toContain("$10.00");
    expect(screen.getByRole("status").textContent).toContain(
      limit === null ? translate(locale, "quotaGuide.personalUnlimited") : `$${(limit - 10).toFixed(2)}`,
    );
    view.unmount();
  }
  for (const [isLoading, isError] of [
    [true, false],
    [false, true],
    [false, false],
  ]) {
    state.result = { isLoading, isError };
    const view = render(<PersonalBudgetSummary />);
    expect(screen.getByRole("status").textContent).toBe(
      translate(locale, isLoading ? "quotaGuide.personalLoading" : "quotaGuide.personalUnavailable"),
    );
    view.unmount();
  }
});
