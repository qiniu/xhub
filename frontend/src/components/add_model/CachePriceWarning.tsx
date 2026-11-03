import { useT } from "@/i18n";
import type { PriceRate } from "./modelEditorPricing";

/** 判断费率表的缓存读写价格是否覆盖输入/输出使用的时段；参数为编辑器费率数组。
 * 返回兼容提示组件的零值或缺项标记；没有 Token 费率返回 null。仅计算展示，不修改费率或影响计费。 */
export function cachePricesFromRates(rates: PriceRate[]): { read: number | null; write: number | null } | null {
  const token = rates.filter((rate) => rate.measure === "token");
  if (!token.length) return null;
  const applicable = ["peak", "offpeak"].filter((window) => token.some((rate) =>
    ["input", "output"].includes(rate.side) && (!rate.window || rate.window === "all" || rate.window === window),
  ));
  const windows = applicable.length ? applicable : ["peak", "offpeak"];
  const hasRate = (side: "cache_read" | "cache_write", window: string) => token.some((rate) =>
    (rate.side === side || (side === "cache_read" && rate.side === "input" && rate.variant === "cached")) &&
    (!rate.window || rate.window === "all" || rate.window === window) && Number.isFinite(rate.usd) && rate.usd >= 0,
  );
  return { read: windows.every((window) => hasRate("cache_read", window)) ? 0 : null,
    write: windows.every((window) => hasRate("cache_write", window)) ? 0 : null };
}

/** 展示手动Token价格的缓存缺项；参数为表单原始读/写价格，返回提示或null。
 * 新旧模型编辑器调用；显式0有效、留空/非法值仍不完整。仅提示，不写默认价格或拦截保存。 */
export function CachePriceWarning({ read, write }: { read: unknown; write: unknown }) {
  const t = useT();
  const missing = [
    ["Cache read", read],
    ["Cache write", write],
  ]
    .filter(
      ([, value]) =>
        !(
          (typeof value === "number" || typeof value === "string") &&
          String(value).trim() !== "" &&
          Number.isFinite(Number(value)) &&
          Number(value) >= 0
        ),
    )
    .map(([label]) => t(String(label)));
  if (!missing.length) return null;
  return (
    <p role="status" className="text-sm text-amber-700" aria-label={t("cachePrice.warningTitle")}>
      {t("cachePrice.missing", { fields: missing.join(", ") })}
    </p>
  );
}
