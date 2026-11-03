import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { I18nProvider, LOCALE_COOKIE } from "@/i18n";
import { translate } from "@/i18n/translate";
import { CachePriceWarning, cachePricesFromRates } from "./CachePriceWarning";
afterEach(cleanup);
/** 验证费率表的缺项、零价、时间窗口与非 Token 费率；纯内存输入无需清理。 */
it("从费率表判断缓存价格完整性", () => {
  const base = [{ measure: "token", side: "input", window: "all", usd: 1 }, { measure: "token", side: "output", window: "all", usd: 1 }];
  expect(cachePricesFromRates(base)).toEqual({ read: null, write: null });
  expect(cachePricesFromRates([...base, { measure: "token", side: "cache_read", window: "all", usd: 0 }, { measure: "token", side: "cache_write", window: "all", usd: 0 }])).toEqual({ read: 0, write: 0 });
  expect(cachePricesFromRates([...base, { measure: "token", side: "cache_read", window: "peak", usd: 1 }, { measure: "token", side: "cache_write", window: "all", usd: 0 }])).toEqual({ read: null, write: 0 });
  expect(cachePricesFromRates([...base, { measure: "token", side: "cache_read", window: "", usd: 1 }, { measure: "token", side: "cache_write", window: "", usd: 1 }])).toEqual({ read: 0, write: 0 });
  expect(cachePricesFromRates([...base, { measure: "token", side: "cache_read", window: "all", usd: NaN }])).toEqual({ read: null, write: null });
  expect(cachePricesFromRates([{ measure: "second", side: "output", window: "all", usd: 1 }])).toBeNull();
});
/** 用真实双语翻译验证缺失、非法、零价及完整价格；只渲染组件，自动卸载并重置语言缓存。 */
for (const locale of ["en", "zh-CN"] as const) {
  for (const [read, write, missing] of [
    [undefined, null, true],
    ["", " ", true],
    [-1, 1, true],
    [false, 1, true],
    ["bad", 1, true],
    [0, "0", false],
    ["0.1", 1, false],
  ] as const) {
    it(`cache price warning ${locale} ${String(read)} ${String(write)}`, () => {
      localStorage.removeItem(LOCALE_COOKIE);
      document.cookie = `${LOCALE_COOKIE}=; path=/; max-age=0`;
      render(
        <I18nProvider initialLocale={locale}>
          <CachePriceWarning read={read} write={write} />
        </I18nProvider>,
      );
      const title = translate(locale, "cachePrice.warningTitle");
      if (missing) {
        expect(screen.getByRole("status", { name: title })).toBeVisible();
        expect(screen.getByRole("status")).not.toHaveTextContent("{fields}");
      } else expect(screen.queryByRole("status")).toBeNull();
    });
  }
}
