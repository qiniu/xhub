import type { Locale } from "@/i18n/translate";
import type { PluginSource } from "./types";
import { getSourceLink } from "./helpers";

/** 为Skill详情生成源码定位；source为原始来源配置，返回链接或null，不访问远程仓库。
 * 保留git-subdir原有main分支子目录定位；其他来源复用公共规则，不借展示修复推断安装能力。
 */
export function skillDetailSourceLink(source?: PluginSource): string | null {
  const base = source ? getSourceLink(source) : null;
  return base && source?.source === "git-subdir" && source.path ? `${base}/tree/main/${source.path}` : base;
}

/** 将详情添加日期按界面locale格式化；value缺失返回undefined、非法日期保留原始值，供元数据表显示。
 * 使用浏览器本地时区延续原页面语义，不修改API时间戳、不影响持久化；语言切换时重新调用。
 */
export function skillDetailDate(value: string | undefined, locale: Locale): string | undefined {
  if (!value) return value;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString(locale);
}
