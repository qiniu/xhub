import { useT } from "@/i18n";

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
