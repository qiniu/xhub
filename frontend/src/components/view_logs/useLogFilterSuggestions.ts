import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { getProxyBaseUrl } from "@/components/networking";
import { createApiClient } from "@/lib/http/client";
import type { LogsWindow } from "./log_filter_logic";

export type LogSuggestionField = "model" | "key_alias" | "key_hash" | "supplier" | "inbound_endpoint";
export type LogSuggestions = { data: string[]; page: number; page_size: number; has_more: boolean };
// 独立客户端沿用公共请求规范，且不注册全局通知；候选失败由筛选控件局部提示。
const suggestionsClient = createApiClient({ getBaseUrl: getProxyBaseUrl });

/** 从当前账号可见的日志中获取指定字段的候选值；field、window、query 和 open 指定查询上下文。
 * 返回候选、加载及失败状态；筛选抽屉调用，关闭时不请求，输入防抖并取消旧请求。
 * 服务端负责授权和分页；账户或时间范围切换不复用其他查询的候选，异常不影响手动输入。 */
export function useLogFilterSuggestions(field: LogSuggestionField, window: LogsWindow, query: string, open: boolean) {
  const { accessToken, userId } = useAuthorized();
  const [debounced, setDebounced] = useState(query);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query), 250);
    return () => clearTimeout(timer);
  }, [query]);
  const result = useQuery<LogSuggestions>({
    queryKey: ["log-filter-options", accessToken, userId, field, window.start_date, window.end_date, debounced],
    enabled: Boolean(accessToken && userId && open && debounced === query),
    retry: false,
    staleTime: 0,
    queryFn: async ({ signal }) => {
      const body = await suggestionsClient.get<unknown>("/spend/logs/ui/filter-options", {
        query: { field, q: debounced, start_date: window.start_date, end_date: window.end_date, page: 1, page_size: 50 },
        accessToken, signal, headers: { "Cache-Control": "no-store" },
      });
      if (!body || typeof body !== "object") throw new Error("invalid_filter_options");
      const parsed = body as LogSuggestions;
      if (!Array.isArray(parsed.data) || !parsed.data.every((item) => typeof item === "string") || typeof parsed.has_more !== "boolean" || parsed.page !== 1 || !Number.isInteger(parsed.page_size) || parsed.page_size < 1 || parsed.page_size > 50 || parsed.data.length > parsed.page_size) {
        throw new Error("invalid_filter_options");
      }
      return parsed;
    },
  });
  // 防抖尚未到期时不展示旧搜索结果；错误或切换身份同样不展示过期候选。
  return { ...result, data: open && debounced === query && !result.isError && !result.isFetching ? result.data : undefined,
    isPendingQuery: open && (debounced !== query || result.isFetching), isUnavailable: open && (!accessToken || !userId) };
}
