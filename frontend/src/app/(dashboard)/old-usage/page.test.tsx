import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import OldUsagePage from "./page";
import { I18nProvider } from "@/i18n/I18nProvider";
import { translate } from "@/i18n/translate";
const mocks = vi.hoisted(() => ({ get: vi.fn(), auth: vi.fn() }));
vi.mock("@/components/networking", () => ({ apiClient: { get: mocks.get } }));
vi.mock("@/app/(dashboard)/hooks/useAuthorized", () => ({ default: mocks.auth }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
/** 旧页面在成功、缺失或未就绪会话下均不得伪造统计或调用旧接口；双语标题、说明及真实用量链接必须匹配，框架清理DOM。 */
describe("legacy usage capability notice", () => {
  for (const locale of ["en", "zh-CN"] as const)
    for (const token of ["session-token", null, undefined]) {
      it(`${locale} session ${String(token)} preserves capability boundary`, () => {
        mocks.auth.mockReturnValue({ accessToken: token });
        render(
          <I18nProvider initialLocale={locale}>
            <OldUsagePage />
          </I18nProvider>,
        );
        expect(screen.getByRole("heading")).toHaveTextContent(translate(locale, "legacyUsage.title"));
        expect(screen.getByRole("status")).toHaveTextContent(translate(locale, "legacyUsage.unavailable"));
        expect(screen.getByRole("link", { name: translate(locale, "legacyUsage.openUsage") })).toHaveAttribute(
          "href",
          "/ui/usage",
        );
        expect(mocks.auth).toHaveBeenCalled();
        expect(mocks.get).not.toHaveBeenCalled();
      });
    }
});
