import { beforeEach, describe, expect, it } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { I18nProvider, translate } from "@/i18n";
import { KeyBudgetStatus } from "./KeyBudgetStatus";

// 双语验证软预算说明、动态小额、未知状态和空列表；无后端数据，逐例清理 DOM 与语言存储。
describe("KeyBudgetStatus", () => {
  beforeEach(() => {
    cleanup();
    localStorage.clear();
    document.cookie = "xhub_locale=; max-age=0; path=/";
  });
  for (const locale of ["zh-CN", "en"] as const) {
    it("shows soft budget policy and unknown reservations in " + locale, () => {
      render(
        <I18nProvider initialLocale={locale}>
          <KeyBudgetStatus
            pending={0.000062}
            reservations={[{ reservation_id: "attempt-用户", amount: 0.000062, state: "future-state" }]}
          />
        </I18nProvider>,
      );
      expect(screen.getByText(translate(locale, "hardBudget.contract"))).toBeVisible();
      expect(screen.getByText(translate(locale, "hardBudget.pending", { amount: 0.000062 }))).toBeVisible();
      expect(screen.queryByText(/4096/)).not.toBeInTheDocument();
      expect(screen.getByText("attempt-用户")).toBeVisible();
      expect(screen.getByRole("listitem")).toHaveTextContent(
        translate(locale, "hardBudget.attempt", {
          amount: 0.000062,
          state: translate(locale, "hardBudget.states.uncertain"),
        }),
      );
    });
    it("shows zero without inventing unknown reservations in " + locale, () => {
      render(
        <I18nProvider initialLocale={locale}>
          <KeyBudgetStatus reservations={null} />
        </I18nProvider>,
      );
      expect(screen.getByText(translate(locale, "hardBudget.pending", { amount: 0 }))).toBeVisible();
      expect(screen.queryByRole("list")).not.toBeInTheDocument();
    });
  }
});
