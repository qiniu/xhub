import { render, screen, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { translate } from "@/i18n/translate";
import Page from "./page";

const state = vi.hoisted(() => ({
  locale: "en" as "en" | "zh-CN",
  query: "code=unverified&state=local",
  write: vi.fn(),
}));
vi.mock("@/i18n", () => ({ useT: () => (key: string) => translate(state.locale, key) }));
vi.mock("next/navigation", () => ({ useSearchParams: () => new URLSearchParams(state.query) }));
vi.mock("@/utils/secureStorage", () => ({ getSecureItem: () => null, setSecureItem: state.write }));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  state.write.mockReset();
});

// 双语验证授权回调只转交未验证结果，错误和存储失败均不能显示已授权/已保存；无HTTP或真实凭据，测试后还原DOM和location。
describe("MCP callback does not claim completed authorization", () => {
  for (const locale of ["en", "zh-CN"] as const) {
    for (const scenario of [
      { name: "unverified code", query: "code=unverified&state=local", failedStorage: false },
      { name: "provider denial", query: "error=access_denied&state=local", failedStorage: false },
      { name: "missing result", query: "", failedStorage: false },
      { name: "unavailable storage", query: "code=unverified&state=local", failedStorage: true },
    ]) {
      it(locale + " " + scenario.name, () => {
        state.locale = locale;
        state.query = scenario.query;
        if (scenario.failedStorage)
          state.write.mockImplementation(() => {
            throw new Error("unavailable storage");
          });
        const replace = vi.fn();
        vi.spyOn(window, "location", "get").mockReturnValue({
          pathname: "/ui/mcp/oauth/callback",
          origin: "https://example.invalid",
          replace,
        } as unknown as Location);
        render(<Page />);
        expect(screen.getByRole("heading", { name: translate(locale, "mcpOAuthCallback.title") })).toBeVisible();
        expect(screen.getByText(translate(locale, "mcpOAuthCallback.pending"))).toBeVisible();
        expect(screen.getByText(translate(locale, "mcpOAuthCallback.returnHelp"))).toBeVisible();
        expect(
          screen.queryByText(translate(locale === "en" ? "zh-CN" : "en", "mcpOAuthCallback.pending")),
        ).not.toBeInTheDocument();
        expect(
          screen.queryByText(
            translate(locale, "Authorization complete. You may close this window and return to the LiteLLM dashboard."),
          ),
        ).not.toBeInTheDocument();
        expect(replace).toHaveBeenCalledWith("/ui");
        expect(state.write.mock.calls[0][1]).not.toContain("access_token");
        const params = new URLSearchParams(scenario.query);
        expect(JSON.parse(state.write.mock.calls[0][1])).toMatchObject({
          code: params.get("code"),
          error: params.get("error"),
          state: params.get("state"),
        });
      });
    }
  }
});
